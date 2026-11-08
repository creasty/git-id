package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/ids"
	"github.com/creasty/git-id/internal/testrepo"
)

const configText = `
exclude = ["vendor/**"]

[prefixes]
R = { value = 0, description = "requirement" }
C = { value = 1, description = "contract" }

[[format]]
body = 6
checksum = 1
`

const (
	fresh     = "000000" // human, fresh
	slot1     = "000001"
	agentSlot = "100001"
	agentNew  = "100000"
)

type fixture struct {
	*testrepo.Repo
	t      *testing.T
	syntax *ids.Syntax
}

func newFixture(t *testing.T) *fixture {
	r := testrepo.New(t)
	r.Write(config.FileName, configText)
	cfg, err := config.Parse([]byte(configText))
	require.NoError(t, err)
	return &fixture{Repo: r, t: t, syntax: cfg.Syntax}
}

// run executes git id in the repository root and returns (exit code, stdout, stderr).
func (f *fixture) run(args ...string) (int, string, string) {
	return f.runIn(f.Dir, args...)
}

func (f *fixture) runIn(dir string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := Run(args, dir, strings.NewReader(""), &out, &errOut)
	return code, out.String(), errOut.String()
}

func (f *fixture) ids(path string) []string {
	f.t.Helper()
	var out []string
	for _, o := range f.syntax.Scan([]byte(f.Read(path))) {
		if !o.IsPlaceholder() {
			out = append(out, o.ID)
		}
	}
	return out
}

func (f *fixture) mint(prefix string) string {
	id, err := f.syntax.Generate(prefix)
	require.NoError(f.t, err)
	return id
}

