package ids

import (
	"math/big"
	mathrand "math/rand/v2"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An ID cannot be reissued once it is written in a document, so the detection the checksum
// promises is checked exhaustively ("never misses"), not by sampling ("rarely misses"). Only
// the behaviour outside the promise (how often garbage passes) is sampled.

// shapes cover the default, longer checksums, a format where the checksum is most of the
// body, and the shortest legal body.
var shapes = [][2]int{{6, 1}, {6, 2}, {8, 2}, {5, 3}, {3, 1}, {2, 1}}

var rng = mathrand.New(mathrand.NewPCG(1, 2))

func TestCharset(t *testing.T) {
	t.Run("its size is the base of the checksum", func(t *testing.T) {
		assert.Equal(t, 57, Base)
		seen := map[rune]bool{}
		for _, c := range Charset {
			assert.False(t, seen[c], "duplicate %q", c)
			seen[c] = true
		}
	})
	t.Run("has no look-alikes", func(t *testing.T) {
		for _, c := range "0O1Il" {
			assert.NotContains(t, Charset, string(c))
		}
	})
	t.Run("base is odd so that powers of two are invertible", func(t *testing.T) {
		assert.Equal(t, 1, Base%2)
	})
}

func TestNewFormat(t *testing.T) {
	for _, c := range []struct {
		body, checksum int
		ok             bool
	}{
		{2, 0, true}, {2, 1, true}, {6, 1, true}, {6, 5, true},
		{1, 0, false}, {0, 0, false}, {6, 6, false}, {6, -1, false},
	} {
		_, err := NewFormat(c.body, c.checksum)
		assert.Equal(t, c.ok, err == nil, "body=%d checksum=%d: %v", c.body, c.checksum, err)
	}
}

// TestSingleCheckDigitFormula pins the checksum with one check character to its closed form,
// computed independently with machine integers:
//
//	v·2^6 + d₁·2^5 + d₂·2^4 + d₃·2^3 + d₄·2^2 + d₅·2 + d₆ ≡ 0 (mod 57)
//
// IDs minted before this tool existed follow exactly this rule, so it must not drift.
func TestSingleCheckDigitFormula(t *testing.T) {
	f, err := NewFormat(6, 1)
	require.NoError(t, err)
	weights := make([]int64, len(f.weights))
	for i, w := range f.weights {
		weights[i] = w.Int64()
	}
	assert.Equal(t, []int64{7, 32, 16, 8, 4, 2}, weights, "2^6 mod 57 = 7, then halving")

	for range 2000 {
		v := rng.IntN(Base)
		body, err := f.generate(v)
		require.NoError(t, err)
		sum := v * 64
		for i := 0; i < 6; i++ {
			sum += int(charValue[body[i]]) << (5 - i)
		}
		assert.Zero(t, sum%57, "v=%d body=%s", v, body)
	}
}

func TestGenerateIsValid(t *testing.T) {
	for _, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		for range 2000 {
			v := rng.IntN(Base)
			body, err := f.generate(v)
			require.NoError(t, err)
			require.Len(t, body, sh[0])
			require.True(t, f.valid(v, body), "shape=%v v=%d body=%s", sh, v, body)
		}
	}
}

func TestValidRejectsWrongShape(t *testing.T) {
	f, err := NewFormat(6, 1)
	require.NoError(t, err)
	body, err := f.generate(0)
	require.NoError(t, err)
	for _, bad := range []string{body[:5], body + "2", body[:5] + "0", body[:5] + "l", ""} {
		assert.False(t, f.valid(0, bad), bad)
	}
}

func TestDetectsEverySingleSubstitution(t *testing.T) {
	for _, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		if sh[1] == 0 {
			continue
		}
		for range 200 {
			v := rng.IntN(Base)
			body, err := f.generate(v)
			require.NoError(t, err)
			for i := 0; i < len(body); i++ {
				for j := 0; j < Base; j++ {
					if Charset[j] == body[i] {
						continue
					}
					typo := body[:i] + string(Charset[j]) + body[i+1:]
					require.False(t, f.valid(v, typo), "shape=%v %s -> %s", sh, body, typo)
				}
			}
		}
	}
}

