package hook

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/ids"
	"github.com/creasty/git-id/internal/testrepo"
)

const configText = `
exclude = ["**/*.go"]

[prefixes]
R = { value = 0 }
C = { value = 1 }

[[format]]
body = 6
checksum = 1
`

type fixture struct {
	*testrepo.Repo
	t      *testing.T
	syntax *ids.Syntax
	slots  string
}

func newFixture(t *testing.T) *fixture {
	r := testrepo.New(t)
	r.Write(config.FileName, configText)
	cfg, err := config.Parse([]byte(configText))
	require.NoError(t, err)
	slots := filepath.Join(t.TempDir(), "slots")
	orig := slotDir
	slotDir = func() string { return slots }
	t.Cleanup(func() { slotDir = orig })
	return &fixture{Repo: r, t: t, syntax: cfg.Syntax, slots: slots}
}

var (
	agentFresh = "100000"
	agentSlot1 = "100001"
	agentSlot2 = "100002"
	humanFresh = "000000"
	humanSlot  = "000001"
)

// fields maps a tool to the field it writes.
var fields = map[string]string{"Write": "content", "Edit": "new_string", "NotebookEdit": "new_source"}

type call struct {
	tool, session, event, path string
}

func (f *fixture) event(content string, c call) map[string]any {
	if c.tool == "" {
		c.tool = "Write"
	}
	if c.session == "" {
		c.session = "s1"
	}
	if c.event == "" {
		c.event = "PreToolUse"
	}
	if c.path == "" {
		c.path = filepath.Join(f.Dir, "a.md")
	}
	pathField := "file_path"
	if c.tool == "NotebookEdit" {
		pathField = "notebook_path"
	}
	return map[string]any{
		"hook_event_name": c.event,
		"tool_name":       c.tool,
		"session_id":      c.session,
		"cwd":             f.Dir,
		"tool_input":      map[string]any{pathField: c.path, fields[c.tool]: content},
	}
}

// send runs the Claude Code hook and returns its decoded output, nil when it printed nothing.
func (f *fixture) send(e map[string]any) map[string]any {
	f.t.Helper()
	return run(f.t, Claude, e)
}

func run(t *testing.T, h func(io.Reader, io.Writer) error, e map[string]any) map[string]any {
	t.Helper()
	in, err := json.Marshal(e)
	require.NoError(t, err)
	var out bytes.Buffer
	require.NoError(t, h(bytes.NewReader(in), &out))
	if out.Len() == 0 {
		return nil
	}
	var decoded map[string]any
	require.NoError(t, json.Unmarshal(out.Bytes(), &decoded))
	return decoded["hookSpecificOutput"].(map[string]any)
}

// rewritten returns the content after the hook, failing on a denial.
func (f *fixture) rewritten(content string, c call) string {
	f.t.Helper()
	h := f.send(f.event(content, c))
	if h == nil {
		return content
	}
	require.NotContains(f.t, h, "permissionDecision", h["permissionDecisionReason"])
	tool := c.tool
	if tool == "" {
		tool = "Write"
	}
	return h["updatedInput"].(map[string]any)[fields[tool]].(string)
}

func (f *fixture) denied(content string, c call) string {
	f.t.Helper()
	h := f.send(f.event(content, c))
	require.NotNil(f.t, h)
	require.Equal(f.t, "deny", h["permissionDecision"])
	require.NotContains(f.t, h, "updatedInput", "a denial carries no rewrite")
	return h["permissionDecisionReason"].(string)
}