func TestResolve(t *testing.T) {
	t.Run("the whole working tree", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		code, _, _ := f.run("resolve")
		assert.Equal(t, 0, code)
		assert.Len(t, f.ids("a.md"), 1)
	})
	t.Run("only the given paths", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		f.Write("docs/b.md", "[R-"+fresh+"]\n")
		f.Write("docs/c.md", "[R-"+fresh+"]\n")
		f.run("resolve", "a.md")
		assert.Len(t, f.ids("a.md"), 1)
		assert.Contains(t, f.Read("docs/b.md"), fresh, "a file that was not given is untouched")
		f.runIn(filepath.Join(f.Dir, "docs"), "resolve", ".")
		assert.Len(t, f.ids("docs/b.md"), 1, "paths are relative to the working directory")
	})
	t.Run("a slot is one ID across files", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+slot1+"]\n")
		f.Write("b.md", "see R-"+slot1+"\n")
		f.run("resolve")
		assert.Equal(t, f.ids("a.md"), f.ids("b.md"))
	})
	t.Run("assignments last for one run", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+slot1+"]\n")
		f.run("resolve")
		f.Write("b.md", "[R-"+slot1+"]\n")
		f.run("resolve")
		assert.NotEqual(t, f.ids("a.md"), f.ids("b.md"))
	})
	t.Run("minting avoids committed bodies", func(t *testing.T) {
		f := newFixture(t)
		done := f.mint("R")
		f.Write("done.md", "["+done+"]\n")
		f.Commit("done.md")
		for i := range 20 {
			name := filepath.Join("new", string(rune('a'+i))+".md")
			f.Write(name, "[R-"+fresh+"]\n")
			f.run("resolve")
			assert.NotContains(t, f.ids(name), done)
		}
	})
	t.Run("replacements come first, then what was left", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"] and [R-"+agentNew+"]\n")
		f.Write("b.md", "[R-"+fresh+"]\n")
		code, out, errOut := f.run("resolve")
		assert.Equal(t, 1, code)
		var heads [][2]string // place, then what happened
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			place, rest, _ := strings.Cut(line, ": ")
			heads = append(heads, [2]string{place, strings.Fields(rest)[0]})
		}
		assert.Equal(t, [][2]string{{"a.md:1", "replaced"}, {"b.md:1", "replaced"}, {"a.md:1", "left"}}, heads)
		assert.Contains(t, errOut, "replaced 2 placeholders in 2 files, left 1")
		assert.Contains(t, errOut, ids.Guide)
	})
	t.Run("dry run prints but does not write", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		code, out, errOut := f.run("resolve", "--dry-run")
		assert.Equal(t, 0, code)
		assert.Contains(t, out, fresh)
		assert.Contains(t, f.Read("a.md"), fresh)
		assert.Contains(t, errOut, "dry run")
	})
	t.Run("agent placeholders are left unless --all", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+agentSlot+"] and [R-"+fresh+"]\n")
		code, out, _ := f.run("resolve")
		assert.Equal(t, 1, code)
		assert.Contains(t, f.Read("a.md"), agentSlot)
		assert.Contains(t, out, "other scope")
		code, _, _ = f.run("resolve", "--all")
		assert.Equal(t, 0, code)
		assert.Len(t, f.ids("a.md"), 2)
	})
	t.Run("what can be resolved is written despite what cannot", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		f.Write("b.md", "a fresh reference R-"+fresh+"\n")
		code, _, _ := f.run("resolve")
		assert.Equal(t, 1, code)
		assert.Len(t, f.ids("a.md"), 1)
	})
	t.Run("untracked files are resolved, ignored ones are not", func(t *testing.T) {
		f := newFixture(t)
		f.Write(".gitignore", "ignored/\n")
		f.Write("ignored/a.md", "[R-"+fresh+"]\n")
		f.Write("b.md", "[R-"+fresh+"]\n")
		code, _, _ := f.run("resolve")
		assert.Equal(t, 0, code)
		assert.Contains(t, f.Read("ignored/a.md"), fresh)
		assert.Len(t, f.ids("b.md"), 1)
	})
	t.Run("excluded and binary files are not touched, even when named", func(t *testing.T) {
		f := newFixture(t)
		f.Write("vendor/a.md", "[R-"+fresh+"]\n")
		f.WriteBytes("b.dat", []byte("\x00[R-"+fresh+"]\n"))
		code, out, _ := f.run("resolve", "vendor/a.md", "b.dat")
		assert.Equal(t, 0, code)
		assert.Contains(t, f.Read("vendor/a.md"), fresh)
		assert.Contains(t, f.Read("b.dat"), fresh)
		assert.Empty(t, out)
	})
	t.Run("every other byte is kept, invalid UTF-8 included", func(t *testing.T) {
		// Reading bytes means no decoding step can corrupt the rest of the file, so files
		// with stray bytes are resolved instead of skipped.
		f := newFixture(t)
		f.WriteBytes("a.md", []byte("\xff[R-"+fresh+"]\r\nno newline at the end"))
		f.run("resolve")
		out := f.Read("a.md")
		assert.True(t, strings.HasPrefix(out, "\xff["), "the stray byte is kept")
		assert.True(t, strings.HasSuffix(out, "]\r\nno newline at the end"), "CRLF and the missing final newline are kept")
		assert.Len(t, f.ids("a.md"), 1)
	})
	t.Run("the file mode is kept", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		require.NoError(t, os.Chmod(filepath.Join(f.Dir, "a.md"), 0o600))
		f.run("resolve")
		info, err := os.Stat(filepath.Join(f.Dir, "a.md"))
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	})
	t.Run("the result passes integrity", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+slot1+"]\n")
		f.Write("b.md", "see R-"+slot1+"\n")
		f.run("resolve")
		code, _, errOut := f.run("integrity")
		assert.Equal(t, 0, code)
		assert.Contains(t, errOut, "0 errors")
	})
	t.Run("nothing to resolve mints nothing", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "["+f.mint("R")+"]\n")
		code, out, errOut := f.run("resolve")
		assert.Equal(t, 0, code)
		assert.Empty(t, out)
		assert.Contains(t, errOut, "replaced 0 placeholders in 0 files, left 0")
	})
}

