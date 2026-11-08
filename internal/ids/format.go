// Package ids defines what an ID looks like, and how IDs are found, minted, resolved and
// checked in text. It does no I/O: where the text comes from is the corpus package's concern.
package ids

import (
	"crypto/rand"
	"fmt"
	"math/big"
)

// Charset is the alphabet of ID bodies: ASCII letters and digits without the look-alikes
// 0, O, 1, I and l. Placeholders start with 0 or 1, so they can never be mistaken for an ID.
const Charset = "23456789ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz"

// Base is the size of Charset. It is odd, so every power of two is invertible modulo any
// power of it; the checksum's guarantees rest on that.
const Base = len(Charset)

var charValue = func() (v [256]int8) {
	for i := range v {
		v[i] = -1
	}
	for i := 0; i < len(Charset); i++ {
		v[Charset[i]] = int8(i)
	}
	return v
}()

// Format is the shape of an ID body: how many characters it has, and how many of the last
// ones are check characters. The rest are random.
//
// A valid body satisfies
//
//	(v·2^(r+1) + d₁·2^r + … + d_r·2^1 + C) mod Base^c = 0
//
// where v is the prefix's value (0 without a prefix), d₁…d_r are the random characters read
// as digits of Charset, and C is the c check characters read as one base-57 number. Because
// every weight and every difference of neighbouring weights is invertible, the check
// rejects every single-character substitution and every swap of adjacent characters; because
// prefix values are distinct, it rejects a body paired with another registered prefix. Any
// other string passes with probability exactly 1/Base^c.
type Format struct {
	Body     int // characters after the separator, check characters included
	Checksum int // trailing check characters

	modulus *big.Int   // Base^Checksum
	weights []*big.Int // for the prefix value, then each random character
}

// NewFormat validates the shape and precomputes its weights.
func NewFormat(body, checksum int) (*Format, error) {
	if body < 2 {
		return nil, fmt.Errorf("body must be at least 2 characters, got %d", body)
	}
	if checksum < 0 || checksum >= body {
		return nil, fmt.Errorf("checksum must be between 0 and %d (body - 1), got %d", body-1, checksum)
	}
	f := &Format{Body: body, Checksum: checksum}
	f.modulus = new(big.Int).Exp(big.NewInt(int64(Base)), big.NewInt(int64(checksum)), nil)
	r := f.random()
	f.weights = make([]*big.Int, r+1)
	w := big.NewInt(2) // the last random character weighs 2, each one before it twice as much
	for i := r; i >= 0; i-- {
		f.weights[i] = new(big.Int).Mod(w, f.modulus)
		w = new(big.Int).Lsh(w, 1)
	}
	return f, nil
}

func (f *Format) random() int { return f.Body - f.Checksum }

// Space is the number of distinct bodies of this shape.
func (f *Format) Space() *big.Int {
	return new(big.Int).Exp(big.NewInt(int64(Base)), big.NewInt(int64(f.random())), nil)
}

// residue is the weighted sum of the prefix value and the random digits, modulo Base^c.
func (f *Format) residue(prefixValue int, digits []int) *big.Int {
	acc := new(big.Int).Mul(f.weights[0], big.NewInt(int64(prefixValue)))
	t := new(big.Int)
	for i, d := range digits {
		acc.Add(acc, t.Mul(f.weights[i+1], t.SetInt64(int64(d))))
	}
	return acc.Mod(acc, f.modulus)
}

// generate returns a random body that is valid with the given prefix value.
func (f *Format) generate(prefixValue int) (string, error) {
	digits := make([]int, f.random())
	if err := randomDigits(digits); err != nil {
		return "", err
	}
	out := make([]byte, f.Body)
	for i, d := range digits {
		out[i] = Charset[d]
	}
	check := f.residue(prefixValue, digits)
	check.Sub(f.modulus, check).Mod(check, f.modulus) // the value that brings the sum to 0
	base := big.NewInt(int64(Base))
	digit := new(big.Int)
	for i := f.Body - 1; i >= len(digits); i-- {
		check.DivMod(check, base, digit)
		out[i] = Charset[digit.Int64()]
	}
	return string(out), nil
}

// valid reports whether body has this shape and passes the check with the given prefix value.
func (f *Format) valid(prefixValue int, body string) bool {
	if len(body) != f.Body {
		return false
	}
	digits := make([]int, len(body))
	for i := 0; i < len(body); i++ {
		v := charValue[body[i]]
		if v < 0 {
			return false
		}
		digits[i] = int(v)
	}
	if f.Checksum == 0 {
		return true
	}
	r := f.random()
	sum := f.residue(prefixValue, digits[:r])
	base := big.NewInt(int64(Base))
	check := new(big.Int)
	for _, d := range digits[r:] {
		check.Mul(check, base).Add(check, big.NewInt(int64(d)))
	}
	return sum.Add(sum, check).Mod(sum, f.modulus).Sign() == 0
}

// randomDigits fills dst with uniformly random digits of Charset from a CSPRNG. Bytes at or
// above the largest multiple of Base are rejected, so every digit is equally likely.
func randomDigits(dst []int) error {
	const limit = 256 / Base * Base
	buf := make([]byte, len(dst)+8)
	for i := 0; i < len(dst); {
		if _, err := rand.Read(buf); err != nil {
			return err
		}
		for _, b := range buf {
			if int(b) < limit {
				dst[i] = int(b) % Base
				if i++; i == len(dst) {
					break
				}
			}
		}
	}
	return nil
}
