package ids

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// newSyntax builds a syntax from (body, checksum) pairs, oldest first.
func newSyntax(t testing.TB, sep string, prefixes []Prefix, shapes ...[2]int) *Syntax {
	t.Helper()
	formats := make([]*Format, len(shapes))
	for i, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		formats[i] = f
	}
	s, err := NewSyntax(sep, prefixes, formats, nil)
	require.NoError(t, err)
	return s
}

var fourPrefixes = []Prefix{{Name: "R", Value: 0}, {Name: "C", Value: 1}, {Name: "P", Value: 2}, {Name: "I", Value: 3}}

// defaultSyntax is four one-letter prefixes and a body of 6 with one check character.
func defaultSyntax(t testing.TB) *Syntax {
	return newSyntax(t, "-", fourPrefixes, [2]int{6, 1})
}

// mint generates an ID; tests that do not care about uniqueness use it as a fresh value.
func mint(t testing.TB, s *Syntax, prefix string) string {
	t.Helper()
	id, err := s.Generate(prefix)
	require.NoError(t, err)
	return id
}

// bodyOf generates an ID and returns only its body.
func bodyOf(t testing.TB, s *Syntax, prefix string) string {
	return s.BodyOf(mint(t, s, prefix))
}

// handWritten is a body of the right shape whose checksum fails: what a person (or an
// agent) produces when typing an ID instead of minting one.
func handWritten(t testing.TB, s *Syntax, prefix string) string {
	t.Helper()
	b := []byte(bodyOf(t, s, prefix))
	last := len(b) - 1
	b[last] = Charset[(int(charValue[b[last]])+1)%Base]
	return string(b)
}

// freshSlot is the placeholder that becomes a new ID at each occurrence: the scope digit,
// then zeros, as wide as the current format.
func freshSlot(s *Syntax, scope Scope) string {
	return string(scope) + strings.Repeat("0", s.Current().Body-1)
}

// exampleSlot is the smallest numbered slot of a scope.
func exampleSlot(s *Syntax, scope Scope) string {
	f := freshSlot(s, scope)
	return f[:len(f)-1] + "1"
}

// parseNotation builds a notation from template strings.
func parseNotation(definitions, references []string) (*Notation, error) {
	parse := func(src []string) ([]Template, error) {
		var out []Template
		for _, s := range src {
			t, err := ParseTemplate(s)
			if err != nil {
				return nil, err
			}
			out = append(out, t)
		}
		return out, nil
	}
	defs, err := parse(definitions)
	if err != nil {
		return nil, err
	}
	refs, err := parse(references)
	if err != nil {
		return nil, err
	}
	return NewNotation(defs, refs)
}
