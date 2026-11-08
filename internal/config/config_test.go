package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/creasty/git-id/internal/ids"
)

const full = `
separator = "-"
history = true
include = ["**/*.md", "docs/**/*.yaml"]
exclude = ["vendor/**"]

[prefixes]
R = { value = 0, description = "Requirement" }
C = { value = 1 }

[[format]]
body = 6
checksum = 1

[[format]]
body = 8
checksum = 2
`

func TestParse(t *testing.T) {
	c, err := Parse([]byte(full))
	require.NoError(t, err)
	assert.True(t, c.History)
	assert.Equal(t, "-", c.Syntax.Separator)
	assert.Equal(t, []string{"C", "R"}, c.Syntax.PrefixNames())
	p, ok := c.Syntax.Prefix("R")
	require.True(t, ok)
	assert.Equal(t, "Requirement", p.Description)
	require.Len(t, c.Syntax.Formats, 2)
	assert.Equal(t, 8, c.Syntax.Current().Body, "the last format mints")
}

func TestParseDefaults(t *testing.T) {
	c, err := Parse([]byte("[[format]]\nbody = 6\nchecksum = 1\n"))
	require.NoError(t, err)
	assert.Equal(t, "", c.Syntax.Separator, "without prefixes an ID is its body alone")
	assert.False(t, c.History)
	assert.Equal(t, DefaultInclude, c.Include)
	assert.False(t, c.Syntax.HasPrefixes())
}

func TestParseRejects(t *testing.T) {
	for name, text := range map[string]string{
		"no format":                  `separator = "-"`,
		"unknown key":                "[[format]]\nbody = 6\nchecksum = 1\nwidth = 3\n",
		"unknown table":              "[[format]]\nbody = 6\nchecksum = 1\n[other]\nx = 1\n",
		"missing checksum":           "[[format]]\nbody = 6\n",
		"missing body":               "[[format]]\nchecksum = 1\n",
		"body too short":             "[[format]]\nbody = 1\nchecksum = 0\n",
		"checksum too long":          "[[format]]\nbody = 6\nchecksum = 6\n",
		"missing value":              "[prefixes]\nR = {}\n[[format]]\nbody = 6\nchecksum = 1\n",
		"bad separator":              "separator = \"x\"\n[prefixes]\nR = { value = 0 }\n[[format]]\nbody = 6\nchecksum = 1\n",
		"bad glob":                   "include = [\"[\"]\n[[format]]\nbody = 6\nchecksum = 1\n",
		"invalid TOML":               "[[format]\n",
		"separator without prefixes": "separator = \"-\"\n[[format]]\nbody = 6\nchecksum = 1\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(text))
			assert.Error(t, err)
		})
	}
}

func TestParseValueIsOptionalWithoutChecksum(t *testing.T) {
	c, err := Parse([]byte("[prefixes]\nR = {}\nC = {}\n[[format]]\nbody = 6\nchecksum = 0\n"))
	require.NoError(t, err)
	assert.True(t, c.Syntax.HasPrefixes())
}

func TestScanned(t *testing.T) {
	c, err := Parse([]byte(full))
	require.NoError(t, err)
	for path, want := range map[string]bool{
		"README.md":           true,
		"docs/a/b.md":         true,
		"docs/x.yaml":         true,
		"other/x.yaml":        false,
		"vendor/lib/x.md":     false,
		"main.go":             false,
		"docs/a/README.md.go": false,
	} {
		assert.Equal(t, want, c.Scanned(path), path)
	}
}

func TestLoad(t *testing.T) {
	dir := t.TempDir()
	_, err := Load(dir)
	assert.ErrorIs(t, err, ErrNotFound)

	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte(full), 0o644))
	c, err := Load(dir)
	require.NoError(t, err)
	assert.Equal(t, 8, c.Syntax.Current().Body)

	require.NoError(t, os.WriteFile(filepath.Join(dir, FileName), []byte("[[format]]\n"), 0o644))
	_, err = Load(dir)
	assert.ErrorContains(t, err, FileName, "errors name the file")
}

func TestParseNotation(t *testing.T) {
	base := "[prefixes]\nR = { value = 0 }\n[[format]]\nbody = 6\nchecksum = 1\n"
	t.Run("defaults", func(t *testing.T) {
		c, err := Parse([]byte(base))
		require.NoError(t, err)
		assert.Equal(t, ids.DefaultNotation(), c.Syntax.Notation)
	})
	t.Run("each list falls back to its default", func(t *testing.T) {
		c, err := Parse([]byte(base + "[notation]\nreferences = [\"[[{id}]]\"]\n"))
		require.NoError(t, err)
		assert.Equal(t, "[{id}]", c.Syntax.Notation.Definitions[0].String())
		require.Len(t, c.Syntax.Notation.References, 1)
		assert.Equal(t, "[[{id}]]", c.Syntax.Notation.References[0].String())
	})
	for name, text := range map[string]string{
		"a template without {id}": base + "[notation]\ndefinitions = [\"[id]\"]\n",
		"an unknown key":          base + "[notation]\nlinks = [\"{id}\"]\n",
		"a tie between roles":     base + "[notation]\ndefinitions = [\"[{id}]\"]\nreferences = [\"[{id}]\"]\n",
		"without prefixes":        "[[format]]\nbody = 6\nchecksum = 1\n[notation]\ndefinitions = [\"[{id}]\"]\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(text))
			assert.Error(t, err)
		})
	}
}

func TestParseNotationDescriptions(t *testing.T) {
	base := "[prefixes]\nR = { value = 0 }\n[[format]]\nbody = 6\nchecksum = 1\n"
	c, err := Parse([]byte(base + `[notation]
definitions = [
  { template = "^### {id} ", description = "a rule as a heading" },
]
references = ["[[{id}]]", { template = "[[{id}|" }]
`))
	require.NoError(t, err)
	n := c.Syntax.Notation
	require.Len(t, n.Definitions, 1)
	assert.Equal(t, "^### {id} ", n.Definitions[0].String())
	assert.Equal(t, "a rule as a heading", n.Definitions[0].Description)
	assert.Equal(t, []string{"[[{id}]]", "[[{id}|"}, []string{n.References[0].String(), n.References[1].String()}, "strings and tables mix, in order")
	assert.Empty(t, n.References[1].Description)

	for name, notation := range map[string]string{
		"an unknown key":           `definitions = [{ template = "[{id}]", note = "x" }]`,
		"a table without template": `definitions = [{ description = "x" }]`,
		"a number":                 `definitions = [1]`,
		"a non-string value":       `definitions = [{ template = "[{id}]", description = 1 }]`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(base + "[notation]\n" + notation + "\n"))
			assert.Error(t, err)
		})
	}
}
