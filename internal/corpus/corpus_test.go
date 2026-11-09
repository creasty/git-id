package corpus

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/ids"
	"github.com/creasty/git-id/internal/testrepo"
)

const configText = `
include = ["**/*.md", "**/*.txt"]

[prefixes]
R = { value = 0 }
C = { value = 1 }
P = { value = 2 }

[[format]]
body = 6
checksum = 1
`

type fixture struct {
	*testrepo.Repo
	cfg *config.Config
}

func newFixture(t *testing.T, text string) *fixture {
	t.Helper()
	cfg, err := config.Parse([]byte(text))
	require.NoError(t, err)
	return &fixture{testrepo.New(t), cfg}
}

func (f *fixture) corpus() *Corpus { return New(f.Dir, f.cfg) }

func (f *fixture) id(t *testing.T, prefix string) string {
	t.Helper()
	id, err := f.cfg.Syntax.Generate(prefix)
	require.NoError(t, err)
	return id
}

func (f *fixture) taken(t *testing.T) []string {
	t.Helper()
	taken, err := f.corpus().Taken()
	require.NoError(t, err)
	return sortedBodies(taken)
}

func sortedBodies(m map[string]struct{}) []string {
	out := []string{}
	for b := range m {
		out = append(out, b)
	}
	sort.Strings(out)
	return out
}

func (f *fixture) bodies(ids ...string) []string {
	out := []string{}
	for _, id := range ids {
		out = append(out, f.cfg.Syntax.BodyOf(id))
	}
	sort.Strings(out)
	return out
}

// threeStages puts one ID in each of HEAD, the index and the working tree.
func threeStages(t *testing.T, f *fixture) (committed, staged, untracked string) {
	committed, staged, untracked = f.id(t, "R"), f.id(t, "C"), f.id(t, "P")
	f.Write("committed.md", "["+committed+"]\n")
	f.Commit("committed.md")
	f.Write("staged.md", "["+staged+"]\n")
	f.Add("staged.md")
	f.Write("untracked.md", "["+untracked+"]\n")
	return
}

func TestTakenCoversThreeStages(t *testing.T) {
	f := newFixture(t, configText)
	committed, staged, untracked := threeStages(t, f)
	assert.Equal(t, f.bodies(committed, staged, untracked), f.taken(t))

	t.Run("a definition deleted after the commit stays taken", func(t *testing.T) {
		f.Write("committed.md", "deleted\n")
		assert.Contains(t, f.taken(t), f.cfg.Syntax.BodyOf(committed))
	})
	t.Run("staged content edited again stays taken", func(t *testing.T) {
		f.Write("staged.md", "edited after git add\n")
		assert.Contains(t, f.taken(t), f.cfg.Syntax.BodyOf(staged))
	})
}

func TestTakenIgnoresPlaceholdersAndUnscannedFiles(t *testing.T) {
	f := newFixture(t, configText)
	f.Write("a.md", "[R-000000] R-000001\n")
	f.Write("b.go", "// ["+f.id(t, "R")+"]\n")
	f.Commit("a.md", "b.go")
	assert.Empty(t, f.taken(t))
}

func TestTakenWithoutCommits(t *testing.T) {
	f := newFixture(t, configText)
	id := f.id(t, "R")
	f.Write("a.md", id)
	assert.Equal(t, f.bodies(id), f.taken(t))
}

func TestTakenReadsConflicts(t *testing.T) {
	f := newFixture(t, configText)
	f.Write("a.md", "base\n")
	f.Commit("a.md")
	f.Git("checkout", "-q", "-b", "other")
	theirs := f.id(t, "R")
	f.Write("a.md", "["+theirs+"]\n")
	f.Commit("a.md")
	f.Git("checkout", "-q", "main")
	ours := f.id(t, "R")
	f.Write("a.md", "["+ours+"]\n")
	f.Commit("a.md")
	_ = exec.Command("git", "-C", f.Dir, "merge", "-q", "other").Run() // conflicts
	assert.Subset(t, f.taken(t), f.bodies(ours, theirs))
}

