package hook

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func patchOf(lines ...string) string {
	return strings.Join(append(append([]string{"*** Begin Patch"}, lines...), "*** End Patch"), "\n") + "\n"
}

func (f *fixture) codexEvent(patch string, session string) map[string]any {
	if session == "" {
		session = "s1"
	}
	return map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "apply_patch",
		"session_id":      session,
		"cwd":             f.Dir,
		"tool_input":      map[string]any{"command": patch},
	}
}

func (f *fixture) codex(e map[string]any) map[string]any {
	f.t.Helper()
	return run(f.t, Codex, e)
}

func (f *fixture) patched(patch, session string) string {
	f.t.Helper()
	h := f.codex(f.codexEvent(patch, session))
	if h == nil {
		return patch
	}
	require.Equal(f.t, "allow", h["permissionDecision"], h["permissionDecisionReason"])
	return h["updatedInput"].(map[string]any)["command"].(string)
}

func TestCodexIgnores(t *testing.T) {
	f := newFixture(t)
	p := patchOf("*** Add File: a.md", "+[R-"+agentFresh+"]")
	e := f.codexEvent(p, "")
	e["hook_event_name"] = "PostToolUse"
	assert.Nil(t, f.codex(e))
	for _, tool := range []string{"Bash", "Write", "Edit"} {
		e := f.codexEvent(p, "")
		e["tool_name"] = tool
		assert.Nil(t, f.codex(e), tool)
	}
	t.Run("patches without placeholders, without running git", func(t *testing.T) {
		existing := f.mint("R")
		noGit(t)
		assert.Nil(t, f.codex(f.codexEvent(patchOf("*** Update File: a.md", "@@", " ["+existing+"] is", "-old", "+see "+existing), "")))
	})
}

func TestCodexResolvesAddedLinesOnly(t *testing.T) {
	f := newFixture(t)
	t.Run("an added line keeps its brackets", func(t *testing.T) {
		out := f.patched(patchOf("*** Add File: a.md", "+## [R-"+agentFresh+"] Title"), "")
		ids := f.ids(out)
		require.Len(t, ids, 1)
		assert.Equal(t, patchOf("*** Add File: a.md", "+## ["+ids[0]+"] Title"), out)
	})
	t.Run("headers, context and removed lines are untouched", func(t *testing.T) {
		// Rewriting the lines a patch matches against would stop it from applying.
		untouched := []string{"*** Update File: R-" + agentFresh + ".md", "@@", " context [R-" + agentFresh + "]", "-removed R-" + agentSlot1}
		out := f.patched(patchOf(append(untouched, "+[R-"+agentSlot1+"] added")...), "untouched")
		ids := f.ids(out)
		require.Len(t, ids, 1)
		assert.Equal(t, patchOf(append(untouched, "+["+ids[0]+"] added")...), out)
	})
	t.Run("a slot agrees across files and lines", func(t *testing.T) {
		out := f.patched(patchOf(
			"*** Add File: a.md", "+[R-"+agentSlot2+"] defined",
			"*** Update File: b.md", "@@", " existing", "+R-"+agentSlot2+" refers",
		), "across")
		ids := f.ids(out)
		require.Len(t, ids, 2)
		assert.Equal(t, ids[0], ids[1])
	})
	t.Run("CRLF is kept", func(t *testing.T) {
		p := strings.ReplaceAll(patchOf("*** Add File: a.md", "+[R-"+agentFresh+"]"), "\n", "\r\n")
		out := f.patched(p, "")
		assert.Equal(t, strings.Count(p, "\r\n"), strings.Count(out, "\r\n"))
	})
	t.Run("files the repository does not scan are untouched", func(t *testing.T) {
		p := patchOf("*** Add File: a.go", "+// [R-"+agentFresh+"]", "*** Add File: b.md", "+[R-"+agentFresh+"]")
		out := f.patched(p, "")
		assert.Contains(t, out, "+// [R-"+agentFresh+"]")
		assert.NotContains(t, out, "+[R-"+agentFresh+"]")
	})
	t.Run("a move takes the destination's rule", func(t *testing.T) {
		p := patchOf("*** Update File: a.go", "*** Move to: a.md", "@@", "+[R-"+agentFresh+"]")
		assert.Len(t, f.ids(f.patched(p, "")), 1)
	})
}

func TestCodexResponses(t *testing.T) {
	f := newFixture(t)
	t.Run("a rewrite comes with allow and the note", func(t *testing.T) {
		h := f.codex(f.codexEvent(patchOf("*** Add File: a.md", "+[R-"+agentFresh+"]"), ""))
		assert.Equal(t, "PreToolUse", h["hookEventName"])
		assert.Equal(t, "allow", h["permissionDecision"], "Codex drops updatedInput without allow")
		ids := f.ids(h["updatedInput"].(map[string]any)["command"].(string))
		assert.Contains(t, h["additionalContext"], "[R-"+agentFresh+"] -> ["+ids[0]+"]")
	})
	t.Run("other tool_input fields are kept", func(t *testing.T) {
		e := f.codexEvent(patchOf("*** Add File: a.md", "+[R-"+agentFresh+"]"), "")
		e["tool_input"].(map[string]any)["other"] = 1
		h := f.codex(e)
		assert.EqualValues(t, 1, h["updatedInput"].(map[string]any)["other"])
	})
	t.Run("denials", func(t *testing.T) {
		for _, line := range []string{"+see R-" + agentFresh, "+[R-" + humanFresh + "] and [R-" + agentFresh + "]"} {
			h := f.codex(f.codexEvent(patchOf("*** Add File: a.md", line), ""))
			require.NotNil(t, h)
			assert.Equal(t, "deny", h["permissionDecision"], line)
			assert.NotContains(t, h, "updatedInput")
		}
	})
	t.Run("shares the session record with the Claude Code hook", func(t *testing.T) {
		first := f.ids(f.rewritten("[R-"+agentSlot1+"]", call{session: "shared"}))
		second := f.ids(f.patched(patchOf("*** Add File: a.md", "+R-"+agentSlot1), "shared"))
		assert.Equal(t, first, second)
	})
}
