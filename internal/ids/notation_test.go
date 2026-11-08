package ids

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseTemplate(t *testing.T) {
	for s, want := range map[string]Template{
		"[{id}]":     {Before: "[", After: "]"},
		"{id}":       {},
		"^### {id} ": {LineStart: true, Before: "### ", After: " "},
		"[[{id}|":    {Before: "[[", After: "|"},
	} {
		got, err := ParseTemplate(s)
		require.NoError(t, err, s)
		assert.Equal(t, want, got, s)
		assert.Equal(t, s, got.String())
	}
	for _, bad := range []string{"", "[id]", "{id}{id}", "{id}\n", "^\r{id}"} {
		_, err := ParseTemplate(bad)
		assert.Error(t, err, "%q", bad)
	}
}

func TestNewNotationRejects(t *testing.T) {
	for name, c := range map[string][2][]string{
		"no definitions":          {{}, {"{id}"}},
		"no references":           {{"[{id}]"}, {}},
		"a bad template":          {{"[id]"}, {"{id}"}},
		"the same template twice": {{"[{id}]"}, {"[{id}]"}},
		"a tie that can co-fit":   {{"ab{id}"}, {"b{id}c"}},
		"a tie at the line start": {{"^#{id}"}, {"#{id}#"}},
	} {
		_, err := parseNotation(c[0], c[1])
		assert.Error(t, err, name)
	}
	_, err := parseNotation([]string{"({id})"}, []string{"[{id}]"})
	assert.NoError(t, err, "templates of equal length that can never fit one occurrence")
	_, err = parseNotation([]string{"^## {id}"}, []string{"## {id}"})
	assert.NoError(t, err, "the line start makes one ask for more")
}

// wikiSyntax defines IDs in headings and table rows and refers to them with wiki links only,
// so a bare ID is outside the notation.
func wikiSyntax(t *testing.T) *Syntax {
	t.Helper()
	n, err := parseNotation([]string{"^### {id} ", "^| {id} |"}, []string{"[[{id}]]", "[[{id}|"})
	require.NoError(t, err)
	f, err := NewFormat(6, 1)
	require.NoError(t, err)
	s, err := NewSyntax("-", fourPrefixes, []*Format{f}, n)
	require.NoError(t, err)
	return s
}

func TestNotationRoles(t *testing.T) {
	s := wikiSyntax(t)
	body := bodyOf(t, s, "R")
	id := "R-" + body
	for text, want := range map[string]string{
		"### " + id + " Title":              "def",
		"intro\n### " + id + " Title":       "def",
		"| " + id + " | a finding |":        "def",
		"see [[" + id + "]]":                "ref",
		"see [[" + id + "|the policy]]":     "ref",
		"see " + id:                         "unmarked",
		"  ### " + id + " not at the start": "unmarked",
		"[" + id + "]":                      "unmarked",
	} {
		got := kinds(s, text)
		require.Len(t, got, 1, text)
		assert.Equal(t, want, got[0].Kind, text)
	}
}

func TestNotationTokenKeepsTheContext(t *testing.T) {
	s := wikiSyntax(t)
	id := mint(t, s, "R")
	occ := s.Scan([]byte("see [[" + id + "]]\n| " + id + " | a row"))
	require.Len(t, occ, 2)
	assert.Equal(t, "[["+id+"]]", occ[0].Token())
	assert.Equal(t, "| "+id+" |", occ[1].Token())
	assert.Equal(t, "["+id+"]", defaultSyntax(t).Scan([]byte("[" + id + "]"))[0].Token())
}

func TestNotationChecks(t *testing.T) {
	s := wikiSyntax(t)
	id := mint(t, s, "R")
	g := BuildGraph(s, files(s,
		"a.md", "### "+id+" Title\n",
		"b.md", "see [["+id+"]]\nand bare "+id+"\nand "+"R-"+handWritten(t, s, "R")+" bare\n",
	))
	assert.Equal(t, []Check{UnmarkedID, SuspectRef}, checks(g), "a valid ID outside the notation is an error; one failing the checksum may be a word")
	assert.True(t, UnmarkedID.IsError())
	assert.Equal(t, []Site{{id, "b.md", 1}}, g.ReferencesTo(id), "the unmarked one is not counted as a reference")
}

func TestNotationResolve(t *testing.T) {
	s := wikiSyntax(t)
	r := newResolver(s)
	out := resolve(t, r, "### R-000001 Title\nsee [[R-000001]]\nbare R-000001\n[[R-000000]]\n")
	assert.Equal(t, []Reason{ReasonUnmarked, ReasonFreshRef}, reasons(r), "unmarked, then fresh in a reference template")
	ids := realIDs(t, s, out)
	require.Len(t, ids, 2)
	assert.Equal(t, ids[0], ids[1], "the slot's definition and wiki reference agree")

	r = newResolver(s)
	resolve(t, r, "bare "+mint(t, s, "R"))
	assert.Equal(t, []Reason{ReasonUnmarked}, reasons(r), "a real ID outside the notation")

	for text, want := range map[string]bool{
		"bare " + mint(t, s, "R"):          true,
		"[[" + mint(t, s, "R") + "]]":      false,
		"bare R-" + handWritten(t, s, "R"): false,
		"[[R-100001]]":                     true,
	} {
		assert.Equal(t, want, s.Actionable(s.Scan([]byte(text))[0]), text)
	}
}