func TestNew(t *testing.T) {
	f := newFixture(t)
	code, out, _ := f.run("new", "R", "-n", "3")
	assert.Equal(t, 0, code)
	lines := strings.Fields(out)
	require.Len(t, lines, 3)
	for _, id := range lines {
		assert.True(t, f.syntax.Valid(id), id)
		assert.True(t, strings.HasPrefix(id, "R-"))
	}

	t.Run("a prefix is required and listed", func(t *testing.T) {
		code, _, errOut := f.run("new")
		assert.Equal(t, 2, code)
		assert.Contains(t, errOut, "C (contract), R (requirement)")
	})
	t.Run("an unknown prefix", func(t *testing.T) {
		code, _, errOut := f.run("new", "Z")
		assert.Equal(t, 1, code)
		assert.Contains(t, errOut, `unknown prefix "Z"`)
	})
	t.Run("without a configuration", func(t *testing.T) {
		g := testrepo.New(t)
		code, _, errOut := f.runIn(g.Dir, "new", "R")
		assert.Equal(t, 1, code)
		assert.Contains(t, errOut, config.FileName+" not found")
	})
	t.Run("without prefixes", func(t *testing.T) {
		g := testrepo.New(t)
		g.Write(config.FileName, "[[format]]\nbody = 2\nchecksum = 1\n")
		// Every valid body but one is written somewhere, as a word: minting must find the last.
		cfg, err := config.Parse([]byte(g.Read(config.FileName)))
		require.NoError(t, err)
		var words []string
		for _, c := range ids.Charset {
			for _, d := range ids.Charset {
				if b := string(c) + string(d); cfg.Syntax.Valid(b) {
					words = append(words, b)
				}
			}
		}
		require.Len(t, words, ids.Base)
		g.Write("taken.md", strings.Join(words[1:], " "))
		code, out, errOut := f.runIn(g.Dir, "new")
		assert.Equal(t, 0, code, errOut)
		assert.Equal(t, words[0]+"\n", out, "the body alone, unique among every word of the repository")

		for _, cmd := range []string{"integrity", "check", "resolve"} {
			args := []string{cmd}
			if cmd == "check" {
				args = append(args, words[0])
			}
			code, _, errOut := f.runIn(g.Dir, args...)
			assert.Equal(t, 1, code, cmd)
			assert.Contains(t, errOut, "needs [prefixes]", cmd)
		}
	})
}

func TestHarvest(t *testing.T) {
	f := newFixture(t)
	f.Write("a.md", "["+f.mint("R")+"]\n")
	f.Write("b.md", "["+f.mint("C")+"]\n")
	code, out, _ := f.run("harvest")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "taken\t2\n")
	assert.Contains(t, out, filepath.Join(".git", "git-id", "head.json"))

	t.Run("counts bodies the working tree no longer has", func(t *testing.T) {
		g := newFixture(t)
		g.Write("r.md", "["+g.mint("C")+"]\n")
		g.Add("r.md")
		g.Remove("r.md")
		_, out, _ := g.run("harvest")
		assert.Contains(t, out, "taken\t1\n")
	})
	t.Run("lists no IDs", func(t *testing.T) {
		g := newFixture(t)
		id := g.mint("R")
		g.Write("a.md", "["+id+"]\n")
		_, out, _ := g.run("harvest")
		assert.NotContains(t, out, g.syntax.BodyOf(id), "listing is check's job")
	})
}

func TestCheck(t *testing.T) {
	f := newFixture(t)
	def := f.mint("R")
	f.Write("item.md", "["+def+"]\n")
	f.Write("other.md", "see "+def+"\n")

	t.Run("definition and references", func(t *testing.T) {
		_, out, _ := f.run("check", def)
		assert.Equal(t, def+": defined, 1 reference\n  item.md:1: definition\n  other.md:1: reference\n", out)
	})
	t.Run("unused", func(t *testing.T) {
		unused := f.mint("R")
		_, out, _ := f.run("check", unused)
		assert.Equal(t, unused+": unused\n", out)
	})
	t.Run("a stray reference", func(t *testing.T) {
		stray := f.mint("R")
		f.Write("stray.md", stray+"\n")
		_, out, _ := f.run("check", stray)
		assert.Equal(t, stray+": no definition, 1 reference\n  stray.md:1: reference\n", out)
	})
	t.Run("reserved: only in the index", func(t *testing.T) {
		reserved := f.mint("C")
		f.Write("reserved.md", "["+reserved+"]\n")
		f.Add("reserved.md")
		f.Remove("reserved.md")
		_, out, _ := f.run("check", reserved)
		assert.Equal(t, reserved+": reserved\n", out, "minting must avoid it, so it is not unused")

		_, out, _ = f.run("check", reserved, "--json")
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		assert.Equal(t, map[string]any{"reserved": true}, got[reserved])
	})
	t.Run("JSON keeps the order of the arguments", func(t *testing.T) {
		unused := f.mint("R")
		code, out, _ := f.run("check", unused, def, "--json")
		assert.Equal(t, 0, code)
		assert.Less(t, strings.Index(out, unused), strings.Index(out, def))
		var got map[string]any
		require.NoError(t, json.Unmarshal([]byte(out), &got))
		assert.Nil(t, got[unused], "unused has nothing to say")
		assert.Equal(t, map[string]any{
			"definition": map[string]any{"path": "item.md", "line": float64(1)},
			"references": []any{map[string]any{"path": "other.md", "line": float64(1)}},
		}, got[def])
	})
	t.Run("a query always exits 0", func(t *testing.T) {
		for _, id := range []string{def, f.mint("R")} {
			code, _, _ := f.run("check", id)
			assert.Equal(t, 0, code)
		}
	})
}

