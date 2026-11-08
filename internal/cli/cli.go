// Package cli is the git id command: argument parsing and what people read.
package cli

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"runtime/debug"
	"slices"

	"github.com/spf13/pflag"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/corpus"
	"github.com/creasty/git-id/internal/git"
	"github.com/creasty/git-id/internal/hook"
	"github.com/creasty/git-id/internal/ids"
)

const usage = `git id: mint, resolve and check IDs that cross-reference documents

Usage:
  git id new [<prefix>] [-n <count>]  mint IDs nobody has used
  git id check <id>... [--json]       show where IDs are defined and referenced
  git id resolve [<path>...]          replace human placeholders (first digit 0) in files
        --dry-run                     print the replacements without writing them
        --all                         also replace agent placeholders (first digit 1)
  git id integrity                    check every ID in the working tree; exit 1 on errors
  git id harvest                      refresh the caches minting reads
  git id hook run claude|codex        resolve agent placeholders in a PreToolUse hook
  git id hook install claude|codex    add that hook to .claude/settings.json or .codex/hooks.json
  git id --skill-instructions         describe this repository's IDs for an agent skill
  git id version

Configuration: .git-id.toml at the root of the repository.
Documentation: https://github.com/creasty/git-id
`

// Exit codes.
const (
	exitOK     = 0
	exitFailed = 1 // integrity errors, or placeholders left by resolve
	exitUsage  = 2
)

// env is what a command runs against.
type env struct {
	dir            string
	stdin          io.Reader
	stdout, stderr io.Writer
}

// Run executes git id with args (without the program name) in dir and returns the exit code.
func Run(args []string, dir string, stdin io.Reader, stdout, stderr io.Writer) int {
	e := &env{dir: dir, stdin: stdin, stdout: stdout, stderr: stderr}
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return exitUsage
	}
	cmd, rest := args[0], args[1:]
	var err error
	switch cmd {
	case "new":
		err = e.new(rest)
	case "check":
		err = e.check(rest)
	case "resolve":
		return e.resolve(rest)
	case "integrity":
		return e.integrity(rest)
	case "harvest":
		err = e.harvest(rest)
	case "hook":
		err = e.hook(rest)
	case "--skill-instructions":
		return e.skillInstructions()
	case "version", "--version":
		fmt.Fprintln(stdout, version())
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
	default:
		fmt.Fprintf(stderr, "git id: unknown command %q\n\n%s", cmd, usage)
		return exitUsage
	}
	return e.exit(err)
}

// usageError is a mistake in the invocation rather than a failure of the work.
type usageError struct{ error }

// helpShown ends a command whose -h was requested.
var helpShown = errors.New("help shown")

func (e *env) exit(err error) int {
	var ue usageError
	switch {
	case err == nil, errors.Is(err, helpShown):
		return exitOK
	case errors.As(err, &ue):
		fmt.Fprintf(e.stderr, "git id: %v\n", err)
		return exitUsage
	default:
		fmt.Fprintf(e.stderr, "git id: %v\n", err)
		return exitFailed
	}
}

// flags parses a command's flags. Flags may come after positional arguments.
func (e *env) flags(name, synopsis string, args []string, define func(*pflag.FlagSet)) ([]string, error) {
	fs := pflag.NewFlagSet(name, pflag.ContinueOnError)
	fs.SetOutput(e.stderr)
	fs.Usage = func() { fmt.Fprintf(e.stderr, "Usage: git id %s\n%s", synopsis, fs.FlagUsages()) }
	if define != nil {
		define(fs)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, pflag.ErrHelp) {
			return nil, helpShown
		}
		return nil, usageError{err}
	}
	return fs.Args(), nil
}

// open finds the repository around the working directory and loads its configuration.
func (e *env) open() (string, *config.Config, error) {
	root, err := git.FindRoot(e.dir)
	if err != nil {
		return "", nil, err
	}
	cfg, err := config.Load(root)
	if errors.Is(err, config.ErrNotFound) {
		return "", nil, fmt.Errorf("%s not found at %s; see https://github.com/creasty/git-id#configuration", config.FileName, root)
	}
	if err != nil {
		return "", nil, err
	}
	return root, cfg, nil
}

