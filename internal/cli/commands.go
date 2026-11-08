package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/pflag"

	"github.com/creasty/git-id/internal/corpus"
	"github.com/creasty/git-id/internal/ids"
)

// Findings are written the way compilers and linters write them, place first
// (`path:line: what`), since that is what terminals, editors and CI logs link and parse. A
// finding with more places lists them on indented lines below its own. Counts go to stderr,
// so stdout holds findings only.

// integrity reports every finding, errors before warnings, each in path order, and fails on
// errors; warnings never fail it.
func (e *env) integrity(args []string) int {
	if _, err := e.flags("integrity", "integrity", args, nil); err != nil {
		return e.exit(err)
	}
	root, cfg, err := e.openNotation("integrity")
	if err != nil {
		return e.exit(err)
	}
	files, err := corpus.New(root, cfg).Worktree()
	if err != nil {
		return e.exit(err)
	}
	findings := ids.BuildGraph(cfg.Syntax, files).Findings
	sort.SliceStable(findings, func(i, j int) bool {
		a, b := findings[i], findings[j]
		if a.Check.IsError() != b.Check.IsError() {
			return a.Check.IsError()
		}
		if a.Site.Path != b.Site.Path {
			return a.Site.Path < b.Site.Path
		}
		return a.Site.Line < b.Site.Line
	})
	errorCount := 0
	for _, f := range findings {
		if f.Check.IsError() {
			errorCount++
		}
		fmt.Fprintf(e.stdout, "%s: %s %s %s\n", f.Site, f.Check, f.Site.ID, f.Check.Description())
		for _, s := range f.Related {
			fmt.Fprintf(e.stdout, "  %s: %s\n", s, f.Check.RelatedDescription())
		}
	}
	fmt.Fprintf(e.stderr, "%s, %s\n", plural(errorCount, "error"), plural(len(findings)-errorCount, "warning"))
	if errorCount > 0 {
		return exitFailed
	}
	return exitOK
}

// check answers, for each ID: where it is defined and referenced in the working tree, or,
// when it is written nowhere there, whether minting must still avoid it (reserved: it is in
// HEAD, the index or the history) or it is unused.
func (e *env) check(args []string) error {
	var asJSON bool
	idArgs, err := e.flags("check", "check <id>... [--json]", args, func(fs *pflag.FlagSet) {
		fs.BoolVar(&asJSON, "json", false, "print JSON")
	})
	if err != nil {
		return err
	}
	if len(idArgs) == 0 {
		return usageError{fmt.Errorf("usage: git id check <id>... [--json]")}
	}
	root, cfg, err := e.openNotation("check")
	if err != nil {
		return err
	}
	c := corpus.New(root, cfg)
	files, err := c.Worktree()
	if err != nil {
		return err
	}
	g := ids.BuildGraph(cfg.Syntax, files)
	m := &ids.Minter{Syntax: cfg.Syntax, Load: c.Taken}

	type answer struct {
		id       string
		def      *ids.Site
		refs     []ids.Site
		reserved bool
	}
	answers := make([]answer, len(idArgs))
	for i, id := range idArgs {
		a := answer{id: id, refs: g.ReferencesTo(id)}
		if s, ok := g.DefinitionOf(id); ok {
			a.def = &s
		}
		if a.def == nil && len(a.refs) == 0 {
			if a.reserved, err = m.Used(id); err != nil {
				return err
			}
		}
		answers[i] = a
	}

	if asJSON {
		type place struct {
			Path string `json:"path"`
			Line int    `json:"line"`
		}
		var buf bytes.Buffer
		buf.WriteString("{")
		for i, a := range answers {
			var v any
			switch {
			case a.reserved:
				v = map[string]bool{"reserved": true}
			case a.def != nil || len(a.refs) > 0:
				refs := []place{}
				for _, r := range a.refs {
					refs = append(refs, place{r.Path, r.Line})
				}
				var def *place
				if a.def != nil {
					def = &place{a.def.Path, a.def.Line}
				}
				v = map[string]any{"definition": def, "references": refs}
			}
			key, _ := json.Marshal(a.id)
			val, _ := json.Marshal(v)
			if i > 0 {
				buf.WriteString(",")
			}
			fmt.Fprintf(&buf, "\n  %s: %s", key, val)
		}
		buf.WriteString("\n}\n")
		_, err := e.stdout.Write(buf.Bytes())
		return err
	}
	// One block per ID, since the question is about IDs: what it is, then its places.
	for _, a := range answers {
		switch {
		case a.reserved:
			fmt.Fprintf(e.stdout, "%s: reserved\n", a.id)
		case a.def == nil && len(a.refs) == 0:
			fmt.Fprintf(e.stdout, "%s: unused\n", a.id)
		default:
			def := "defined"
			if a.def == nil {
				def = "no definition"
			}
			count := "no references"
			if len(a.refs) > 0 {
				count = plural(len(a.refs), "reference")
			}
			fmt.Fprintf(e.stdout, "%s: %s, %s\n", a.id, def, count)
			if a.def != nil {
				fmt.Fprintf(e.stdout, "  %s: definition\n", a.def)
			}
			for _, r := range a.refs {
				fmt.Fprintf(e.stdout, "  %s: reference\n", r)
			}
		}
	}
	return nil
}