func TestDetectsEveryAdjacentTransposition(t *testing.T) {
	for _, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		for range 2000 {
			v := rng.IntN(Base)
			body, err := f.generate(v)
			require.NoError(t, err)
			for i := 0; i+1 < len(body); i++ {
				if body[i] == body[i+1] {
					continue // swapping equal characters changes nothing
				}
				b := []byte(body)
				b[i], b[i+1] = b[i+1], b[i]
				require.False(t, f.valid(v, string(b)), "shape=%v %s -> %s", sh, body, b)
			}
		}
	}
}

func TestDetectsAnotherPrefix(t *testing.T) {
	for _, sh := range shapes {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		limit := int(f.modulus.Int64())
		for range 200 {
			v := rng.IntN(limit)
			body, err := f.generate(v)
			require.NoError(t, err)
			for range 50 {
				w := rng.IntN(limit)
				if w != v {
					require.False(t, f.valid(w, body), "shape=%v body=%s v=%d w=%d", sh, body, v, w)
				}
			}
		}
	}
}

// TestExactlyOneCheckPerRandomPart is the exact form of "garbage passes with probability
// 1/Base^c": for any random part and prefix, exactly one of the Base^c check values passes.
func TestExactlyOneCheckPerRandomPart(t *testing.T) {
	for _, sh := range [][2]int{{4, 1}, {5, 2}} {
		f, err := NewFormat(sh[0], sh[1])
		require.NoError(t, err)
		r := sh[0] - sh[1]
		for range 20 {
			random := make([]byte, r)
			for i := range random {
				random[i] = Charset[rng.IntN(Base)]
			}
			v := rng.IntN(Base)
			passing := 0
			n := int(new(big.Int).Exp(big.NewInt(int64(Base)), big.NewInt(int64(sh[1])), nil).Int64())
			for x := 0; x < n; x++ {
				check := make([]byte, sh[1])
				for i, y := sh[1]-1, x; i >= 0; i, y = i-1, y/Base {
					check[i] = Charset[y%Base]
				}
				if f.valid(v, string(random)+string(check)) {
					passing++
				}
			}
			assert.Equal(t, 1, passing, "shape=%v random=%s", sh, random)
		}
	}
}

func TestRandomStringsPassAtOneInBase(t *testing.T) {
	f, err := NewFormat(6, 1)
	require.NoError(t, err)
	const trials = 200_000
	passed := 0
	var b strings.Builder
	for range trials {
		b.Reset()
		for range 6 {
			b.WriteByte(Charset[rng.IntN(Base)])
		}
		if f.valid(0, b.String()) {
			passed++
		}
	}
	rate := float64(passed) / trials
	assert.InDelta(t, 1.0/57, rate, 0.002)
}

func TestGeneratedDigitsAreUniform(t *testing.T) {
	f, err := NewFormat(6, 1)
	require.NoError(t, err)
	counts := map[byte]int{}
	for range Base * 1000 {
		body, err := f.generate(0)
		require.NoError(t, err)
		counts[body[0]]++
	}
	for i := 0; i < Base; i++ {
		n := counts[Charset[i]]
		assert.True(t, n > 800 && n < 1200, "%q drawn %d times out of ~1000", Charset[i], n)
	}
}

func TestNoChecksumAcceptsAnyBody(t *testing.T) {
	f, err := NewFormat(4, 0)
	require.NoError(t, err)
	assert.True(t, f.valid(0, "abcd"))
	assert.True(t, f.valid(5, "abcd"), "without a checksum the prefix value plays no part")
	assert.False(t, f.valid(0, "abc0"), "the characters are still checked")
}