func TestWorktree(t *testing.T) {
	f := newFixture(t, configText)
	kept, removed, edited, staged := f.id(t, "R"), f.id(t, "R"), f.id(t, "R"), f.id(t, "C")
	f.Write("kept.md", "["+kept+"]\n")
	f.Write("removed.md", "["+removed+"]\n")
	f.Write("edited.md", "old\n")
	f.Commit("kept.md", "removed.md", "edited.md")
	f.Remove("removed.md")
	f.Write("edited.md", "\n["+edited+"]\n")
	f.Write("staged.md", "["+staged+"]\n")
	f.Add("staged.md")
	f.Write("untracked.md", "see "+kept+"\n")
	f.Write("ignored/x.md", "["+f.id(t, "P")+"]\n")
	f.Write(".gitignore", "ignored/\n")

	files, err := f.corpus().Worktree()
	require.NoError(t, err)
	got := map[string]string{}
	for _, file := range files {
		var parts []string
		for _, o := range file.Occurrences {
			parts = append(parts, o.Token())
		}
		got[file.Path] = strings.Join(parts, " ")
	}
	assert.Equal(t, map[string]string{
		"kept.md":      "[" + kept + "]",
		"edited.md":    "[" + edited + "]",
		"staged.md":    "[" + staged + "]",
		"untracked.md": kept,
	}, got)
	for _, file := range files {
		if file.Path == "edited.md" {
			assert.Equal(t, 2, file.Occurrences[0].Line, "lines come from the working tree content")
		}
	}
}

func TestWorktreeUsesStagedContent(t *testing.T) {
	f := newFixture(t, configText)
	first, second := f.id(t, "R"), f.id(t, "R")
	f.Write("a.md", "["+first+"]\n")
	f.Commit("a.md")
	f.Write("a.md", "["+second+"]\n")
	f.Add("a.md")
	files, err := f.corpus().Worktree()
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, second, files[0].Occurrences[0].ID)

	f.Git("rm", "-q", "--cached", "a.md")
	files, err = f.corpus().Worktree()
	require.NoError(t, err)
	require.Len(t, files, 1, "untracked but still on disk")
}

// TestBinaryFiles follows git's rule for what is binary: the diff attribute decides when it
// is set or unset (the binary macro unsets it); otherwise a NUL in the first 8,000 bytes does.
func TestBinaryFiles(t *testing.T) {
	t.Run("a NUL early makes a file binary in every stage", func(t *testing.T) {
		f := newFixture(t, configText)
		f.WriteBytes("committed.txt", []byte("\x00["+f.id(t, "R")+"]\n"))
		f.Commit("committed.txt")
		f.WriteBytes("staged.txt", []byte("\x00["+f.id(t, "C")+"]\n"))
		f.Add("staged.txt")
		f.WriteBytes("untracked.txt", []byte("\x00["+f.id(t, "P")+"]\n"))
		assert.Empty(t, f.taken(t))
		files, err := f.corpus().Worktree()
		require.NoError(t, err)
		assert.Empty(t, files)
	})
	t.Run("a NUL after the first 8,000 bytes does not", func(t *testing.T) {
		f := newFixture(t, configText)
		id, later := f.id(t, "R"), f.id(t, "C")
		f.WriteBytes("a.txt", append([]byte(strings.Repeat("x", 8000)+"\x00 ["+id+"]\n"), 0xff))
		f.Commit("a.txt")
		f.WriteBytes("b.txt", []byte(strings.Repeat("x", 8000)+"\x00 ["+later+"]\n"))
		assert.Equal(t, f.bodies(id, later), f.taken(t), "invalid UTF-8 is fine too: text is read as bytes")
	})
	t.Run("the diff attribute decides first", func(t *testing.T) {
		f := newFixture(t, configText)
		forced := f.id(t, "R")
		f.Write(".gitattributes", "forced.txt diff\nmarked.md binary\n")
		f.WriteBytes("forced.txt", []byte("\x00["+forced+"]\n"))
		f.Write("marked.md", "["+f.id(t, "R")+"]\n")
		f.Commit(".gitattributes", "forced.txt", "marked.md")
		assert.Equal(t, f.bodies(forced), f.taken(t), "diff forces text; binary skips a text file")

		f.Write("marked.md", "["+f.id(t, "C")+"]\n")
		f.Write("new.md", "["+f.id(t, "C")+"]\n")
		f.Write(".gitattributes", "forced.txt diff\nmarked.md binary\nnew.md -diff\n")
		assert.Equal(t, f.bodies(forced), f.taken(t), "the working tree follows its own attributes")
	})
	t.Run("a change to .gitattributes rereads unchanged files", func(t *testing.T) {
		f := newFixture(t, configText)
		id := f.id(t, "R")
		f.Write(".gitattributes", "a.md -diff\n")
		f.Write("a.md", "["+id+"]\n")
		f.Commit(".gitattributes", "a.md")
		assert.Empty(t, f.taken(t))
		f.Write(".gitattributes", "")
		f.Commit(".gitattributes")
		assert.Equal(t, f.bodies(id), f.taken(t), "a.md did not change, but it is text now")
	})
	t.Run("HEAD follows its own attributes, not staged ones", func(t *testing.T) {
		f := newFixture(t, configText)
		f.Write(".gitattributes", "a.md binary\n")
		f.Write("sub/.gitattributes", "b.md binary\n")
		f.Write("a.md", "["+f.id(t, "R")+"]\n")
		f.Write("sub/b.md", "["+f.id(t, "R")+"]\n")
		f.Commit(".gitattributes", "sub/.gitattributes", "a.md", "sub/b.md")
		f.Write(".gitattributes", "")
		f.Write("sub/.gitattributes", "")
		f.Add(".gitattributes", "sub/.gitattributes")
		assert.Empty(t, f.taken(t), "a.md and sub/b.md are binary in HEAD and unchanged since")
	})
	t.Run("a change to info/attributes rereads too", func(t *testing.T) {
		f := newFixture(t, configText)
		id := f.id(t, "R")
		f.Write("a.md", "["+id+"]\n")
		f.Commit("a.md")
		assert.Equal(t, f.bodies(id), f.taken(t))
		info := filepath.Join(f.Dir, ".git", "info")
		require.NoError(t, os.MkdirAll(info, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(info, "attributes"), []byte("*.md binary\n"), 0o644))
		assert.Empty(t, f.taken(t))
	})
}

