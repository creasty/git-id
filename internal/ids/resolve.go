package ids

import "strings"

// Scope says who resolves a placeholder. It is the placeholder's first character.
//
// The two are kept apart because they carry assignments differently. A human writes the
// placeholders and runs `git id resolve` once, so an assignment lives for that one run. An
// agent writes across many edits, so the hook keeps each session's assignments, and it must
// tell the agent the result, or the agent keeps talking about the placeholder.
type Scope byte

const (
	Human Scope = '0' // resolved by `git id resolve`
	Agent Scope = '1' // resolved by the agent hook
)

func slotScope(slot string) Scope { return Scope(slot[0]) }

// isFresh reports whether everything after the scope digit is 0. Any other number is a slot:
// every occurrence of the same slot resolves to the same ID.
func isFresh(slot string) bool { return strings.Trim(slot[1:], "0") == "" }

// Guide points people and agents to how IDs are written. Messages name what went wrong and
// leave how to fix it to the guide, so the rules are stated in one place.
const Guide = "See https://github.com/creasty/git-id#notation."

// Reason is why a resolver left an occurrence as it was.
type Reason string

const (
	ReasonForeign    Reason = "foreign"
	ReasonFreshRef   Reason = "fresh_ref"
	ReasonInvalidDef Reason = "invalid_def"
	ReasonUnmarked   Reason = "unmarked"
)

// Description is one line for people and agents. How to write it correctly is the guide's job.
func (r Reason) Description() string {
	switch r {
	case ReasonForeign:
		return "a placeholder of the other scope (first digit 0 is for `git id resolve`, 1 for the agent hook)"
	case ReasonFreshRef:
		return "a fresh placeholder written as a reference; it would refer to nothing"
	case ReasonInvalidDef:
		return "a definition that fails the checksum, so it was not minted by git id"
	case ReasonUnmarked:
		return "written outside the notation: no definition or reference template fits around it"
	}
	return string(r)
}

// Replaced is one placeholder that was replaced.
type Replaced struct {
	Occurrence Occurrence
	New        string // the ID it became
	Path       string // empty when the text has no file (hooks)
}

// Token is the replacement as written, with the notation of the placeholder.
func (r Replaced) Token() string {
	return strings.Replace(r.Occurrence.Token(), r.Occurrence.ID, r.New, 1)
}

// Rejected is one occurrence left as it was, and why.
type Rejected struct {
	Reason     Reason
	Occurrence Occurrence
	Path       string
}

// Resolver replaces placeholders with real IDs.
//
// It resolves placeholders of its own Scope; others are left and reported, unless Sweep is
// set, in which case it takes them too within this resolver's assignments.
//
// Mint and LoadSlots are called only when a placeholder needs them, so text without
// placeholders mints nothing and reads no record. Saving the slots back is the caller's job:
// see Slots and SlotsChanged.
type Resolver struct {
	Syntax    *Syntax
	Scope     Scope
	Sweep     bool
	Mint      func(prefix string) (string, error)
	LoadSlots func() (map[string]string, error) // nil: assignments live in this resolver only

	Replaced     []Replaced
	Rejected     []Rejected
	SlotsChanged bool

	slots map[string]string
}

// Slots is the slot → ID assignment, loaded on first use.
func (r *Resolver) Slots() (map[string]string, error) {
	if r.slots == nil {
		r.slots = map[string]string{}
		if r.LoadSlots != nil {
			loaded, err := r.LoadSlots()
			if err != nil {
				return nil, err
			}
			if loaded != nil {
				r.slots = loaded
			}
		}
	}
	return r.slots, nil
}

// replacement decides what one occurrence becomes; "" leaves it as it is.
func (r *Resolver) replacement(o Occurrence, path string) (string, error) {
	if !o.IsPlaceholder() {
		if reason, bad := r.Syntax.violation(o); bad {
			r.Rejected = append(r.Rejected, Rejected{reason, o, path})
		}
		return "", nil
	}
	if !r.Sweep && slotScope(o.Slot) != r.Scope {
		r.Rejected = append(r.Rejected, Rejected{ReasonForeign, o, path})
		return "", nil
	}
	if o.Role == Unmarked {
		r.Rejected = append(r.Rejected, Rejected{ReasonUnmarked, o, path})
		return "", nil
	}
	if isFresh(o.Slot) {
		if o.Role != Definition {
			r.Rejected = append(r.Rejected, Rejected{ReasonFreshRef, o, path})
			return "", nil
		}
		return r.Mint(o.Prefix)
	}
	slots, err := r.Slots()
	if err != nil {
		return "", err
	}
	if id, ok := slots[o.ID]; ok {
		return id, nil
	}
	id, err := r.Mint(o.Prefix)
	if err != nil {
		return "", err
	}
	slots[o.ID] = id
	r.SlotsChanged = true
	return id, nil
}

// Resolve returns text with its placeholders replaced. Only the ID itself is replaced, so a
// definition keeps its brackets and a reference stays bare; every other byte is kept.
func (r *Resolver) Resolve(text []byte, path string) ([]byte, error) {
	var out []byte
	last := 0
	for _, o := range r.Syntax.Scan(text) {
		id, err := r.replacement(o, path)
		if err != nil {
			return nil, err
		}
		if id == "" {
			continue
		}
		out = append(out, text[last:o.Start]...)
		out = append(out, id...)
		last = o.End
		r.Replaced = append(r.Replaced, Replaced{o, id, path})
	}
	if out == nil {
		return text, nil
	}
	return append(out, text[last:]...), nil
}

// violation reports how a real ID breaks the notation: a definition failing the checksum, or
// a valid ID written outside the notation. Anything else failing the checksum may be an
// ordinary word, so it is not a violation.
func (s *Syntax) violation(o Occurrence) (Reason, bool) {
	switch {
	case o.Role == Definition && !s.Valid(o.ID):
		return ReasonInvalidDef, true
	case o.Role == Unmarked && s.Valid(o.ID):
		return ReasonUnmarked, true
	}
	return "", false
}

// Actionable reports whether a resolver would replace or reject the occurrence, whatever its
// scope: it is a placeholder, or a real ID that breaks the notation.
func (s *Syntax) Actionable(o Occurrence) bool {
	if o.IsPlaceholder() {
		return true
	}
	_, bad := s.violation(o)
	return bad
}
