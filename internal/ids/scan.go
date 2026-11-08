package ids

import "bytes"

// Occurrence is one ID or placeholder found in text. Its role comes from the notation (see
// Notation); a placeholder is told apart by its first character, 0 or 1, which no body
// contains.
type Occurrence struct {
	ID            string // prefix, separator and body as written, without the notation around it
	Prefix        string
	Body          string // the body of a real ID; empty for a placeholder
	Slot          string // the body of a placeholder; empty for a real ID
	Role          Role
	Before, After string // the notation it was written with, such as "[" and "]"
	Line          int    // 1-based
	Start         int    // byte offset of ID in the text; replacing [Start, End) keeps the notation
	End           int
}

// Token is the occurrence as written, notation included.
func (o Occurrence) Token() string { return o.Before + o.ID + o.After }

// IsPlaceholder reports whether the occurrence still waits to be replaced by a real ID.
func (o Occurrence) IsPlaceholder() bool { return o.Slot != "" }

// File is the occurrences of one file, in text order.
type File struct {
	Path        string // repository-relative, slash-separated
	Occurrences []Occurrence
}

// Scan returns every ID and placeholder in text, in order.
//
// An ID must not be glued to a longer ASCII word: the byte before it and the byte after it
// must not be a letter, digit or '_'. Anything else may touch it, so IDs inside Japanese or
// Chinese prose, where words are not separated by spaces, are still found. Text is read as
// bytes: an ID is ASCII, and invalid UTF-8 elsewhere cannot hide one.
//
// Without prefixes, an ID is a word that is a valid body. Ordinary words can be too, so the
// result serves uniqueness only: avoiding them costs a few bodies, never a collision.
func (s *Syntax) Scan(text []byte) []Occurrence {
	if !s.HasPrefixes() {
		return s.scanWords(text)
	}
	sep := []byte(s.Separator)
	var out []Occurrence
	line, counted := 1, 0
	for i := 0; ; {
		k := bytes.Index(text[i:], sep)
		if k < 0 {
			return out
		}
		at := i + k // the separator
		i = at + 1  // where the next search starts unless an ID is found

		start, prefix := at, ""
		for _, p := range s.Prefixes {
			n := len(p.Name)
			if at >= n && string(text[at-n:at]) == p.Name && !wordAt(text, at-n-1) {
				start, prefix = at-n, p.Name
				break
			}
		}
		if prefix == "" {
			continue
		}

		bodyStart := at + len(sep)
		end := bodyStart
		for end < len(text) && isWord(text[end]) {
			end++
		}
		run := text[bodyStart:end]
		if _, known := s.byWidth[len(run)]; !known {
			continue
		}
		var body, slot string
		switch {
		case isPlaceholder(run):
			slot = string(run)
		case inCharset(run):
			body = string(run)
		default:
			continue
		}

		role, t := s.Notation.classify(text, start, end)
		line += bytes.Count(text[counted:start], []byte{'\n'})
		counted = start
		out = append(out, Occurrence{
			ID:     string(text[start:end]),
			Prefix: prefix,
			Body:   body,
			Slot:   slot,
			Role:   role,
			Before: t.Before,
			After:  t.After,
			Line:   line,
			Start:  start,
			End:    end,
		})
		i = end
	}
}

// scanWords returns the words of text that are valid bodies. A body fails the checksum it
// was not minted with, and a minted body always passes it, so the others can never collide.
func (s *Syntax) scanWords(text []byte) []Occurrence {
	var out []Occurrence
	line, counted := 1, 0
	for i := 0; i < len(text); {
		if !isWord(text[i]) {
			i++
			continue
		}
		start := i
		for i < len(text) && isWord(text[i]) {
			i++
		}
		f, known := s.byWidth[i-start]
		if !known || !inCharset(text[start:i]) {
			continue
		}
		word := string(text[start:i])
		if !f.valid(0, word) {
			continue
		}
		line += bytes.Count(text[counted:start], []byte{'\n'})
		counted = start
		out = append(out, Occurrence{ID: word, Body: word, Line: line, Start: start, End: i})
	}
	return out
}

func wordAt(text []byte, i int) bool { return i >= 0 && i < len(text) && isWord(text[i]) }

func inCharset(run []byte) bool {
	for _, c := range run {
		if charValue[c] < 0 {
			return false
		}
	}
	return true
}

// isPlaceholder reports whether run is 0 or 1 followed by decimal digits.
func isPlaceholder(run []byte) bool {
	if run[0] != '0' && run[0] != '1' {
		return false
	}
	for _, c := range run[1:] {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}