func TestHeadCache(t *testing.T) {
	f := newFixture(t, configText)
	committed, _, _ := threeStages(t, f)
	c := f.corpus()
	_, err := c.Taken()
	require.NoError(t, err)
	path, err := c.HeadCachePath()
	require.NoError(t, err)

	cache := readHeadCache(t, path)
	tree := strings.TrimSpace(f.Git("rev-parse", "HEAD^{tree}"))
	assert.Equal(t, tree, cache.Tree)
	assert.Equal(t, []string{"committed.md"}, keys(cache.Files), "only files with IDs are listed")
	assert.Contains(t, path, filepath.Join(".git", CacheDir), "kept inside the git directory")

	t.Run("is used as long as the tree is the same", func(t *testing.T) {
		// Corrupt the cached content: if it is served, the result shows it.
		cache.Files["committed.md"] = headFile{Blob: "x", Occurrences: f.cfg.Syntax.Scan([]byte("[" + committed[:2] + "222222]"))}
		writeJSON(path, cache)
		assert.Contains(t, f.taken(t), "222222")
	})
	t.Run("is updated from the diff when HEAD moves", func(t *testing.T) {
		later := f.id(t, "R")
		f.Write("later.md", "["+later+"]\n")
		f.Commit("later.md")
		taken := f.taken(t)
		assert.Contains(t, taken, f.cfg.Syntax.BodyOf(later), "a body committed later must be taken, or it could be minted again")
		assert.Contains(t, taken, "222222", "unchanged files are not read again")
		assert.Equal(t, strings.TrimSpace(f.Git("rev-parse", "HEAD^{tree}")), readHeadCache(t, path).Tree)
	})
	t.Run("is ignored after a configuration change", func(t *testing.T) {
		other := newFixture(t, strings.Replace(configText, `"**/*.txt"`, `"**/*.yaml"`, 1))
		other.Repo = f.Repo
		taken := other.taken(t)
		assert.NotContains(t, taken, "222222")
		assert.Contains(t, taken, f.cfg.Syntax.BodyOf(committed))
	})
	t.Run("a broken cache is ignored", func(t *testing.T) {
		require.NoError(t, os.WriteFile(path, []byte("{broken"), 0o644))
		assert.Contains(t, f.taken(t), f.cfg.Syntax.BodyOf(committed))
	})
	t.Run("a cached tree that no longer exists is ignored", func(t *testing.T) {
		cache := readHeadCache(t, path)
		cache.Tree = strings.Repeat("0", len(cache.Tree)-1) + "1"
		writeJSON(path, cache)
		assert.Contains(t, f.taken(t), f.cfg.Syntax.BodyOf(committed))
	})
}

