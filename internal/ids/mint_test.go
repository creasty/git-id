package ids

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinter(t *testing.T) {
	s := defaultSyntax(t)

	t.Run("loads the taken set on first use only", func(t *testing.T) {
		loads := 0
		m := &Minter{Syntax: s, Load: func() (map[string]struct{}, error) { loads++; return nil, nil }}
		assert.Zero(t, loads, "creating a minter does not load")
		_, err := m.Mint("R")
		require.NoError(t, err)
		_, err = m.Mint("C")
		require.NoError(t, err)
		_, err = m.Used(mint(t, s, "R"))
		require.NoError(t, err)
		assert.Equal(t, 1, loads)
	})
	t.Run("avoids taken bodies and takes what it mints", func(t *testing.T) {
		taken := mint(t, s, "R")
		m := &Minter{Syntax: s, Load: func() (map[string]struct{}, error) {
			return map[string]struct{}{s.BodyOf(taken): {}}, nil
		}}
		used, err := m.Used(taken)
		require.NoError(t, err)
		assert.True(t, used)
		id, err := m.Mint("R")
		require.NoError(t, err)
		assert.NotEqual(t, taken, id)
		used, err = m.Used(id)
		require.NoError(t, err)
		assert.True(t, used, "a minted ID is taken at once")
	})
	t.Run("draws again on a hit", func(t *testing.T) {
		// Fill every body but one in a tiny format: minting must find the last one.
		tiny := newSyntax(t, "-", []Prefix{{Name: "R"}}, [2]int{2, 1})
		all := map[string]struct{}{}
		for _, c := range Charset {
			for _, d := range Charset {
				if b := string(c) + string(d); tiny.Current().valid(0, b) {
					all[b] = struct{}{}
				}
			}
		}
		require.Len(t, all, Base, "one valid check per random character")
		var last string
		for b := range all {
			last = b
			delete(all, b)
			break
		}
		m := &Minter{Syntax: tiny, Load: func() (map[string]struct{}, error) { return all, nil }}
		id, err := m.Mint("R")
		require.NoError(t, err)
		assert.Equal(t, "R-"+last, id)

		_, err = m.Mint("R")
		assert.ErrorContains(t, err, "every body of the current format is taken")
	})
	t.Run("added bodies are avoided", func(t *testing.T) {
		m := &Minter{Syntax: s, Load: func() (map[string]struct{}, error) { return nil, nil }}
		id := mint(t, s, "R")
		require.NoError(t, m.Add(id))
		used, err := m.Used(s.BodyOf(id))
		require.NoError(t, err)
		assert.True(t, used)
	})
	t.Run("a load error is returned", func(t *testing.T) {
		m := &Minter{Syntax: s, Load: func() (map[string]struct{}, error) { return nil, errors.New("git failed") }}
		_, err := m.Mint("R")
		assert.EqualError(t, err, "git failed")
	})
}
