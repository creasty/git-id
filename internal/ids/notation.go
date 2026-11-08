package ids

import (
	"fmt"
	"sort"
	"strings"
)

// Role is what an occurrence of an ID is: where it is defined, a reference to it, or neither.
type Role int

const (
	Reference Role = iota
	Definition
	Unmarked // no template fits: written outside the notation
)

// DefaultDefinitions and DefaultReferences are the default notation: brackets define, bare
// refers, and the links of Markdown and wikis are references, since their brackets belong to
// the link syntax. The order is the order they are presented in, the most common first.
var (
	DefaultDefinitions = []Template{
		{Before: "[", After: "]", Description: "the ID in brackets, where the item itself is written"},
	}
	DefaultReferences = []Template{
		{Description: "the bare ID, anywhere else"},
		{Before: "[", After: "](", Description: "a Markdown link, `[ID](url)`"},
		{Before: "[", After: "]:", Description: "a Markdown link reference definition, `[ID]: url`"},
		{Before: "[[", After: "]]", Description: "a wiki link"},
	}
)

// Template is one way of writing an ID: literal text before and after it, and, written as a
// leading ^, a start of line before all of it.
type Template struct {
	LineStart     bool
	Before, After string
	Description   string // what the form is for, for whoever writes one; matching ignores it
}

// ParseTemplate reads a template such as "[{id}]" or "^### {id} ".
func ParseTemplate(s string) (Template, error) {
	var t Template
	rest, lineStart := strings.CutPrefix(s, "^")
	t.LineStart = lineStart
	if strings.Count(rest, "{id}") != 1 {
		return t, fmt.Errorf("template %q must contain {id} exactly once", s)
	}
	if strings.ContainsAny(rest, "\r\n") {
		return t, fmt.Errorf("template %q must fit on one line; use ^ for the start of one", s)
	}
	t.Before, t.After, _ = strings.Cut(rest, "{id}")
	return t, nil
}

func (t Template) String() string {
	s := t.Before + "{id}" + t.After
	if t.LineStart {
		s = "^" + s
	}
	return s
}

// context is how much a template asks of the text around an ID: the longer, the more specific.
func (t Template) context() int {
	n := len(t.Before) + len(t.After)
	if t.LineStart {
		n++
	}
	return n
}

// before is what the text must end with before an ID, a start of line written as "\n" (the
// start of the text counts as one).
func (t Template) before() string {
	if t.LineStart {
		return "\n" + t.Before
	}
	return t.Before
}

// fits reports whether the template fits around text[start:end].
func (t Template) fits(text []byte, start, end int) bool {
	b := start - len(t.Before)
	if b < 0 || string(text[b:start]) != t.Before {
		return false
	}
	if t.LineStart && b > 0 && text[b-1] != '\n' {
		return false
	}
	return end+len(t.After) <= len(text) && string(text[end:end+len(t.After)]) == t.After
}

// overlaps reports whether some occurrence could fit both templates.
func (t Template) overlaps(u Template) bool {
	tb, ub := t.before(), u.before()
	return (strings.HasSuffix(tb, ub) || strings.HasSuffix(ub, tb)) &&
		(strings.HasPrefix(t.After, u.After) || strings.HasPrefix(u.After, t.After))
}

// Notation decides the role of each occurrence: the role of the longest template that fits
// around it. With the defaults, `[X-…](url)` fits "[{id}](" before it fits "[{id}]", so a
// Markdown link is a reference.
type Notation struct {
	Definitions, References []Template

	ordered []roleTemplate // longest context first
}

type roleTemplate struct {
	Template
	role Role
}

// NewNotation validates the templates (see ParseTemplate), kept in the order given. Two
// templates of different roles must never tie: when both could fit one occurrence, one must
// ask for more context than the other.
func NewNotation(definitions, references []Template) (*Notation, error) {
	n := &Notation{Definitions: definitions, References: references}
	for _, list := range []struct {
		name      string
		templates []Template
		role      Role
	}{
		{"definitions", definitions, Definition},
		{"references", references, Reference},
	} {
		if len(list.templates) == 0 {
			return nil, fmt.Errorf("notation: %s needs at least one template", list.name)
		}
		for _, t := range list.templates {
			n.ordered = append(n.ordered, roleTemplate{t, list.role})
		}
	}
	for _, d := range n.Definitions {
		for _, r := range n.References {
			if d.overlaps(r) && d.context() == r.context() {
				return nil, fmt.Errorf("notation: definition %q and reference %q can fit the same occurrence; make one ask for more of the text around it", d, r)
			}
		}
	}
	sort.SliceStable(n.ordered, func(i, j int) bool { return n.ordered[i].context() > n.ordered[j].context() })
	return n, nil
}

// DefaultNotation is the notation used when a repository sets none.
func DefaultNotation() *Notation {
	n, err := NewNotation(DefaultDefinitions, DefaultReferences)
	if err != nil {
		panic(err)
	}
	return n
}

// classify returns the role of the ID at text[start:end] and the template it was written with.
func (n *Notation) classify(text []byte, start, end int) (Role, Template) {
	for _, t := range n.ordered {
		if t.fits(text, start, end) {
			return t.role, t.Template
		}
	}
	return Unmarked, Template{}
}