func readHeadCache(t *testing.T, path string) headCache {
	t.Helper()
	var c headCache
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(data, &c))
	return c
}

func TestHistory(t *testing.T) {
	f := newFixture(t, "history = true\n"+configText)
	deleted := f.id(t, "R")
	f.Write("a.md", "["+deleted+"]\n")
	f.Commit("a.md")
	f.Git("rm", "-q", "a.md")
	f.Commit()

	f.Git("checkout", "-q", "-b", "side")
	onSide := f.id(t, "R")
	f.Write("b.md", "["+onSide+"]\n")
	f.Commit("b.md")
	f.Git("checkout", "-q", "main")

	assert.Equal(t, f.bodies(deleted, onSide), f.taken(t),
		"a body deleted in an earlier commit, and one only on another branch, are both taken")

	t.Run("without history only the three stages count", func(t *testing.T) {
		off := newFixture(t, configText)
		off.Repo = f.Repo
		assert.Empty(t, off.taken(t))
	})
	t.Run("new commits are read incrementally", func(t *testing.T) {
		c := f.corpus()
		path, err := c.HistoryCachePath()
		require.NoError(t, err)
		var cache historyCache
		require.True(t, readJSON(path, &cache))
		cache.Bodies = append(cache.Bodies, "333333") // visible only if the cache is reused
		writeJSON(path, cache)

		later := f.id(t, "C")
		f.Write("c.md", "["+later+"]\n")
		f.Commit("c.md")
		taken := f.taken(t)
		assert.Contains(t, taken, "333333")
		assert.Contains(t, taken, f.cfg.Syntax.BodyOf(later))
	})
	t.Run("bodies of deleted branches are kept", func(t *testing.T) {
		f.Git("branch", "-q", "-D", "side")
		assert.Contains(t, f.taken(t), f.cfg.Syntax.BodyOf(onSide))
	})
	t.Run("is shared by every worktree", func(t *testing.T) {
		wt := filepath.Join(t.TempDir(), "wt")
		f.Git("worktree", "add", "-q", wt)
		wt, _ = filepath.EvalSymlinks(wt)
		main, err := f.corpus().HistoryCachePath()
		require.NoError(t, err)
		other, err := New(wt, f.cfg).HistoryCachePath()
		require.NoError(t, err)
		assert.Equal(t, main, other)
		h1, _ := f.corpus().HeadCachePath()
		h2, _ := New(wt, f.cfg).HeadCachePath()
		assert.NotEqual(t, h1, h2, "each worktree has its own HEAD")
	})
}

// TestNothingRunsUntilNeeded: a Corpus is created on every hook call, and almost none needs
// it.
func TestNothingRunsUntilNeeded(t *testing.T) {
	f := newFixture(t, configText)
	t.Setenv("PATH", "") // any git call would fail
	c := New(f.Dir, f.cfg)
	assert.NotNil(t, c)
	_, err := c.Taken()
	assert.Error(t, err, "the first real use runs git")
}

func TestScannedFilesOnly(t *testing.T) {
	f := newFixture(t, configText)
	f.Write("a.yaml", "["+f.id(t, "R")+"]\n")
	f.Write("b.md", "["+f.id(t, "R")+"]\n")
	files, err := f.corpus().Worktree()
	require.NoError(t, err)
	var paths []string
	for _, file := range files {
		paths = append(paths, file.Path)
	}
	assert.Equal(t, []string{"b.md"}, paths)
}

func TestHeadCacheFollowsTheNotation(t *testing.T) {
	// Roles are cached with each occurrence, so a new notation must reread unchanged files.
	f := newFixture(t, configText)
	id := f.id(t, "R")
	f.Write("a.md", "see [["+id+"]]\n")
	f.Commit("a.md")
	files, err := f.corpus().Worktree()
	require.NoError(t, err)
	require.Len(t, files, 1)
	assert.Equal(t, ids.Reference, files[0].Occurrences[0].Role)

	wiki := newFixture(t, configText+"\n[notation]\ndefinitions = [\"[[{id}]]\"]\nreferences = [\"{id}\"]\n")
	wiki.Repo = f.Repo
	files, err = wiki.corpus().Worktree()
	require.NoError(t, err)
	assert.Equal(t, ids.Definition, files[0].Occurrences[0].Role)
}
