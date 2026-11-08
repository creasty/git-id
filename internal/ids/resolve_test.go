package ids

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// realIDs lists the valid IDs in text, in order.
func realIDs(t *testing.T, s *Syntax, text string) []string {
	t.Helper()
	var out []string
	for _, o := range s.Scan([]byte(text)) {
		if !o.IsPlaceholder() {
			require.True(t, s.Valid(o.ID), "minted %s fails the checksum", o.ID)
			out = append(out, o.ID)
		}
	}
	return out
}

func newResolver(s *Syntax, opts ...func(*Resolver)) *Resolver {
	m := &Minter{Syntax: s, Load: func() (map[string]struct{}, error) { return nil, nil }}
	r := &Resolver{Syntax: s, Scope: Human, Mint: m.Mint}
	for _, o := range opts {
		o(r)
	}
	return r
}

func resolve(t *testing.T, r *Resolver, text string) string {
	t.Helper()
	out, err := r.Resolve([]byte(text), "")
	require.NoError(t, err)
	return string(out)
}

func TestResolve(t *testing.T) {
	s := defaultSyntax(t)
	fresh, slot1 := freshSlot(s, Human), exampleSlot(s, Human)
	slot2 := fresh[:len(fresh)-1] + "2"
	agentSlot := exampleSlot(s, Agent)

	t.Run("a fresh placeholder becomes a new ID and keeps its brackets", func(t *testing.T) {
		out := resolve(t, newResolver(s), "## [R-"+fresh+"] Title")
		ids := realIDs(t, s, out)
		require.Len(t, ids, 1)
		assert.Equal(t, "## ["+ids[0]+"] Title", out)
	})
	t.Run("each fresh occurrence is a different ID", func(t *testing.T) {
		ids := realIDs(t, s, resolve(t, newResolver(s), "[R-"+fresh+"] [R-"+fresh+"]"))
		require.Len(t, ids, 2)
		assert.NotEqual(t, ids[0], ids[1])
	})
	t.Run("a slot's definition and references become the same ID", func(t *testing.T) {
		out := resolve(t, newResolver(s), "[R-"+slot1+"] is referred to by R-"+slot1)
		ids := realIDs(t, s, out)
		require.Len(t, ids, 2)
		assert.Equal(t, ids[0], ids[1])
		assert.Equal(t, "["+ids[0]+"] is referred to by "+ids[1], out, "brackets are kept as written")
	})
	t.Run("different slots are different IDs", func(t *testing.T) {
		ids := realIDs(t, s, resolve(t, newResolver(s), "[R-"+slot1+"] [R-"+slot2+"]"))
		assert.NotEqual(t, ids[0], ids[1])
	})
	t.Run("a replacement records both sides", func(t *testing.T) {
		r := newResolver(s)
		out := resolve(t, r, "[R-"+slot1+"]")
		require.Len(t, r.Replaced, 1)
		assert.Equal(t, "[R-"+slot1+"]", r.Replaced[0].Occurrence.Token())
		assert.Equal(t, out, r.Replaced[0].Token())
	})
	t.Run("the other scope is left and reported", func(t *testing.T) {
		r := newResolver(s)
		in := "[R-" + agentSlot + "]"
		assert.Equal(t, in, resolve(t, r, in))
		require.Len(t, r.Rejected, 1)
		assert.Equal(t, ReasonForeign, r.Rejected[0].Reason)
	})
	t.Run("sweep takes the other scope too", func(t *testing.T) {
		r := newResolver(s, func(r *Resolver) { r.Sweep = true })
		assert.Len(t, realIDs(t, s, resolve(t, r, "[R-"+agentSlot+"]")), 1)
		assert.Empty(t, r.Rejected)
	})
	t.Run("the scope decides which ones are taken", func(t *testing.T) {
		r := newResolver(s, func(r *Resolver) { r.Scope = Agent })
		out := resolve(t, r, "[R-"+fresh+"] [R-"+freshSlot(s, Agent)+"]")
		assert.Contains(t, out, "[R-"+fresh+"]")
		assert.Len(t, realIDs(t, s, out), 1)
	})
	t.Run("a fresh reference is left and reported", func(t *testing.T) {
		r := newResolver(s)
		in := "see R-" + fresh
		assert.Equal(t, in, resolve(t, r, in))
		assert.Equal(t, []Reason{ReasonFreshRef}, reasons(r))
	})
	t.Run("a hand-written definition is reported", func(t *testing.T) {
		r := newResolver(s)
		resolve(t, r, "[R-"+handWritten(t, s, "R")+"]")
		assert.Equal(t, []Reason{ReasonInvalidDef}, reasons(r))
	})
	t.Run("a bare token failing the checksum is not", func(t *testing.T) {
		r := newResolver(s)
		resolve(t, r, "R-"+handWritten(t, s, "R")+" may be a word")
		assert.Empty(t, r.Rejected)
	})
	t.Run("real IDs are untouched", func(t *testing.T) {
		r := newResolver(s)
		in := "[" + mint(t, s, "R") + "] and " + mint(t, s, "R")
		assert.Equal(t, in, resolve(t, r, in))
		assert.Empty(t, r.Replaced)
		assert.Empty(t, r.Rejected)
	})
	t.Run("every other byte is kept", func(t *testing.T) {
		in := "\xff[R-" + fresh + "]\r\nno newline at the end"
		out := resolve(t, newResolver(s), in)
		assert.Equal(t, "\xff[", out[:2])
		assert.Equal(t, "]\r\nno newline at the end", out[len(out)-len("]\r\nno newline at the end"):])
	})
	t.Run("the path and line are recorded", func(t *testing.T) {
		r := newResolver(s)
		_, err := r.Resolve([]byte("1\n2\n[R-"+slot1+"]"), "a.md")
		require.NoError(t, err)
		require.Len(t, r.Replaced, 1)
		assert.Equal(t, "a.md", r.Replaced[0].Path)
		assert.Equal(t, 3, r.Replaced[0].Occurrence.Line)
	})
	t.Run("a placeholder of an older width becomes an ID of the current format", func(t *testing.T) {
		s := newSyntax(t, "-", fourPrefixes, [2]int{6, 1}, [2]int{8, 2})
		ids := realIDs(t, s, resolve(t, newResolver(s), "[R-000000]"))
		require.Len(t, ids, 1)
		assert.Len(t, s.BodyOf(ids[0]), 8)
	})
	t.Run("a minting error stops the resolver", func(t *testing.T) {
		r := newResolver(s, func(r *Resolver) {
			r.Mint = func(string) (string, error) { return "", errors.New("boom") }
		})
		_, err := r.Resolve([]byte("[R-"+fresh+"]"), "")
		assert.EqualError(t, err, "boom")
	})
}