func (f *fixture) ids(text string) []string {
	f.t.Helper()
	var out []string
	for _, o := range f.syntax.Scan([]byte(text)) {
		if !o.IsPlaceholder() {
			require.True(f.t, f.syntax.Valid(o.ID), "%s fails the checksum", o.ID)
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

// noGit makes any git call fail, to prove a path never needs one.
func noGit(t *testing.T) { t.Setenv("PATH", "") }

func TestClaudeIgnores(t *testing.T) {
	f := newFixture(t)
	for _, ev := range []string{"PostToolUse", "UserPromptSubmit", "SessionStart"} {
		assert.Nil(t, f.send(f.event("[R-"+agentFresh+"]", call{event: ev})), ev)
	}
	e := f.event("[R-"+agentFresh+"]", call{})
	e["tool_name"] = "Bash"
	assert.Nil(t, f.send(e))

	t.Run("edits without placeholders, without running git", func(t *testing.T) {
		noGit(t)
		existing := f.mint("R")
		for _, content := range []string{"[" + existing + "] is", "see " + existing, "no ID", ""} {
			assert.Nil(t, f.send(f.event(content, call{})), content)
		}
	})
	t.Run("repositories without prefixes", func(t *testing.T) {
		other := testrepo.New(t)
		other.Write(config.FileName, "[[format]]\nbody = 6\nchecksum = 1\n")
		noGit(t)
		assert.Nil(t, f.send(f.event("[R-"+agentFresh+"] 100000", call{path: filepath.Join(other.Dir, "a.md")})))
	})
	t.Run("repositories without a configuration", func(t *testing.T) {
		other := testrepo.New(t)
		noGit(t)
		assert.Nil(t, f.send(f.event("[R-"+agentFresh+"]", call{path: filepath.Join(other.Dir, "a.md")})))
		assert.Nil(t, f.send(f.event("[R-"+agentFresh+"]", call{path: filepath.Join(t.TempDir(), "a.md")})), "not in a repository at all")
	})
	t.Run("files the repository does not scan", func(t *testing.T) {
		assert.Nil(t, f.send(f.event("[R-"+agentFresh+"]", call{path: filepath.Join(f.Dir, "a.go")})))
	})
}

func TestClaudeResolves(t *testing.T) {
	f := newFixture(t)
	t.Run("a fresh placeholder keeps its brackets", func(t *testing.T) {
		out := f.rewritten("## [R-"+agentFresh+"] Title", call{})
		ids := f.ids(out)
		require.Len(t, ids, 1)
		assert.Equal(t, "## ["+ids[0]+"] Title", out)
	})
	t.Run("each fresh occurrence differs", func(t *testing.T) {
		ids := f.ids(f.rewritten("[R-"+agentFresh+"] [R-"+agentFresh+"]", call{}))
		assert.NotEqual(t, ids[0], ids[1])
	})
	t.Run("a slot's definition and reference agree", func(t *testing.T) {
		out := f.rewritten("[R-"+agentSlot1+"] is used by R-"+agentSlot1, call{})
		ids := f.ids(out)
		assert.Equal(t, ids[0], ids[1])
		assert.Equal(t, "["+ids[0]+"] is used by "+ids[1], out)
	})
	t.Run("different slots differ", func(t *testing.T) {
		ids := f.ids(f.rewritten("[R-"+agentSlot1+"] [R-"+agentSlot2+"]", call{session: "different-slots"}))
		assert.NotEqual(t, ids[0], ids[1])
	})
	t.Run("the agent is told what each placeholder became", func(t *testing.T) {
		h := f.send(f.event("[R-"+agentFresh+"]", call{}))
		ids := f.ids(h["updatedInput"].(map[string]any)["content"].(string))
		assert.Contains(t, h["additionalContext"], "[R-"+agentFresh+"] -> ["+ids[0]+"]")
	})
	t.Run("every edit tool", func(t *testing.T) {
		for tool := range fields {
			assert.Len(t, f.ids(f.rewritten("[R-"+agentFresh+"]", call{tool: tool})), 1, tool)
		}
	})
	t.Run("MultiEdit rewrites each new_string", func(t *testing.T) {
		e := f.event("", call{tool: "Edit"})
		e["tool_name"] = "MultiEdit"
		e["tool_input"] = map[string]any{
			"file_path": filepath.Join(f.Dir, "a.md"),
			"edits": []any{
				map[string]any{"old_string": "x", "new_string": "[R-" + agentSlot1 + "]"},
				map[string]any{"old_string": "y", "new_string": "R-" + agentSlot1 + " refers"},
			},
		}
		e["session_id"] = "multi"
		h := f.send(e)
		edits := h["updatedInput"].(map[string]any)["edits"].([]any)
		var old, ids []string
		for _, ed := range edits {
			m := ed.(map[string]any)
			old = append(old, m["old_string"].(string))
			ids = append(ids, f.ids(m["new_string"].(string))...)
		}
		assert.Equal(t, []string{"x", "y"}, old, "other fields are kept")
		assert.Equal(t, ids[0], ids[1], "a slot agrees across edits")
	})
	t.Run("minting avoids committed bodies", func(t *testing.T) {
		done := f.mint("R")
		f.Write("existing.md", "["+done+"]\n")
		f.Commit("existing.md")
		for range 20 {
			assert.NotEqual(t, done, f.ids(f.rewritten("[R-"+agentFresh+"]", call{}))[0])
		}
	})
	t.Run("a relative path is resolved against cwd", func(t *testing.T) {
		assert.Len(t, f.ids(f.rewritten("[R-"+agentFresh+"]", call{path: "docs/new/a.md"})), 1)
	})
	t.Run("the file's repository is used, not cwd's", func(t *testing.T) {
		other := testrepo.New(t)
		other.Write(config.FileName, "[prefixes]\nQ = { value = 0 }\n[[format]]\nbody = 4\nchecksum = 1\n")
		h := f.send(f.event("[Q-1000]", call{path: filepath.Join(other.Dir, "x.md")}))
		require.NotNil(t, h)
		out := h["updatedInput"].(map[string]any)["content"].(string)
		assert.Regexp(t, `^\[Q-[2-9A-Za-z]{4}\]$`, out)
	})
}

func TestClaudeDenies(t *testing.T) {
	f := newFixture(t)
	t.Run("a fresh reference", func(t *testing.T) {
		reason := f.denied("see R-"+agentFresh, call{})
		assert.Contains(t, reason, "R-"+agentFresh)
		assert.Contains(t, reason, "reference")
		assert.Contains(t, reason, ids.Guide)
	})
	t.Run("a hand-written definition, without scanning", func(t *testing.T) {
		b := []byte(f.mint("R"))
		b[len(b)-1] = ids.Charset[(bytes.IndexByte([]byte(ids.Charset), b[len(b)-1])+1)%ids.Base]
		calls := watchGit(t)
		assert.Contains(t, f.denied("["+string(b)+"]", call{}), "checksum")
		assert.Equal(t, []string{"check-attr"}, calls(), "only whether the file is binary")
	})
	t.Run("but not a bare token failing the checksum: it may be a word", func(t *testing.T) {
		b := []byte(f.mint("R"))
		b[len(b)-1] = ids.Charset[(bytes.IndexByte([]byte(ids.Charset), b[len(b)-1])+1)%ids.Base]
		assert.Nil(t, f.send(f.event(string(b)+" may be a word", call{})))
	})
	t.Run("a denial rewrites nothing", func(t *testing.T) {
		f.denied("[R-"+agentSlot1+"] and bare R-"+agentFresh, call{})
	})
	t.Run("human placeholders, without scanning", func(t *testing.T) {
		calls := watchGit(t)
		assert.Contains(t, f.denied("[R-"+humanFresh+"]", call{}), "other scope")
		assert.Contains(t, f.denied("[R-"+humanSlot+"] and R-"+humanSlot, call{}), "R-"+humanSlot)
		assert.Equal(t, []string{"check-attr", "check-attr"}, calls(), "only whether the file is binary")
	})
}

func TestClaudeSession(t *testing.T) {
	f := newFixture(t)
	t.Run("a slot keeps its ID across calls of one session", func(t *testing.T) {
		first := f.ids(f.rewritten("[R-"+agentSlot1+"]", call{session: "same"}))
		next := f.ids(f.rewritten("R-"+agentSlot1, call{session: "same"}))
		assert.Equal(t, first, next)
	})
	t.Run("another session gets another ID", func(t *testing.T) {
		a := f.ids(f.rewritten("[R-"+agentSlot1+"]", call{session: "a"}))
		b := f.ids(f.rewritten("[R-"+agentSlot1+"]", call{session: "b"}))
		assert.NotEqual(t, a, b)
	})
	t.Run("the record is per session", func(t *testing.T) {
		id := f.ids(f.rewritten("[R-"+agentSlot1+"]", call{session: "recorded"}))[0]
		var record map[string]string
		data, err := os.ReadFile(slotPath(f.Dir, "recorded"))
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(data, &record))
		assert.Equal(t, map[string]string{"R-" + agentSlot1: id}, record)
	})
	t.Run("recorded IDs are not minted again", func(t *testing.T) {
		// A recorded ID may not be in any file yet, so only the record keeps it taken.
		recorded := f.mint("R")
		path := slotPath(f.Dir, "taken")
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(`{"R-`+agentSlot2+`": "`+recorded+`"}`), 0o600))
		r, err := findRepo(filepath.Join(f.Dir, "a.md"))
		require.NoError(t, err)
		_, s := r.resolver("taken")
		_, err = s.mint("R")
		require.NoError(t, err)
		used, err := s.minter.Used(recorded)
		require.NoError(t, err)
		assert.True(t, used)
		require.NoError(t, s.save(false))
	})
	t.Run("the record is written only when a slot is assigned", func(t *testing.T) {
		g := newFixture(t)
		g.rewritten("no ID", call{})
		g.rewritten("[R-"+agentFresh+"]", call{})
		_, err := os.Stat(g.slots)
		assert.True(t, os.IsNotExist(err), "fresh placeholders need no record")
		g.rewritten("[R-"+agentSlot1+"]", call{})
		_, err = os.Stat(slotPath(g.Dir, "s1"))
		assert.NoError(t, err)
	})
	t.Run("unsafe session IDs fall back to a fixed name", func(t *testing.T) {
		for _, id := range []string{"", ".", "..", "../x", `a\b`} {
			assert.Equal(t, "default.json", filepath.Base(slotPath(f.Dir, id)), id)
		}
	})
}

