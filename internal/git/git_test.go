package git

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/creasty/git-id/internal/testrepo"
)

func open(r *testrepo.Repo) *Repo { return &Repo{Root: r.Dir} }

func TestFindRoot(t *testing.T) {
	r := testrepo.New(t)
	r.Write("a/b/c.md", "x")
	root, err := FindRoot(filepath.Join(r.Dir, "a", "b"))
	require.NoError(t, err)
	assert.Equal(t, r.Dir, root)

	_, err = FindRoot(t.TempDir())
	assert.Error(t, err)
}

func TestFindRootOfLinkedWorktree(t *testing.T) {
	r := testrepo.New(t)
	r.Commit()
	wt := filepath.Join(t.TempDir(), "wt")
	r.Git("worktree", "add", "-q", wt)
	wt, err := filepath.EvalSymlinks(wt)
	require.NoError(t, err)
	root, err := FindRoot(wt)
	require.NoError(t, err)
	assert.Equal(t, wt, root, "a worktree's .git is a file")

	gitDir, commonDir, err := (&Repo{Root: wt}).Dirs()
	require.NoError(t, err)
	assert.NotEqual(t, gitDir, commonDir)
	assert.Equal(t, filepath.Join(r.Dir, ".git"), commonDir)
}

func TestStatus(t *testing.T) {
	r := testrepo.New(t)
	t.Run("before the first commit", func(t *testing.T) {
		st, err := open(r).Status()
		require.NoError(t, err)
		assert.Empty(t, st.Head)
	})

	r.Write("committed.md", "x\n")
	r.Write("renamed.md", "y\n")
	r.Write("modified.md", "z\n")
	r.Write("deleted.md", "w\n")
	r.Commit("committed.md", "renamed.md", "modified.md", "deleted.md")
	r.Write("staged.md", "s\n")
	r.Add("staged.md")
	r.Git("mv", "renamed.md", "moved.md")
	r.Write("modified.md", "changed\n")
	r.Remove("deleted.md")
	r.Write("dir/untracked file.md", "u\n")

	st, err := open(r).Status()
	require.NoError(t, err)
	assert.Equal(t, r.Head(), st.Head)
	got := map[string]string{}
	for _, e := range st.Entries {
		got[e.Path] = string([]byte{e.X, e.Y})
	}
	assert.Equal(t, map[string]string{
		"staged.md":   "A.",
		"renamed.md":  "D.",
		"moved.md":    "A.",
		"modified.md": ".M",
		"deleted.md":  ".D",
	}, got, "a rename is a deletion and an addition")
	assert.Equal(t, []string{"dir/untracked file.md"}, st.Untracked, "paths with spaces are not quoted")
	for _, e := range st.Entries {
		if e.Path == "staged.md" {
			assert.Equal(t, strings.TrimSpace(r.Git("rev-parse", ":staged.md")), e.IndexBlob)
		}
	}
}

func TestStatusUnmerged(t *testing.T) {
	r := testrepo.New(t)
	r.Write("a.md", "base\n")
	r.Commit("a.md")
	r.Git("checkout", "-q", "-b", "other")
	r.Write("a.md", "theirs\n")
	r.Commit("a.md")
	r.Git("checkout", "-q", "main")
	r.Write("a.md", "ours\n")
	r.Commit("a.md")
	cmd := []string{"merge", "-q", "other"}
	_ = runAllowFail(r, cmd...)

	st, err := open(r).Status()
	require.NoError(t, err)
	require.Len(t, st.Entries, 1)
	assert.True(t, st.Entries[0].Unmerged())
	assert.Len(t, st.Entries[0].Stages, 3)
}

func runAllowFail(r *testrepo.Repo, args ...string) error {
	_, err := (&Repo{Root: r.Dir}).run(nil, args...)
	return err
}

func TestTreesAndBlobs(t *testing.T) {
	r := testrepo.New(t)
	r.Write("a.md", "one\n")
	r.Write("dir/b.md", "two\n")
	r.Commit("a.md", "dir/b.md")
	require.NoError(t, os.Symlink("a.md", filepath.Join(r.Dir, "link.md")))
	r.Commit("link.md")
	g := open(r)

	first, err := g.Tree("HEAD~1")
	require.NoError(t, err)
	tree, err := g.Tree("HEAD")
	require.NoError(t, err)

	blobs, err := g.LsTree(tree)
	require.NoError(t, err)
	paths := []string{}
	for _, b := range blobs {
		paths = append(paths, b.Path)
	}
	assert.Equal(t, []string{"a.md", "dir/b.md"}, paths, "symlinks are not files to read")

	r.Write("a.md", "changed\n")
	r.Git("rm", "-q", "dir/b.md")
	r.Write("c.md", "new\n")
	r.Commit("a.md", "c.md")
	latest, err := g.Tree("HEAD")
	require.NoError(t, err)
	changes, err := g.DiffTree(first, latest)
	require.NoError(t, err)
	byPath := map[string]string{}
	for _, c := range changes {
		byPath[c.Path] = c.OID
	}
	assert.Equal(t, strings.TrimSpace(r.Git("rev-parse", "HEAD:a.md")), byPath["a.md"])
	assert.Equal(t, "", byPath["dir/b.md"], "deleted")
	assert.Contains(t, byPath, "c.md")
	assert.Equal(t, "", byPath["link.md"], "a symlink is not a regular file")

	var read []string
	oid := byPath["a.md"]
	err = g.ReadBlobs([]string{oid, strings.Repeat("0", len(oid)), byPath["c.md"]}, func(o string, data []byte) error {
		read = append(read, string(data))
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"changed\n", "new\n"}, read, "missing objects are skipped")
}

func TestHistory(t *testing.T) {
	r := testrepo.New(t)
	r.Write("a.md", "one\n")
	r.Commit("a.md")
	first := r.Head()
	r.Git("rm", "-q", "a.md")
	r.Write("b.md", "two\n")
	r.Commit("b.md")
	r.Git("checkout", "-q", "-b", "side")
	r.Write("c.md", "three\n")
	r.Commit("c.md")
	r.Git("checkout", "-q", "main")
	g := open(r)

	tips, err := g.Tips()
	require.NoError(t, err)
	assert.Len(t, tips, 2, "main and side")

	all, err := g.HistoryBlobs(tips, nil)
	require.NoError(t, err)
	assert.Equal(t, []string{"a.md", "b.md", "c.md"}, blobPaths(all), "deleted files and other branches count")

	since, err := g.HistoryBlobs(tips, []string{first})
	require.NoError(t, err)
	assert.NotContains(t, blobPaths(since), "a.md", "what the seen tips reach is skipped")

	existing, err := g.Existing([]string{first, strings.Repeat("1", len(first))})
	require.NoError(t, err)
	assert.Equal(t, []string{first}, existing)
}

func blobPaths(blobs []Blob) []string {
	var out []string
	for _, b := range blobs {
		out = append(out, b.Path)
	}
	sort.Strings(out)
	return out
}
