package ids

import (
	"fmt"
	"sort"
	"strings"
)

// Site is one place an ID is written.
type Site struct {
	ID   string // as written: prefix, separator and body (or placeholder)
	Path string
	Line int
}

func (s Site) String() string { return fmt.Sprintf("%s:%d", s.Path, s.Line) }

// Check is the kind of a finding. Its value is the code printed in reports: the letter is
// the severity (E for an error, W for a warning) and the number the rule broken, so E4 and
// W4 are the same failure, a checksum that does not hold, found where it is or is not
// surely meant as an ID.
type Check string

const (
	StrayRef        Check = "E1" // a reference with no definition
	DuplicateDef    Check = "E2" // a definition written more than once
	Unresolved      Check = "E3" // a placeholder left in the text
	InvalidDef      Check = "E4" // a definition that fails the checksum: written by hand
	SuspectRef      Check = "W4" // a reference that fails the checksum; may be an ordinary word
	UnreferencedDef Check = "W5" // a definition nothing refers to
	UnmarkedID      Check = "E6" // a valid ID written outside the notation
)

// IsError reports whether the finding fails the check. Two are warnings: a token failing the
// checksum may be an ordinary word like "X-series", and many definitions are naturally
// never referenced.
func (c Check) IsError() bool { return strings.HasPrefix(string(c), "E") }

// Description is one line for people; the ID and the place are the finding's Site.
func (c Check) Description() string {
	switch c {
	case StrayRef:
		return "stray reference"
	case DuplicateDef:
		return "duplicate definition"
	case Unresolved:
		return "unresolved placeholder"
	case InvalidDef:
		return "invalid definition"
	case SuspectRef:
		return "suspect reference"
	case UnreferencedDef:
		return "unreferenced definition"
	case UnmarkedID:
		return "unmarked ID"
	}
	return string(c)
}

// RelatedDescription says what a finding's further places are.
func (c Check) RelatedDescription() string {
	switch c {
	case StrayRef:
		return "also referenced here"
	case DuplicateDef:
		return "also defined here"
	}
	return "also here"
}

// Finding is one problem.
type Finding struct {
	Check   Check
	Site    Site
	Related []Site // the other places of the same problem: further definitions, further references
}

// Graph indexes where each ID is defined and referenced, and holds the findings.
//
// It records places only (path and line), never what surrounds them: what a line means
// depends on the document format, which belongs to whoever reads the graph.
type Graph struct {
	Syntax      *Syntax
	Definitions map[string]Site   // body → first definition (a second one is an E2)
	References  map[string][]Site // body → references, in file and text order
	Findings    []Finding
}

// BuildGraph indexes files and runs every check. E2 and W5 exclude each other: while a
// definition is duplicated, fixing that comes before asking whether anything refers to it.
func BuildGraph(syntax *Syntax, files []File) *Graph {
	g := &Graph{Syntax: syntax, Definitions: map[string]Site{}, References: map[string][]Site{}}
	defined := map[string][]Site{}
	for _, f := range files {
		for _, o := range f.Occurrences {
			site := Site{o.ID, f.Path, o.Line}
			switch {
			case o.IsPlaceholder():
				g.Findings = append(g.Findings, Finding{Check: Unresolved, Site: site})
			case !syntax.Valid(o.ID):
				// Only a definition is surely meant as an ID; anything else may be an ordinary word.
				check := SuspectRef
				if o.Role == Definition {
					check = InvalidDef
				}
				g.Findings = append(g.Findings, Finding{Check: check, Site: site})
			case o.Role == Unmarked:
				g.Findings = append(g.Findings, Finding{Check: UnmarkedID, Site: site})
			case o.Role == Definition:
				defined[o.Body] = append(defined[o.Body], site)
			default:
				g.References[o.Body] = append(g.References[o.Body], site)
			}
		}
	}
	for _, body := range sortedKeys(defined) {
		sites := defined[body]
		g.Definitions[body] = sites[0]
		if len(sites) > 1 {
			g.Findings = append(g.Findings, Finding{Check: DuplicateDef, Site: sites[0], Related: sites[1:]})
		} else if _, ok := g.References[body]; !ok {
			g.Findings = append(g.Findings, Finding{Check: UnreferencedDef, Site: sites[0]})
		}
	}
	for _, body := range sortedKeys(g.References) {
		if _, ok := defined[body]; !ok {
			sites := g.References[body]
			g.Findings = append(g.Findings, Finding{Check: StrayRef, Site: sites[0], Related: sites[1:]})
		}
	}
	return g
}

// DefinitionOf returns where idOrBody is defined.
func (g *Graph) DefinitionOf(idOrBody string) (Site, bool) {
	s, ok := g.Definitions[g.Syntax.BodyOf(idOrBody)]
	return s, ok
}

// ReferencesTo returns the places that refer to idOrBody.
func (g *Graph) ReferencesTo(idOrBody string) []Site {
	return g.References[g.Syntax.BodyOf(idOrBody)]
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
