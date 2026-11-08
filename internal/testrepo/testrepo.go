// Package testrepo builds throwaway git repositories for tests.
package testrepo

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Repo is a fresh repository in a temporary directory.
type Repo struct {
	t   testing.TB
	Dir string
}

// New creates a repository isolated from the user's git configuration, so a global setting
// (signing, hooks, status defaults) cannot change what the code under test sees.
func New(t testing.TB) *Repo {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(empty, nil, 0o644))
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	r := &Repo{t: t, Dir: dir}
	r.Git("init", "-q", "-b", "main")
	r.Git("config", "user.email", "test@example.test")
	r.Git("config", "user.name", "test")
	return r
}

// Git runs git in the repository and returns its standard output.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	require.NoError(r.t, err, "git %s: %s", strings.Join(args, " "), out)
	return string(out)
}

// Write creates or replaces a file, creating its directories.
func (r *Repo) Write(path, content string) { r.WriteBytes(path, []byte(content)) }

// WriteBytes is Write for raw bytes.
func (r *Repo) WriteBytes(path string, content []byte) {
	r.t.Helper()
	full := filepath.Join(r.Dir, filepath.FromSlash(path))
	require.NoError(r.t, os.MkdirAll(filepath.Dir(full), 0o755))
	require.NoError(r.t, os.WriteFile(full, content, 0o644))
}

// Read returns a file's content.
func (r *Repo) Read(path string) string {
	r.t.Helper()
	b, err := os.ReadFile(filepath.Join(r.Dir, filepath.FromSlash(path)))
	require.NoError(r.t, err)
	return string(b)
}

// Remove deletes a file from the working tree only.
func (r *Repo) Remove(path string) {
	r.t.Helper()
	require.NoError(r.t, os.Remove(filepath.Join(r.Dir, filepath.FromSlash(path))))
}

// Add stages paths.
func (r *Repo) Add(paths ...string) { r.Git(append([]string{"add", "--"}, paths...)...) }

// Commit stages paths and commits.
func (r *Repo) Commit(paths ...string) {
	r.t.Helper()
	if len(paths) > 0 {
		r.Add(paths...)
	}
	r.Git("commit", "-q", "--allow-empty", "--no-verify", "-m", "commit")
}

// Head returns the commit HEAD points at.
func (r *Repo) Head() string { return strings.TrimSpace(r.Git("rev-parse", "HEAD")) }
