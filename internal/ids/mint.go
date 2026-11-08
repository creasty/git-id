package ids

import (
	"errors"
	"math/big"
)

// Minter issues IDs whose body nobody has taken, by rejection sampling: draw at random and
// draw again on a hit. It needs one set of taken bodies; whether a body was taken by a
// definition or a reference makes no difference.
//
// The taken set is loaded on first use. Creating a Minter costs nothing, so a caller that
// may not need to mint (a hook on an edit without placeholders) can create one up front.
type Minter struct {
	Syntax *Syntax
	Load   func() (map[string]struct{}, error)

	taken map[string]struct{}
}

// maxAttempts bounds the draws for one ID; reaching it means the space is nearly full.
const maxAttempts = 1 << 16

// Taken is the set of bodies new IDs must avoid.
func (m *Minter) Taken() (map[string]struct{}, error) {
	if m.taken == nil {
		taken, err := m.Load()
		if err != nil {
			return nil, err
		}
		if taken == nil {
			taken = map[string]struct{}{}
		}
		m.taken = taken
	}
	return m.taken, nil
}

// Used reports whether the body of idOrBody is taken.
func (m *Minter) Used(idOrBody string) (bool, error) {
	taken, err := m.Taken()
	if err != nil {
		return false, err
	}
	_, ok := taken[m.Syntax.BodyOf(idOrBody)]
	return ok, nil
}

// Add takes a body that is not written anywhere yet (an assignment recorded by a hook).
func (m *Minter) Add(idOrBody string) error {
	taken, err := m.Taken()
	if err != nil {
		return err
	}
	taken[m.Syntax.BodyOf(idOrBody)] = struct{}{}
	return nil
}

// Mint returns a new ID in the current format and takes its body.
func (m *Minter) Mint(prefix string) (string, error) {
	taken, err := m.Taken()
	if err != nil {
		return "", err
	}
	if full, err := m.full(prefix, taken); err != nil || full {
		if err == nil {
			err = errors.New("every body of the current format is taken; add a wider format")
		}
		return "", err
	}
	for range maxAttempts {
		id, err := m.Syntax.Generate(prefix)
		if err != nil {
			return "", err
		}
		body := m.Syntax.BodyOf(id)
		if _, hit := taken[body]; !hit {
			taken[body] = struct{}{}
			return id, nil
		}
	}
	return "", errors.New("no unused body found; the current format is nearly full, add a wider format")
}

// full reports whether every body the prefix can have in the current format is taken, so
// that drawing again could never succeed. Only tiny formats get there.
func (m *Minter) full(prefix string, taken map[string]struct{}) (bool, error) {
	f := m.Syntax.Current()
	space := f.Space()
	if !space.IsInt64() || space.Int64() > int64(len(taken)) {
		return false, nil
	}
	v, err := m.Syntax.checkPrefix(prefix)
	if err != nil {
		return false, err
	}
	n := 0
	for body := range taken {
		if f.valid(v, body) {
			n++
		}
	}
	return big.NewInt(int64(n)).Cmp(space) >= 0, nil
}