// resolve replaces human placeholders in the working tree and writes the files back.
//
// One resolver serves every file, so a slot number is the same ID across files. Its
// assignments are not kept anywhere: they last for this run. What cannot be replaced (the
// agent's placeholders, a fresh reference, a hand-written definition) is reported and the
// rest is still written, since none of those can be fixed here; integrity reports what is
// left.
func (e *env) resolve(args []string) int {
	var dryRun, sweep bool
	paths, err := e.flags("resolve", "resolve [<path>...] [--dry-run] [--all]", args, func(fs *pflag.FlagSet) {
		fs.BoolVar(&dryRun, "dry-run", false, "print the replacements without writing them")
		fs.BoolVar(&sweep, "all", false, "also replace agent placeholders (first digit 1)")
	})
	if err != nil {
		return e.exit(err)
	}
	root, cfg, err := e.openNotation("resolve")
	if err != nil {
		return e.exit(err)
	}
	selected, err := e.selector(root, paths)
	if err != nil {
		return e.exit(err)
	}
	c := corpus.New(root, cfg)
	files, err := c.Worktree()
	if err != nil {
		return e.exit(err)
	}
	m := &ids.Minter{Syntax: cfg.Syntax, Load: c.Taken}
	r := &ids.Resolver{Syntax: cfg.Syntax, Scope: ids.Human, Sweep: sweep, Mint: m.Mint}
	for _, f := range files {
		if !selected(f.Path) || !hasPlaceholder(f) {
			continue
		}
		full := filepath.Join(root, filepath.FromSlash(f.Path))
		info, err := os.Stat(full)
		if err != nil {
			return e.exit(err)
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return e.exit(err)
		}
		out, err := r.Resolve(data, f.Path)
		if err != nil {
			return e.exit(err)
		}
		if !dryRun && !bytes.Equal(out, data) {
			if err := os.WriteFile(full, out, info.Mode().Perm()); err != nil {
				return e.exit(err)
			}
		}
	}

	touched := map[string]bool{}
	for _, rep := range r.Replaced {
		fmt.Fprintf(e.stdout, "%s:%d: replaced %s -> %s\n", rep.Path, rep.Occurrence.Line, rep.Occurrence.Token(), rep.Token())
		touched[rep.Path] = true
	}
	for _, rej := range r.Rejected {
		fmt.Fprintf(e.stdout, "%s:%d: left %s: %s\n", rej.Path, rej.Occurrence.Line, rej.Occurrence.Token(), rej.Reason.Description())
	}
	summary := fmt.Sprintf("replaced %s in %s, left %d", plural(len(r.Replaced), "placeholder"), plural(len(touched), "file"), len(r.Rejected))
	if dryRun {
		summary += " (dry run: nothing written)"
	}
	if len(r.Rejected) > 0 {
		summary += ". " + ids.Guide
	}
	fmt.Fprintln(e.stderr, summary)
	if len(r.Rejected) > 0 {
		return exitFailed
	}
	return exitOK
}

func hasPlaceholder(f ids.File) bool {
	for _, o := range f.Occurrences {
		if o.IsPlaceholder() {
			return true
		}
	}
	return false
}

// selector returns whether a repository path is under one of the given paths, which are
// relative to the working directory. No paths select everything.
func (e *env) selector(root string, paths []string) (func(string) bool, error) {
	if len(paths) == 0 {
		return func(string) bool { return true }, nil
	}
	var prefixes []string
	for _, p := range paths {
		abs := p
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(e.dir, p)
		}
		rel, err := filepath.Rel(root, filepath.Clean(abs))
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return nil, usageError{fmt.Errorf("%s is outside the repository", p)}
		}
		prefixes = append(prefixes, filepath.ToSlash(rel))
	}
	return func(path string) bool {
		for _, p := range prefixes {
			if p == "." || path == p || strings.HasPrefix(path, p+"/") {
				return true
			}
		}
		return false
	}, nil
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}
