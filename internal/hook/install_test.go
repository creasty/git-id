package hook

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func installed(t *testing.T, root, agent string) (string, bool) {
	t.Helper()
	file, changed, err := Install(root, agent)
	require.NoError(t, err)
	data, err := os.ReadFile(filepath.Join(root, file))
	require.NoError(t, err)
	return string(data), changed
}

func TestInstallCreates(t *testing.T) {
	root := t.TempDir()
	got, changed := installed(t, root, "claude")
	assert.True(t, changed)
	assert.Equal(t, `{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Edit|Write|MultiEdit|NotebookEdit",
        "hooks": [
          {
            "type": "command",
            "command": "git id hook run claude",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
`, got)

	got, changed = installed(t, root, "codex")
	assert.True(t, changed)
	assert.Contains(t, got, `"matcher": "apply_patch"`)
	assert.Contains(t, got, `"command": "git id hook run codex"`)
	_, err := os.Stat(filepath.Join(root, ".codex", "hooks.json"))
	assert.NoError(t, err)
}

// existing is a settings file as Claude Code writes it: key order, other hooks, non-ASCII
// text and characters JSON encoders like to escape.
const existing = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "permissions": {
    "defaultMode": "acceptEdits",
    "deny": [
      "Read(./secrets/**)"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "check && run < in > out",
            "timeout": 1.5,
            "statusMessage": "見直し中"
          }
        ]
      }
    ],
    "PostToolUse": []
  },
  "enabledPlugins": {},
  "spinnerTipsEnabled": true,
  "model": null
}
`

func TestInstallKeepsTheRest(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte(existing), 0o600))

	got, changed := installed(t, root, "claude")
	assert.True(t, changed)
	want := `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "permissions": {
    "defaultMode": "acceptEdits",
    "deny": [
      "Read(./secrets/**)"
    ]
  },
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "check && run < in > out",
            "timeout": 1.5,
            "statusMessage": "見直し中"
          }
        ]
      },
      {
        "matcher": "Edit|Write|MultiEdit|NotebookEdit",
        "hooks": [
          {
            "type": "command",
            "command": "git id hook run claude",
            "timeout": 30
          }
        ]
      }
    ],
    "PostToolUse": []
  },
  "enabledPlugins": {},
  "spinnerTipsEnabled": true,
  "model": null
}
`
	assert.Equal(t, want, got, "only the new entry is added")
	info, err := os.Stat(path)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm(), "the file mode is kept")

	again, changed := installed(t, root, "claude")
	assert.False(t, changed, "installing twice adds nothing")
	assert.Equal(t, want, again)
}

func TestInstallFindsTheHookUnderAnyMatcher(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.json")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	content := `{"hooks":{"PreToolUse":[{"matcher":"Write","hooks":[{"type":"command","command":"git id hook run claude"}]}]}}`
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
	got, changed := installed(t, root, "claude")
	assert.False(t, changed)
	assert.Equal(t, content, got, "an unchanged file is not even reformatted")
}

func TestInstallRefuses(t *testing.T) {
	for name, content := range map[string]string{
		"invalid JSON":          `{"hooks": `,
		"not an object":         `[]`,
		"hooks of another type": `{"hooks": []}`,
		"PreToolUse not a list": `{"hooks": {"PreToolUse": {}}}`,
		"trailing data":         `{} {}`,
	} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, ".claude", "settings.json")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
			require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
			_, _, err := Install(root, "claude")
			assert.ErrorContains(t, err, filepath.Join(".claude", "settings.json"))
			data, _ := os.ReadFile(path)
			assert.Equal(t, content, string(data), "the file is left as it was")
		})
	}
	_, _, err := Install(t.TempDir(), "other")
	assert.ErrorContains(t, err, "unknown agent")
}