func TestIntegrity(t *testing.T) {
	t.Run("an error exits 1", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "only a reference "+f.mint("R")+"\n")
		code, out, errOut := f.run("integrity")
		assert.Equal(t, 1, code)
		assert.Contains(t, out, "a.md:1: E1 ")
		assert.Equal(t, "1 error, 0 warnings\n", errOut)
	})
	t.Run("warnings exit 0", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "["+f.mint("R")+"]\n")
		code, out, errOut := f.run("integrity")
		assert.Equal(t, 0, code)
		assert.Contains(t, out, "a.md:1: W5 ")
		assert.Equal(t, "0 errors, 1 warning\n", errOut)
	})
	t.Run("every place of a finding", func(t *testing.T) {
		f := newFixture(t)
		id := f.mint("R")
		f.Write("a.md", "["+id+"]\n")
		f.Write("b.md", "["+id+"]\n")
		_, out, _ := f.run("integrity")
		assert.Equal(t, "a.md:1: E2 "+id+" duplicate definition\n  b.md:1: also defined here\n", out)
	})
	t.Run("errors first, then warnings, each in path order", func(t *testing.T) {
		f := newFixture(t)
		stray, lone := f.mint("R"), f.mint("R")
		f.Write("a.md", "["+lone+"]\n")
		f.Write("b.md", "see "+stray+"\n")
		f.Write("c.md", "\n[R-"+fresh+"]\nand "+stray+"\n")
		_, out, errOut := f.run("integrity")
		assert.Equal(t, "b.md:1: E1 "+stray+" stray reference\n"+
			"  c.md:3: also referenced here\n"+
			"c.md:2: E3 R-"+fresh+" unresolved placeholder\n"+
			"a.md:1: W5 "+lone+" unreferenced definition\n", out)
		assert.Equal(t, "2 errors, 1 warning\n", errOut)
	})
	t.Run("a placeholder left in a file", func(t *testing.T) {
		f := newFixture(t)
		f.Write("a.md", "[R-"+fresh+"]\n")
		code, out, _ := f.run("integrity")
		assert.Equal(t, 1, code)
		assert.Contains(t, out, "E3")
	})
	t.Run("from a subdirectory", func(t *testing.T) {
		f := newFixture(t)
		f.Write("docs/a.md", "only a reference "+f.mint("R")+"\n")
		code, out, _ := f.runIn(filepath.Join(f.Dir, "docs"), "integrity")
		assert.Equal(t, 1, code)
		assert.Contains(t, out, "docs/a.md:1", "paths are relative to the repository root")
	})
}

func TestUsage(t *testing.T) {
	f := newFixture(t)
	code, _, errOut := f.run()
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, "Usage:")
	code, _, _ = f.run("nope")
	assert.Equal(t, 2, code)
	code, out, _ := f.run("help")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "git id new")
	code, _, _ = f.run("check")
	assert.Equal(t, 2, code)
	code, _, _ = f.run("new", "-h")
	assert.Equal(t, 0, code)
	code, _, _ = f.run("hook", "other")
	assert.Equal(t, 2, code)
	code, out, _ = f.run("version")
	assert.Equal(t, 0, code)
	assert.Contains(t, out, "git-id")
}

func TestHookCommand(t *testing.T) {
	f := newFixture(t)
	event := `{"hook_event_name":"PreToolUse","tool_name":"Write","session_id":"s","cwd":"` + f.Dir +
		`","tool_input":{"file_path":"` + filepath.Join(f.Dir, "a.md") + `","content":"[R-` + agentNew + `]"}}`
	var out, errOut bytes.Buffer
	code := Run([]string{"hook", "run", "claude"}, f.Dir, strings.NewReader(event), &out, &errOut)
	assert.Equal(t, 0, code, errOut.String())
	assert.Contains(t, out.String(), "updatedInput")
}