// openNotation is open for the commands that read the notation, which takes a prefix: without
// one, an ID cannot be told apart from an ordinary word.
func (e *env) openNotation(cmd string) (string, *config.Config, error) {
	root, cfg, err := e.open()
	if err == nil && !cfg.Syntax.HasPrefixes() {
		err = fmt.Errorf("git id %s needs [prefixes] in %s: without a prefix, IDs cannot be told apart from ordinary words, and git id only mints them unique", cmd, config.FileName)
	}
	return root, cfg, err
}

func (e *env) new(args []string) error {
	var n int
	rest, err := e.flags("new", "new [<prefix>] [-n <count>]", args, func(fs *pflag.FlagSet) {
		fs.IntVarP(&n, "count", "n", 1, "how many IDs to mint")
	})
	if err != nil {
		return err
	}
	if len(rest) > 1 || n < 1 {
		return usageError{errors.New("usage: git id new [<prefix>] [-n <count>]")}
	}
	root, cfg, err := e.open()
	if err != nil {
		return err
	}
	prefix := ""
	if len(rest) == 1 {
		prefix = rest[0]
	}
	if cfg.Syntax.HasPrefixes() && prefix == "" {
		return usageError{fmt.Errorf("a prefix is required: %s", describePrefixes(cfg.Syntax))}
	}
	m := &ids.Minter{Syntax: cfg.Syntax, Load: corpus.New(root, cfg).Taken}
	for range n {
		id, err := m.Mint(prefix)
		if err != nil {
			return err
		}
		fmt.Fprintln(e.stdout, id)
	}
	return nil
}

// describePrefixes lists the registered prefixes with their descriptions.
func describePrefixes(s *ids.Syntax) string {
	out := ""
	for i, name := range s.PrefixNames() {
		if i > 0 {
			out += ", "
		}
		out += name
		if p, _ := s.Prefix(name); p.Description != "" {
			out += " (" + p.Description + ")"
		}
	}
	return out
}

func (e *env) harvest(args []string) error {
	if _, err := e.flags("harvest", "harvest", args, nil); err != nil {
		return err
	}
	root, cfg, err := e.open()
	if err != nil {
		return err
	}
	c := corpus.New(root, cfg)
	taken, err := c.Taken()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "taken\t%d\n", len(taken))
	head, err := c.HeadCachePath()
	if err != nil {
		return err
	}
	fmt.Fprintf(e.stdout, "head cache\t%s\n", head)
	if cfg.History {
		history, err := c.HistoryCachePath()
		if err != nil {
			return err
		}
		fmt.Fprintf(e.stdout, "history cache\t%s\n", history)
	}
	return nil
}

// hook runs an agent's hook, which its settings call before every edit, or installs that
// call into the settings.
func (e *env) hook(args []string) error {
	const synopsis = "hook run|install claude|codex"
	rest, err := e.flags("hook", synopsis, args, nil)
	if err != nil {
		return err
	}
	if len(rest) != 2 {
		return usageError{errors.New("usage: git id " + synopsis)}
	}
	action, agent := rest[0], rest[1]
	if !slices.Contains(hook.Agents, agent) {
		return usageError{fmt.Errorf("unknown agent %q: claude or codex", agent)}
	}
	switch action {
	case "run":
		if agent == "claude" {
			return hook.Claude(e.stdin, e.stdout)
		}
		return hook.Codex(e.stdin, e.stdout)
	case "install":
		return e.install(agent)
	}
	return usageError{fmt.Errorf("unknown hook action %q: run or install", action)}
}

// install adds an agent's hook to the project settings at the root of the working tree.
func (e *env) install(agent string) error {
	root, err := git.FindRoot(e.dir)
	if err != nil {
		return err
	}
	file, changed, err := hook.Install(root, agent)
	if err != nil {
		return err
	}
	status := "already there"
	if changed {
		status = "added"
	}
	fmt.Fprintf(e.stdout, "%s: %s\n", filepath.ToSlash(file), status)
	return nil
}

func version() string {
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return "git-id " + info.Main.Version
	}
	return "git-id (devel)"
}
