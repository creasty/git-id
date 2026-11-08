package ids

import (
	"fmt"
	"math/big"
	"sort"
	"strings"
)

// Prefix is a registered prefix: the human-readable kind written before the separator.
type Prefix struct {
	Name        string
	Value       int // what the prefix contributes to the checksum; unique
	Description string
}

// Syntax is the set of ID shapes a repository accepts:
//
//	<prefix><separator><body>
//
// The prefix is one of the registered ones. With none registered, an ID is its body alone:
// it cannot be told apart from an ordinary word, so such IDs are minted unique and nothing
// more (no notation, no checks). Every format stays valid; the last one mints.
type Syntax struct {
	Separator string    // empty without prefixes
	Prefixes  []Prefix  // longest name first, the order in which text is matched
	Formats   []*Format // oldest first
	Notation  *Notation // how definitions and references are written; nil without prefixes

	byName  map[string]Prefix
	byWidth map[int]*Format
}

// NewSyntax validates the combination; a nil notation is the default one. Every rule fails
// closed: an ID minted under an ambiguous syntax could later be read as something else, and
// IDs cannot be renamed.
func NewSyntax(separator string, prefixes []Prefix, formats []*Format, notation *Notation) (*Syntax, error) {
	if len(prefixes) == 0 {
		if separator != "" {
			return nil, fmt.Errorf("a separator needs prefixes: without one, an ID is its body alone")
		}
		if notation != nil {
			return nil, fmt.Errorf("a notation needs prefixes: without one, IDs cannot be told apart from ordinary words")
		}
	} else {
		if err := validateSeparator(separator); err != nil {
			return nil, err
		}
		if notation == nil {
			notation = DefaultNotation()
		}
	}
	if len(formats) == 0 {
		return nil, fmt.Errorf("at least one format is required")
	}
	s := &Syntax{
		Separator: separator,
		Prefixes:  append([]Prefix(nil), prefixes...),
		Formats:   formats,
		Notation:  notation,
		byName:    map[string]Prefix{},
		byWidth:   map[int]*Format{},
	}
	for _, f := range formats {
		if _, dup := s.byWidth[f.Body]; dup {
			return nil, fmt.Errorf("two formats have a body of %d characters; formats are told apart by their width", f.Body)
		}
		s.byWidth[f.Body] = f
	}

	// Values must stay distinct under every checksum, so they are bounded by the smallest one.
	var limit *big.Int
	for _, f := range formats {
		if f.Checksum > 0 && (limit == nil || f.modulus.Cmp(limit) < 0) {
			limit = f.modulus
		}
	}
	values := map[int]string{}
	for _, p := range s.Prefixes {
		if err := validatePrefixName(p.Name, separator); err != nil {
			return nil, err
		}
		if _, dup := s.byName[p.Name]; dup {
			return nil, fmt.Errorf("prefix %q is registered twice", p.Name)
		}
		s.byName[p.Name] = p
		if limit == nil {
			continue // no format has a checksum, so values are unused
		}
		if p.Value < 0 || big.NewInt(int64(p.Value)).Cmp(limit) >= 0 {
			return nil, fmt.Errorf("prefix %q: value must be between 0 and %s, got %d", p.Name, new(big.Int).Sub(limit, big.NewInt(1)), p.Value)
		}
		if other, dup := values[p.Value]; dup {
			return nil, fmt.Errorf("prefixes %q and %q share the value %d; the checksum could not tell them apart", other, p.Name, p.Value)
		}
		values[p.Value] = p.Name
	}
	for _, a := range s.Prefixes {
		for _, b := range s.Prefixes {
			if a.Name != b.Name && strings.HasPrefix(b.Name, a.Name+separator) {
				return nil, fmt.Errorf("prefix %q starts with prefix %q and the separator, so %q followed by a body would be ambiguous", b.Name, a.Name, b.Name)
			}
		}
	}
	sort.SliceStable(s.Prefixes, func(i, j int) bool { return len(s.Prefixes[i].Name) > len(s.Prefixes[j].Name) })
	return s, nil
}

