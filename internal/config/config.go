// Package config reads .git-id.toml at the root of a repository.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/bmatcuk/doublestar/v4"

	"github.com/creasty/git-id/internal/ids"
)

// FileName is the configuration file, at the root of the working tree.
const FileName = ".git-id.toml"

// ErrNotFound means the repository has no configuration: it does not use git id.
var ErrNotFound = errors.New(FileName + " not found")

// DefaultInclude is the files scanned when include is not set: all of them, binary ones aside.
var DefaultInclude = []string{"**"}

// Config is a validated configuration.
type Config struct {
	Syntax  *ids.Syntax
	History bool     // also take every body ever committed on any ref when minting
	Include []string // glob patterns of the files to scan, relative to the root
	Exclude []string
}

type file struct {
	Separator *string                `toml:"separator"`
	History   bool                   `toml:"history"`
	Include   []string               `toml:"include"`
	Exclude   []string               `toml:"exclude"`
	Prefixes  map[string]prefixEntry `toml:"prefixes"`
	Format    []formatEntry          `toml:"format"`
	Notation  *notationEntry         `toml:"notation"`
}

// notationEntry overrides the default notation one list at a time.
type notationEntry struct {
	Definitions []templateEntry `toml:"definitions"`
	References  []templateEntry `toml:"references"`
}

// templateEntry is a notation template, written as a string or, to say what the form is for,
// as a table: { template = "...", description = "..." }.
type templateEntry struct{ ids.Template }

func (e *templateEntry) UnmarshalTOML(v any) error {
	text, description := "", ""
	switch v := v.(type) {
	case string:
		text = v
	case map[string]any:
		for key, val := range v {
			s, ok := val.(string)
			switch {
			case !ok:
				return fmt.Errorf("notation: %q must be a string", key)
			case key == "template":
				text = s
			case key == "description":
				description = s
			default:
				return fmt.Errorf("notation: unknown key %q; a template takes template and description", key)
			}
		}
		if text == "" {
			return fmt.Errorf("notation: a table needs a template")
		}
	default:
		return fmt.Errorf("notation: a template is a string or a { template, description } table")
	}
	t, err := ids.ParseTemplate(text)
	if err != nil {
		return fmt.Errorf("notation: %w", err)
	}
	t.Description = description
	e.Template = t
	return nil
}

// templates unwraps entries, or returns the default when the list was left out.
func templates(entries []templateEntry, defaults []ids.Template) []ids.Template {
	if entries == nil {
		return defaults
	}
	out := make([]ids.Template, len(entries))
	for i, e := range entries {
		out[i] = e.Template
	}
	return out
}

type prefixEntry struct {
	Value       *int   `toml:"value"`
	Description string `toml:"description"`
}

type formatEntry struct {
	Body     *int `toml:"body"`
	Checksum *int `toml:"checksum"`
}

// Load reads the configuration of the working tree at root.
func Load(root string) (*Config, error) {
	data, err := os.ReadFile(filepath.Join(root, FileName))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c, err := Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", FileName, err)
	}
	return c, nil
}

// Parse validates a configuration. Unknown keys are errors, and so is anything left to a
// default that would shape IDs: those cannot change once IDs are written.
func Parse(data []byte) (*Config, error) {
	var f file
	md, err := toml.NewDecoder(bytes.NewReader(data)).Decode(&f)
	if err != nil {
		return nil, err
	}
	if undecoded := md.Undecoded(); len(undecoded) > 0 {
		keys := make([]string, len(undecoded))
		for i, k := range undecoded {
			keys[i] = k.String()
		}
		return nil, fmt.Errorf("unknown keys: %s", strings.Join(keys, ", "))
	}

	if len(f.Format) == 0 {
		return nil, errors.New("at least one [[format]] is required")
	}
	formats := make([]*ids.Format, len(f.Format))
	checksummed := false
	for i, e := range f.Format {
		if e.Body == nil || e.Checksum == nil {
			return nil, fmt.Errorf("format %d: both body and checksum are required", i+1)
		}
		fm, err := ids.NewFormat(*e.Body, *e.Checksum)
		if err != nil {
			return nil, fmt.Errorf("format %d: %w", i+1, err)
		}
		formats[i] = fm
		checksummed = checksummed || fm.Checksum > 0
	}

	names := make([]string, 0, len(f.Prefixes))
	for name := range f.Prefixes {
		names = append(names, name)
	}
	sort.Strings(names)
	prefixes := make([]ids.Prefix, 0, len(names))
	for _, name := range names {
		e := f.Prefixes[name]
		p := ids.Prefix{Name: name, Description: e.Description}
		if e.Value != nil {
			p.Value = *e.Value
		} else if checksummed {
			return nil, fmt.Errorf("prefix %q: value is required, since a format has a checksum", name)
		}
		prefixes = append(prefixes, p)
	}

	sep := ""
	if f.Separator != nil {
		sep = *f.Separator
	} else if len(prefixes) > 0 {
		sep = "-"
	}
	var notation *ids.Notation
	if f.Notation != nil {
		defs := templates(f.Notation.Definitions, ids.DefaultDefinitions)
		refs := templates(f.Notation.References, ids.DefaultReferences)
		if notation, err = ids.NewNotation(defs, refs); err != nil {
			return nil, err
		}
	}
	syntax, err := ids.NewSyntax(sep, prefixes, formats, notation)
	if err != nil {
		return nil, err
	}

	include := f.Include
	if include == nil {
		include = DefaultInclude
	}
	for _, p := range append(append([]string(nil), include...), f.Exclude...) {
		if !doublestar.ValidatePattern(p) {
			return nil, fmt.Errorf("invalid glob pattern %q", p)
		}
	}
	return &Config{Syntax: syntax, History: f.History, Include: include, Exclude: f.Exclude}, nil
}

// Scanned reports whether the file at a slash-separated path relative to the root is read.
func (c *Config) Scanned(path string) bool {
	return matchAny(c.Include, path) && !matchAny(c.Exclude, path)
}

func matchAny(patterns []string, path string) bool {
	for _, p := range patterns {
		if ok, _ := doublestar.Match(p, path); ok {
			return true
		}
	}
	return false
}
