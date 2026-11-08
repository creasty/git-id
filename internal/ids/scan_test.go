package ids

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type kind struct {
	Kind  string // def, ref, placeholder
	Value string // body or slot
}

func kinds(s *Syntax, text string) []kind {
	var out []kind
	for _, o := range s.Scan([]byte(text)) {
		switch {
		case o.IsPlaceholder():
			out = append(out, kind{"placeholder", o.Slot})
		case o.Role == Definition:
			out = append(out, kind{"def", o.Body})
		case o.Role == Unmarked:
			out = append(out, kind{"unmarked", o.Body})
		default:
			out = append(out, kind{"ref", o.Body})
		}
	}
	return out
}

func TestScanBoundaries(t *testing.T) {
	s := defaultSyntax(t)
	body := bodyOf(t, s, "R")
	fresh := freshSlot(s, Human)

	t.Run("not part of a longer ASCII word", func(t *testing.T) {
		// The rule is the same on both sides. Bounding the end by Charset alone would miss 0,
		// 1 and _, which are not in it, and read "R-kNzgST0" as an ID.
		for _, text := range []string{
			"YR-" + body, "1R-" + body, "foo_R-" + body,
			"R-" + body + "2", "R-" + body + "0", "R-" + body + "_",
			"R-" + fresh + "0", "R-" + fresh + "x",
		} {
			assert.Empty(t, kinds(s, text), text)
		}
	})
	t.Run("glued to CJK prose", func(t *testing.T) {
		// \b would miss these: CJK characters count as word characters, and CJK prose puts
		// no spaces between words.
		for _, text := range []string{"仕様R-" + body, "本文のR-" + body + "です", "詳しくはR-" + body + "を参照"} {
			assert.Equal(t, []kind{{"ref", body}}, kinds(s, text), text)
		}
	})
	t.Run("between punctuation", func(t *testing.T) {
		for _, text := range []string{
			"（R-" + body + "）", "「R-" + body + "」", "R-" + body + "。", "see R-" + body + ".",
			"(R-" + body + ")", "|R-" + body + "|", "R-" + body + "-continued", "-R-" + body,
		} {
			assert.Equal(t, []kind{{"ref", body}}, kinds(s, text), text)
		}
	})
	t.Run("placeholders too", func(t *testing.T) {
		for _, text := range []string{"（R-" + fresh + "）", "[R-" + fresh + "] is"} {
			assert.Equal(t, []kind{{"placeholder", fresh}}, kinds(s, text), text)
		}
	})
	t.Run("invalid UTF-8 around an ID", func(t *testing.T) {
		text := "\xff\xfe[R-" + body + "]\x00"
		assert.Equal(t, []kind{{"def", body}}, kinds(s, text))
	})
}

func TestScanKinds(t *testing.T) {
	s := defaultSyntax(t)
	body := bodyOf(t, s, "R")
	fresh := freshSlot(s, Human)

	assert.Equal(t, []kind{{"def", body}}, kinds(s, "[R-"+body+"] is"), "brackets define")
	assert.Equal(t, []kind{{"ref", body}}, kinds(s, "see R-"+body), "bare refers")
	for _, link := range []string{
		"[R-" + body + "](https://example.test)",
		"[R-" + body + "]: https://example.test",
		"[[R-" + body + "]]",
		"[[R-" + body + "|alias]]",
	} {
		assert.Equal(t, []kind{{"ref", body}}, kinds(s, link), "a link is a reference: %s", link)
	}
	assert.Equal(t, []kind{{"def", body}}, kinds(s, "[R-"+body+"] — retired"), "a definition may be followed by text")
	assert.Equal(t, []kind{{"placeholder", fresh}}, kinds(s, "[R-"+fresh+"]"), "a placeholder is neither")
	assert.Equal(t, []kind{{"placeholder", fresh}}, kinds(s, "R-"+fresh))
}

func TestScanPositions(t *testing.T) {
	s := defaultSyntax(t)
	id := mint(t, s, "R")
	text := "line 1\nline 2\nthen [" + id + "] and " + id + "\n"
	occ := s.Scan([]byte(text))
	require.Len(t, occ, 2)
	for _, o := range occ {
		assert.Equal(t, 3, o.Line)
		assert.Equal(t, id, text[o.Start:o.End], "the span is the ID without brackets")
	}
	assert.Equal(t, "["+id+"]", occ[0].Token())
	assert.Equal(t, id, occ[1].Token())
}

func TestScanFormats(t *testing.T) {
	s := newSyntax(t, "-", fourPrefixes, [2]int{6, 1}, [2]int{8, 2})
	six := bodyOf(t, newSyntax(t, "-", fourPrefixes, [2]int{6, 1}), "R")
	eight := bodyOf(t, s, "R")
	assert.Equal(t, []kind{{"ref", six}, {"ref", eight}}, kinds(s, "R-"+six+" R-"+eight))
	assert.Empty(t, kinds(s, "R-"+eight[:7]), "no format is 7 wide")
	assert.Equal(t, []kind{{"placeholder", "000000"}, {"placeholder", "10000001"}}, kinds(s, "[R-000000] R-10000001"),
		"placeholders of every width are found, so none is left behind silently")
}

func TestScanPrefixes(t *testing.T) {
	t.Run("joined by the separator", func(t *testing.T) {
		s := newSyntax(t, "-", []Prefix{{Name: "SHOP-CART-R"}, {Name: "R", Value: 1}}, [2]int{6, 1})
		long := mint(t, s, "SHOP-CART-R")
		occ := s.Scan([]byte("see " + long + "."))
		require.Len(t, occ, 1)
		assert.Equal(t, "SHOP-CART-R", occ[0].Prefix, "the longest registered prefix wins")
	})
	t.Run("only registered ones", func(t *testing.T) {
		s := defaultSyntax(t)
		assert.Empty(t, kinds(s, "Z-"+bodyOf(t, s, "R")))
	})
	t.Run("no prefix: every word that is a valid body", func(t *testing.T) {
		// Ordinary words pass too, 1 in 57 with one check character. Counting them as taken
		// costs a few bodies; leaving out a real ID would let it be minted again.
		s := newSyntax(t, "", nil, [2]int{6, 1}, [2]int{8, 2})
		six := mint(t, newSyntax(t, "", nil, [2]int{6, 1}), "")
		eight := mint(t, s, "")
		assert.Equal(t, []kind{{"ref", six}, {"ref", eight}}, kinds(s, "see "+six+", ["+eight+"]."), "no notation: brackets mean nothing")
		assert.Equal(t, []kind{{"ref", "SELECT"}, {"ref", "cursor"}}, kinds(s, "SELECT cursor Header"), "Header fails the checksum")
		assert.Empty(t, kinds(s, "abc"+six), "glued to a word")
		assert.Empty(t, kinds(s, "000000 100001"), "no placeholders")
	})
}
