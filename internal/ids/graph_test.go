package ids

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// files builds the input of BuildGraph from path → content pairs, in the given order.
func files(s *Syntax, pairs ...string) []File {
	var out []File
	for i := 0; i < len(pairs); i += 2 {
		out = append(out, File{Path: pairs[i], Occurrences: s.Scan([]byte(pairs[i+1]))})
	}
	return out
}

func checks(g *Graph) []Check {
	var out []Check
	for _, f := range g.Findings {
		out = append(out, f.Check)
	}
	return out
}

func TestGraphIndex(t *testing.T) {
	s := defaultSyntax(t)
	id := mint(t, s, "R")
	g := BuildGraph(s, files(s,
		"item.md", "# Heading\n\n["+id+"] explained\n",
		"other.md", "see "+id+"\nline 2\nand "+id+" again\n",
	))
	def, ok := g.DefinitionOf(id)
	require.True(t, ok)
	assert.Equal(t, Site{id, "item.md", 3}, def)
	assert.Equal(t, []Site{{id, "other.md", 1}, {id, "other.md", 3}}, g.ReferencesTo(id))
	_, ok = g.DefinitionOf(s.BodyOf(id))
	assert.True(t, ok, "a body alone finds it too")
	_, ok = g.DefinitionOf(mint(t, s, "R"))
	assert.False(t, ok)
	assert.Empty(t, g.Findings)
}

func TestGraphChecks(t *testing.T) {
	s := defaultSyntax(t)
	fresh := freshSlot(s, Human)
	t.Run("one finding per kind", func(t *testing.T) {
		for _, c := range []struct {
			text  string
			check Check
			error bool
		}{
			{mint(t, s, "R") + "\n", StrayRef, true},
			{"[R-" + fresh + "]\n", Unresolved, true},
			{"[R-" + handWritten(t, s, "R") + "]\n", InvalidDef, true},
			{"R-" + handWritten(t, s, "R") + "\n", SuspectRef, false},
			{"[" + mint(t, s, "R") + "]\n", UnreferencedDef, false},
		} {
			g := BuildGraph(s, files(s, "a.md", c.text))
			require.Len(t, g.Findings, 1, c.text)
			assert.Equal(t, c.check, g.Findings[0].Check)
			assert.Equal(t, c.error, g.Findings[0].Check.IsError())
		}
	})
	t.Run("E2 gathers every definition into one finding and excludes W5", func(t *testing.T) {
		id := mint(t, s, "R")
		g := BuildGraph(s, files(s, "a.md", "["+id+"]", "b.md", "["+id+"]", "c.md", "["+id+"]"))
		require.Equal(t, []Check{DuplicateDef}, checks(g))
		f := g.Findings[0]
		assert.Equal(t, "a.md:1", f.Site.String())
		assert.Equal(t, []Site{{id, "b.md", 1}, {id, "c.md", 1}}, f.Related)
	})
	t.Run("E1 gathers every reference into one finding", func(t *testing.T) {
		id := mint(t, s, "R")
		g := BuildGraph(s, files(s, "a.md", id, "b.md", id))
		require.Equal(t, []Check{StrayRef}, checks(g))
		assert.Equal(t, []Site{{id, "b.md", 1}}, g.Findings[0].Related)
	})
	t.Run("a Markdown link is no definition", func(t *testing.T) {
		g := BuildGraph(s, files(s, "a.md", "["+mint(t, s, "R")+"](https://example.test)"))
		assert.Equal(t, []Check{StrayRef}, checks(g))
	})
	t.Run("a definition with a reference is clean", func(t *testing.T) {
		id := mint(t, s, "R")
		assert.Empty(t, BuildGraph(s, files(s, "a.md", "["+id+"]", "b.md", id)).Findings)
	})
}

// TestRetiredIDs: a retired ID keeps its definition, either struck through where it was or
// moved to a list of retired IDs. Either way one bracketed occurrence remains, which keeps
// the references valid and the body taken, with no extra notation.
func TestRetiredIDs(t *testing.T) {
	s := defaultSyntax(t)
	id := mint(t, s, "R")
	ref := "other.md"

	t.Run("deleting the definition strands the references", func(t *testing.T) {
		g := BuildGraph(s, files(s, "item.md", "deleted", ref, "see "+id))
		assert.Equal(t, []Check{StrayRef}, checks(g))
	})
	t.Run("struck through in place", func(t *testing.T) {
		g := BuildGraph(s, files(s, "item.md", "## ~~["+id+"] Topic~~\n\nOut of scope.", ref, "see "+id))
		assert.Empty(t, g.Findings)
	})
	t.Run("moved to a list", func(t *testing.T) {
		g := BuildGraph(s, files(s, "item.md", "deleted", "retired.md", "Retired: ["+id+"]", ref, "formerly "+id))
		assert.Empty(t, g.Findings)
	})
	t.Run("kept in both places is a duplicate", func(t *testing.T) {
		g := BuildGraph(s, files(s, "item.md", "## ["+id+"] Topic", "retired.md", "Retired: ["+id+"]", ref, id))
		assert.Equal(t, []Check{DuplicateDef}, checks(g))
	})
	t.Run("a colon or a parenthesis after the bracket makes a link instead", func(t *testing.T) {
		for _, c := range []struct {
			line string
			def  bool
		}{
			{"Retired: [" + id + "], and others", true},
			{"- [" + id + "] — merged", true},
			{"- [" + id + "]: merged", false},
			{"- [" + id + "](../old.md) old place", false},
		} {
			g := BuildGraph(s, files(s, "retired.md", c.line, ref, id))
			if c.def {
				assert.Empty(t, g.Findings, c.line)
			} else {
				assert.Equal(t, []Check{StrayRef}, checks(g), c.line)
			}
		}
	})
}