func TestHookInstall(t *testing.T) {
	f := newFixture(t)
	sub := filepath.Join(f.Dir, "docs")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	code, out, errOut := f.runIn(sub, "hook", "install", "claude")
	assert.Equal(t, 0, code, errOut)
	assert.Equal(t, ".claude/settings.json: added\n", out, "written at the root, whatever the directory")
	assert.Contains(t, f.Read(".claude/settings.json"), `"command": "git id hook run claude"`)

	code, out, _ = f.run("hook", "install", "codex")
	assert.Equal(t, 0, code)
	assert.Equal(t, ".codex/hooks.json: added\n", out)
	assert.Contains(t, f.Read(".codex/hooks.json"), `"command": "git id hook run codex"`)

	code, out, _ = f.run("hook", "install", "codex")
	assert.Equal(t, 0, code)
	assert.Equal(t, ".codex/hooks.json: already there\n", out)

	code, _, errOut = f.run("hook", "install", "vim")
	assert.Equal(t, 2, code)
	assert.Contains(t, errOut, `unknown agent "vim"`)
	for _, args := range [][]string{
		{"hook", "install"},                    // the agent is required
		{"hook", "install", "claude", "codex"}, // one at a time
		{"hook", "run"},
		{"hook", "claude"}, // the action is required
		{"hook", "start", "claude"},
	} {
		code, _, _ := f.run(args...)
		assert.Equal(t, 2, code, args)
	}
}

func TestIntegrityFollowsTheNotation(t *testing.T) {
	f := newFixture(t)
	f.Write(config.FileName, configText+"\n[notation]\ndefinitions = [\"^### {id} \"]\nreferences = [\"[[{id}]]\"]\n")
	id := f.mint("R")
	f.Write("a.md", "### "+id+" Title\n\nsee [["+id+"]] and bare "+id+"\n")
	code, out, errOut := f.run("integrity")
	assert.Equal(t, 1, code)
	assert.Equal(t, "a.md:3: E6 "+id+" unmarked ID\n", out)
	assert.Equal(t, "1 error, 0 warnings\n", errOut)
}

func TestSkillInstructions(t *testing.T) {
	run := func(dir string) string {
		var out, errOut bytes.Buffer
		code := Run([]string{"--skill-instructions"}, dir, strings.NewReader(""), &out, &errOut)
		require.Equal(t, 0, code, "it never fails: the skill embeds whatever it prints")
		require.Empty(t, errOut.String())
		return out.String()
	}

	t.Run("the default notation", func(t *testing.T) {
		f := newFixture(t)
		out := run(f.Dir)
		for _, want := range []string{
			"An ID is a prefix, `-`, then 6 characters, the last one a check character.",
			"- `C`: contract\n- `R`: requirement\n",
			"- `[{id}]`: the ID in brackets, where the item itself is written\n",
			"- `{id}`: the bare ID, anywhere else\n",
			"- `[C-100000]`: a new definition",
			"- `[C-100001]` with `C-100001`: a slot",
			"IDs count only in files matching `**`, except `vendor/**`.",
		} {
			assert.Contains(t, out, want)
		}
		assert.NotContains(t, out, "E6", "a bare reference form exists, so nothing is unmarked")
		assert.NotContains(t, out, "`^`")
	})
	t.Run("a notation of its own, with intent", func(t *testing.T) {
		f := newFixture(t)
		f.Write(config.FileName, configText+`
[notation]
definitions = [{ template = "^### {id} ", description = "a rule: a heading, then the rule in one sentence" }]
references = ["[[{id}]]"]

[[format]]
body = 8
checksum = 2
`)
		out := run(f.Dir)
		for _, want := range []string{
			"then 8 characters, the last 2 check characters. Older IDs can also have 6 characters.",
			"- `^### {id} `: a rule: a heading, then the rule in one sentence\n",
			"- `[[{id}]]`\n",
			"a leading `^` for the start of a line",
			"an ID that fits none is an error (E6)",
			"- `### C-10000000 ` at the start of a line: a new definition",
			"with `[[C-10000001]]`: a slot",
		} {
			assert.Contains(t, out, want)
		}
	})
	t.Run("without prefixes", func(t *testing.T) {
		g := testrepo.New(t)
		g.Write(config.FileName, "[[format]]\nbody = 6\nchecksum = 0\n")
		assert.Equal(t, "IDs here have no prefix: an ID is 6 characters, and git id only mints them unique. Get each one from `git id new`; there is no notation to follow and nothing to check.\n", run(g.Dir))
	})
	t.Run("without a configuration", func(t *testing.T) {
		assert.Contains(t, run(testrepo.New(t).Dir), "not set up in this repository")
		assert.Contains(t, run(t.TempDir()), "not a git repository")
	})
	t.Run("with a broken configuration", func(t *testing.T) {
		g := testrepo.New(t)
		g.Write(config.FileName, "[[format]]\n")
		assert.Contains(t, run(g.Dir), "cannot read this repository's configuration")
	})
}