func reasons(r *Resolver) []Reason {
	var out []Reason
	for _, rej := range r.Rejected {
		out = append(out, rej.Reason)
	}
	return out
}

// TestResolveIsLazy: hooks run on every edit, and almost none contain a placeholder, so a
// resolver must not mint or read its record unless a placeholder needs it.
func TestResolveIsLazy(t *testing.T) {
	s := defaultSyntax(t)
	fresh, slot := freshSlot(s, Human), exampleSlot(s, Human)
	count := func(text string, opts ...func(*Resolver)) (mints, loads int) {
		r := newResolver(s, opts...)
		inner := r.Mint
		r.Mint = func(p string) (string, error) { mints++; return inner(p) }
		r.LoadSlots = func() (map[string]string, error) { loads++; return nil, nil }
		resolve(t, r, text)
		return
	}
	existing := mint(t, s, "R")
	for name, c := range map[string]struct {
		text         string
		mints, loads int
	}{
		"no ID":                           {"plain text", 0, 0},
		"real IDs only":                   {"[" + existing + "] " + existing, 0, 0},
		"the other scope only":            {"[R-" + exampleSlot(s, Agent) + "]", 0, 0},
		"a fresh reference":               {"R-" + fresh, 0, 0},
		"a hand-written definition":       {"[R-" + handWritten(t, s, "R") + "]", 0, 0},
		"fresh: one mint each, no record": {"[R-" + fresh + "] [R-" + fresh + "]", 2, 0},
		"a slot: one mint, one load":      {"[R-" + slot + "] R-" + slot, 1, 1},
	} {
		t.Run(name, func(t *testing.T) {
			mints, loads := count(c.text)
			assert.Equal(t, [2]int{c.mints, c.loads}, [2]int{mints, loads})
		})
	}
}

func TestResolveContinuesRecordedSlots(t *testing.T) {
	s := defaultSyntax(t)
	slot := "R-" + exampleSlot(s, Agent)
	recorded := mint(t, s, "R")
	r := newResolver(s, func(r *Resolver) {
		r.Scope = Agent
		r.LoadSlots = func() (map[string]string, error) { return map[string]string{slot: recorded}, nil }
	})
	assert.Equal(t, "see "+recorded, resolve(t, r, "see "+slot))
	assert.False(t, r.SlotsChanged, "nothing new was assigned")
}
