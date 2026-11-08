package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/git"
	"github.com/creasty/git-id/internal/ids"
)

// skillInstructions prints what an agent needs to know about this repository's IDs, read from
// its configuration. The skill embeds the output when it loads, so the command never fails:
// whatever is wrong is said in the text, and the exit code stays 0.
func (e *env) skillInstructions() int {
	root, err := git.FindRoot(e.dir)
	if err != nil {
		fmt.Fprintln(e.stdout, "git id is not set up here: this is not a git repository.")
		return exitOK
	}
	cfg, err := config.Load(root)
	switch {
	case errors.Is(err, config.ErrNotFound):
		fmt.Fprintf(e.stdout, "git id is not set up in this repository: it has no %s.\n", config.FileName)
	case err != nil:
		fmt.Fprintf(e.stdout, "git id cannot read this repository's configuration, so no ID can be minted or checked until it is fixed: %v\n", err)
	default:
		fmt.Fprint(e.stdout, describe(cfg))
	}
	return exitOK
}

// describe renders a configuration as instructions: the shape of an ID, the prefixes, the
// forms of the notation with what each is for, and the placeholders written in those forms.
func describe(cfg *config.Config) string {
	var b strings.Builder
	s, f := cfg.Syntax, cfg.Syntax.Current()
	shape := fmt.Sprintf("%d characters%s", f.Body, checkCharacters(f))
	if !s.HasPrefixes() {
		fmt.Fprintf(&b, "IDs here have no prefix: an ID is %s, and git id only mints them unique. Get each one from `git id new`; there is no notation to follow and nothing to check.\n", shape)
		return b.String()
	}

	fmt.Fprintf(&b, "An ID is a prefix, `%s`, then %s.", s.Separator, shape)
	var older []string
	for _, other := range s.Formats[:len(s.Formats)-1] {
		older = append(older, fmt.Sprint(other.Body))
	}
	if len(older) > 0 {
		fmt.Fprintf(&b, " Older IDs can also have %s characters.", strings.Join(older, " or "))
	}
	b.WriteString("\n\nPrefixes:\n")
	for _, name := range s.PrefixNames() {
		p, _ := s.Prefix(name)
		item(&b, code(name), p.Description)
	}

	b.WriteString("\nDefinitions, each ID exactly once in the repository:\n")
	for _, t := range s.Notation.Definitions {
		item(&b, code(t.String()), t.Description)
	}
	b.WriteString("\nReferences:\n")
	for _, t := range s.Notation.References {
		item(&b, code(t.String()), t.Description)
	}
	all := append(slices.Clone(s.Notation.Definitions), s.Notation.References...)
	b.WriteString("\n`{id}` stands for the ID")
	if slices.ContainsFunc(all, func(t ids.Template) bool { return t.LineStart }) {
		b.WriteString(" and a leading `^` for the start of a line")
	}
	b.WriteString(". An ID takes the role of the longest form that fits around it")
	if !slices.ContainsFunc(s.Notation.References, bare) && !slices.ContainsFunc(s.Notation.Definitions, bare) {
		b.WriteString("; an ID that fits none is an error (E6)")
	}
	b.WriteString(".\n")

	prefix := s.PrefixNames()[0]
	fresh := prefix + s.Separator + string(ids.Agent) + strings.Repeat("0", f.Body-1)
	slot := fresh[:len(fresh)-1] + "1"
	def, ref := s.Notation.Definitions[0], s.Notation.References[0]
	b.WriteString("\nPlaceholders take an ID's place in those forms:\n")
	item(&b, written(def, fresh), "a new definition; each occurrence becomes a different ID")
	item(&b, written(def, slot)+" with "+written(ref, slot), "a slot; every occurrence of one number becomes the same ID")

	if !slices.Equal(cfg.Include, config.DefaultInclude) || len(cfg.Exclude) > 0 {
		b.WriteString("\nIDs count only in files matching ")
		b.WriteString(codes(cfg.Include))
		if len(cfg.Exclude) > 0 {
			b.WriteString(", except " + codes(cfg.Exclude))
		}
		b.WriteString(".\n")
	}
	return b.String()
}

func checkCharacters(f *ids.Format) string {
	switch f.Checksum {
	case 0:
		return ""
	case 1:
		return ", the last one a check character"
	}
	return fmt.Sprintf(", the last %d check characters", f.Checksum)
}

func bare(t ids.Template) bool { return !t.LineStart && t.Before == "" && t.After == "" }

// written is an ID as it appears in a form.
func written(t ids.Template, id string) string {
	s := code(t.Before + id + t.After)
	if t.LineStart {
		s += " at the start of a line"
	}
	return s
}

func item(b *strings.Builder, term, description string) {
	b.WriteString("- " + term)
	if description != "" {
		b.WriteString(": " + description)
	}
	b.WriteString("\n")
}

// code is a Markdown code span that survives backticks in its content.
func code(s string) string {
	if strings.Contains(s, "`") {
		return "`` " + s + " ``"
	}
	return "`" + s + "`"
}

func codes(list []string) string {
	out := make([]string, len(list))
	for i, s := range list {
		out[i] = code(s)
	}
	return strings.Join(out, ", ")
}