func validateSeparator(sep string) error {
	if sep == "" {
		return fmt.Errorf("separator must not be empty")
	}
	for _, r := range sep {
		if r < 0x80 && (isWord(byte(r)) || strings.ContainsRune(" \t\r\n[]():|", r)) {
			return fmt.Errorf("separator %q: letters, digits, '_', whitespace and []():| are not allowed", sep)
		}
	}
	return nil
}

func validatePrefixName(name, sep string) error {
	if name == "" {
		return fmt.Errorf("prefix names must not be empty")
	}
	for _, segment := range strings.Split(name, sep) {
		if segment == "" {
			return fmt.Errorf("prefix %q: the separator may only join letters and digits", name)
		}
		for i := 0; i < len(segment); i++ {
			if c := segment[i]; !isWord(c) || c == '_' {
				return fmt.Errorf("prefix %q: only ASCII letters and digits are allowed", name)
			}
		}
	}
	return nil
}

// Current is the format new IDs are minted in.
func (s *Syntax) Current() *Format { return s.Formats[len(s.Formats)-1] }

// HasPrefixes reports whether IDs carry a prefix.
func (s *Syntax) HasPrefixes() bool { return len(s.Prefixes) > 0 }

// Prefix looks up a registered prefix.
func (s *Syntax) Prefix(name string) (Prefix, bool) {
	p, ok := s.byName[name]
	return p, ok
}

// PrefixNames lists the registered prefixes in alphabetical order.
func (s *Syntax) PrefixNames() []string {
	names := make([]string, 0, len(s.Prefixes))
	for _, p := range s.Prefixes {
		names = append(names, p.Name)
	}
	sort.Strings(names)
	return names
}

// checkPrefix returns the value a prefix contributes to the checksum, or an error naming the
// registered ones.
func (s *Syntax) checkPrefix(prefix string) (int, error) {
	if !s.HasPrefixes() {
		if prefix != "" {
			return 0, fmt.Errorf("IDs in this repository have no prefix, got %q", prefix)
		}
		return 0, nil
	}
	p, ok := s.byName[prefix]
	if !ok {
		return 0, fmt.Errorf("unknown prefix %q; registered: %s", prefix, strings.Join(s.PrefixNames(), ", "))
	}
	return p.Value, nil
}

// Generate returns a random ID in the current format. Whether it is unused is the caller's
// concern (see Minter).
func (s *Syntax) Generate(prefix string) (string, error) {
	v, err := s.checkPrefix(prefix)
	if err != nil {
		return "", err
	}
	body, err := s.Current().generate(v)
	if err != nil {
		return "", err
	}
	return prefix + s.Separator + body, nil
}

// Split breaks an exact ID-shaped string into prefix and body. It checks the prefix and the
// width but not the characters or the checksum.
func (s *Syntax) Split(id string) (prefix, body string, ok bool) {
	body = id
	if s.HasPrefixes() {
		for _, p := range s.Prefixes {
			if rest, found := strings.CutPrefix(id, p.Name+s.Separator); found {
				prefix, body = p.Name, rest
				break
			}
		}
		if prefix == "" {
			return "", "", false
		}
	}
	if _, known := s.byWidth[len(body)]; !known {
		return "", "", false
	}
	return prefix, body, true
}

// Valid reports whether id is a well-formed ID that passes the checksum of its format.
func (s *Syntax) Valid(id string) bool {
	prefix, body, ok := s.Split(id)
	if !ok {
		return false
	}
	v, err := s.checkPrefix(prefix)
	if err != nil {
		return false
	}
	return s.byWidth[len(body)].valid(v, body)
}

// BodyOf returns the body of an ID, or the argument itself when it is already a body.
// Bodies are the key of every index: the prefix is not part of the identity, so a body is
// never reused under another prefix.
func (s *Syntax) BodyOf(idOrBody string) string {
	if s.Separator == "" {
		return idOrBody
	}
	if i := strings.LastIndex(idOrBody, s.Separator); i >= 0 {
		return idOrBody[i+len(s.Separator):]
	}
	return idOrBody
}

func isWord(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c == '_'
}
