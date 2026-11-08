package ids

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func formats(t *testing.T, shapes ...[2]int) []*Format {
	t.Helper()
	out := make([]*Format, len(shapes))
	for i, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		out[i] = f
	}
	return out
}

func TestNewSyntaxRejects(t *testing.T) {
	one := [2]int{6, 1}
	r := []Prefix{{Name: "R"}}
	for name, c := range map[string]struct {
		sep      string
		prefixes []Prefix
		shapes   [][2]int
	}{
		"empty separator":                    {"", r, [][2]int{one}},
		"letter in separator":                {"x", r, [][2]int{one}},
		"underscore separator":               {"_", r, [][2]int{one}},
		"bracket separator":                  {"[", r, [][2]int{one}},
		"colon separator":                    {":", r, [][2]int{one}},
		"space separator":                    {" ", r, [][2]int{one}},
		"separator without prefixes":         {"-", nil, [][2]int{one}},
		"no format":                          {"-", r, nil},
		"two formats of one width":           {"-", r, [][2]int{{6, 1}, {6, 2}}},
		"empty prefix":                       {"-", []Prefix{{Name: ""}}, [][2]int{one}},
		"prefix with underscore":             {"-", []Prefix{{Name: "A_B"}}, [][2]int{one}},
		"prefix ending in separator":         {"-", []Prefix{{Name: "A-"}}, [][2]int{one}},
		"duplicate prefix":                   {"-", []Prefix{{Name: "R"}, {Name: "R", Value: 1}}, [][2]int{one}},
		"duplicate value":                    {"-", []Prefix{{Name: "R"}, {Name: "C"}}, [][2]int{one}},
		"value beyond one digit":             {"-", []Prefix{{Name: "R", Value: 57}}, [][2]int{one}},
		"negative value":                     {"-", []Prefix{{Name: "R", Value: -1}}, [][2]int{one}},
		"value beyond the smallest checksum": {"-", []Prefix{{Name: "R", Value: 100}}, [][2]int{{6, 2}, {8, 1}}},
		"prefix extending another":           {"-", []Prefix{{Name: "A"}, {Name: "A-B", Value: 1}}, [][2]int{one}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := NewSyntax(c.sep, c.prefixes, formats(t, c.shapes...), nil)
			assert.Error(t, err)
		})
	}
}

func TestNewSyntaxAccepts(t *testing.T) {
	t.Run("values up to 57^c-1 with a longer checksum", func(t *testing.T) {
		_, err := NewSyntax("-", []Prefix{{Name: "R", Value: 3248}}, formats(t, [2]int{8, 2}), nil)
		assert.NoError(t, err)
	})
	t.Run("values are unused without a checksum", func(t *testing.T) {
		_, err := NewSyntax("-", []Prefix{{Name: "R"}, {Name: "C"}}, formats(t, [2]int{6, 0}), nil)
		assert.NoError(t, err)
	})
	t.Run("prefixes joined by the separator", func(t *testing.T) {
		_, err := NewSyntax("-", []Prefix{{Name: "SHOP-CART-R"}, {Name: "SHOP-CART-Q", Value: 1}}, formats(t, [2]int{6, 1}), nil)
		assert.NoError(t, err)
	})
	t.Run("a non-ASCII separator", func(t *testing.T) {
		_, err := NewSyntax("§", []Prefix{{Name: "R"}}, formats(t, [2]int{6, 1}), nil)
		assert.NoError(t, err)
	})
}

func TestGenerate(t *testing.T) {
	s := defaultSyntax(t)
	t.Run("in the current format", func(t *testing.T) {
		s := newSyntax(t, "-", fourPrefixes, [2]int{6, 1}, [2]int{8, 2})
		id := mint(t, s, "R")
		assert.Len(t, s.BodyOf(id), 8)
		assert.True(t, s.Valid(id))
	})
	t.Run("unknown prefix names the registered ones", func(t *testing.T) {
		_, err := s.Generate("Z")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "C, I, P, R")
	})
	t.Run("no prefix: the body alone", func(t *testing.T) {
		s := newSyntax(t, "", nil, [2]int{6, 1})
		id := mint(t, s, "")
		assert.Len(t, id, 6)
		assert.True(t, s.Valid(id))
		assert.Equal(t, id, s.BodyOf(id))
		_, err := s.Generate("R")
		assert.Error(t, err)
	})
}

func TestValid(t *testing.T) {
	s := newSyntax(t, "-", fourPrefixes, [2]int{6, 1}, [2]int{8, 2})
	old := newSyntax(t, "-", fourPrefixes, [2]int{6, 1})

	t.Run("every format stays valid", func(t *testing.T) {
		assert.True(t, s.Valid(mint(t, old, "R")), "an ID minted before the wider format was added")
		assert.True(t, s.Valid(mint(t, s, "C")))
	})
	t.Run("rejects", func(t *testing.T) {
		body := bodyOf(t, s, "R")
		for _, bad := range []string{
			"Z-" + body,       // unregistered prefix
			"R" + body,        // no separator
			"R-" + body[:7],   // no format of that width
			"R-" + body + "2", // too long
			"R-" + handWritten(t, s, "R"),
			"C-" + body, // another prefix
			"",
			"-",
		} {
			assert.False(t, s.Valid(bad), bad)
		}
	})
}

func TestSplitTakesTheLongestPrefix(t *testing.T) {
	for _, order := range [][]Prefix{
		{{Name: "R"}, {Name: "REQ", Value: 1}},
		{{Name: "REQ", Value: 1}, {Name: "R"}},
	} {
		s := newSyntax(t, "-", order, [2]int{6, 1})
		body := bodyOf(t, s, "REQ")
		p, b, ok := s.Split("REQ-" + body)
		require.True(t, ok)
		assert.Equal(t, [2]string{"REQ", body}, [2]string{p, b})
	}
}

func TestBodyOf(t *testing.T) {
	s := defaultSyntax(t)
	id := mint(t, s, "R")
	assert.Equal(t, id[2:], s.BodyOf(id))
	assert.Equal(t, id[2:], s.BodyOf(id[2:]), "a body is returned as is")

	hier := newSyntax(t, "-", []Prefix{{Name: "SHOP-CART-R"}}, [2]int{6, 1})
	id = mint(t, hier, "SHOP-CART-R")
	assert.Equal(t, id[len("SHOP-CART-R-"):], hier.BodyOf(id))
}

func TestPlaceholdersNeverLookLikeIDs(t *testing.T) {
	assert.NotContains(t, Charset, "0")
	assert.NotContains(t, Charset, "1")
	s := newSyntax(t, "-", fourPrefixes, [2]int{6, 1}, [2]int{8, 2})
	for _, scope := range []Scope{Human, Agent} {
		assert.Len(t, freshSlot(s, scope), 8, "as wide as the current format")
		assert.Equal(t, byte(scope), freshSlot(s, scope)[0])
		assert.True(t, isFresh(freshSlot(s, scope)))
		assert.False(t, isFresh(exampleSlot(s, scope)))
		assert.False(t, s.Valid("R-"+freshSlot(s, scope)))
	}
	// What keeps the two apart is the first character, not that a placeholder is all digits:
	// a body of 2-9 only is an ordinary body.
	assert.True(t, inCharset([]byte("999999")))
	assert.False(t, isPlaceholder([]byte("999999")))
}