// watchGit puts a git on PATH that records the subcommand of each call before running the
// real one, and returns what was recorded so far.
func watchGit(t *testing.T) func() []string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	real, err := exec.LookPath("git")
	require.NoError(t, err)
	dir := t.TempDir()
	log := filepath.Join(dir, "calls")
	script := "#!/bin/sh\n" +
		"[ \"$1\" = -C ] && sub=$3 || sub=$1\n" +
		"echo \"$sub\" >> '" + log + "'\n" +
		"exec '" + real + "' \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "git"), []byte(script), 0o755))
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return func() []string {
		data, _ := os.ReadFile(log)
		return strings.Fields(string(data))
	}
}

func TestBinaryFilesAreLeftAlone(t *testing.T) {
	f := newFixture(t)
	f.Write(".gitattributes", "*.dat binary\n")
	assert.Nil(t, f.send(f.event("[R-"+agentFresh+"]", call{path: filepath.Join(f.Dir, "a.dat")})), "binary by attribute")
	assert.Nil(t, f.send(f.event("see R-"+agentFresh, call{path: filepath.Join(f.Dir, "a.dat")})), "not denied either")
	assert.Len(t, f.ids(f.rewritten("[R-"+agentFresh+"]", call{})), 1, "text files still resolve")

	p := patchOf("*** Add File: a.dat", "+[R-"+agentFresh+"]", "*** Add File: b.md", "+[R-"+agentFresh+"]")
	out := f.patched(p, "")
	assert.Contains(t, out, "+[R-"+agentFresh+"]\n*** Add File: b.md", "the binary file's lines are untouched")
	assert.NotContains(t, out, "*** Add File: b.md\n+[R-"+agentFresh+"]")
}

func TestHookFollowsTheNotation(t *testing.T) {
	f := newFixture(t)
	f.Write(config.FileName, configText+"\n[notation]\ndefinitions = [\"^### {id} \"]\nreferences = [\"[[{id}]]\"]\n")
	reason := f.denied("see R-"+agentSlot1, call{})
	assert.Contains(t, reason, "R-"+agentSlot1)
	assert.Contains(t, reason, "outside the notation")

	out := f.rewritten("### R-"+agentSlot2+" Title\nsee [[R-"+agentSlot2+"]]\n", call{session: "notation"})
	ids := f.ids(out)
	require.Len(t, ids, 2)
	assert.Equal(t, ids[0], ids[1])
	assert.Equal(t, "### "+ids[0]+" Title\nsee [["+ids[1]+"]]\n", out)
}
