package corpus

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/creasty/git-id/internal/ids"
)

// cacheVersion is part of every fingerprint: bump it when the cached data changes meaning.
const cacheVersion = 2

// CacheDir is where git id keeps its caches, inside a git directory. They are never part of
// the repository and can be deleted at any time.
const CacheDir = "git-id"

// headFile is the occurrences of one file of the HEAD tree.
type headFile struct {
	Blob        string           `json:"blob"`
	Occurrences []ids.Occurrence `json:"occurrences"`
}

// headCache is the HEAD layer of the last run: which tree it describes and the files of that
// tree that contain anything. Files without occurrences are not listed: git diff-tree names
// every path that changes, so the cache never needs to know the rest.
type headCache struct {
	Fingerprint string              `json:"fingerprint"`
	Tree        string              `json:"tree"`
	Files       map[string]headFile `json:"files"`
}

// historyCache is the bodies found in every commit reachable from Tips.
type historyCache struct {
	Fingerprint string   `json:"fingerprint"`
	Tips        []string `json:"tips"`
	Bodies      []string `json:"bodies"`
}

// fingerprint covers everything that changes what a scan finds, so editing the configuration
// never serves stale results: a cache with another fingerprint is ignored. That includes the
// repository's own attribute file, which can make a file binary; .gitattributes files in the
// tree are handled where the tree changes. Global and system attribute files are not
// covered: after changing those, delete the cache.
func (c *Corpus) fingerprint() (string, error) {
	if c.fp != "" {
		return c.fp, nil
	}
	_, commonDir, err := c.gitDirs()
	if err != nil {
		return "", err
	}
	attributes, _ := os.ReadFile(filepath.Join(commonDir, "info", "attributes"))
	s := c.cfg.Syntax
	var widths []int
	for _, f := range s.Formats {
		widths = append(widths, f.Body)
	}
	sort.Ints(widths)
	var notation [2][]string // roles are cached with each occurrence; order and descriptions play no part
	if s.Notation != nil {
		for _, t := range s.Notation.Definitions {
			notation[0] = append(notation[0], t.String())
		}
		for _, t := range s.Notation.References {
			notation[1] = append(notation[1], t.String())
		}
		sort.Strings(notation[0])
		sort.Strings(notation[1])
	}
	b, _ := json.Marshal(struct {
		Version          int
		Separator        string
		Prefixes         []string
		Widths           []int
		Notation         [2][]string
		Include, Exclude []string
		Attributes       []byte
	}{cacheVersion, s.Separator, s.PrefixNames(), widths, notation, c.cfg.Include, c.cfg.Exclude, attributes})
	sum := sha256.Sum256(b)
	c.fp = hex.EncodeToString(sum[:])
	return c.fp, nil
}

// HeadCachePath is the cache of the HEAD layer. It is per worktree, as each has its own HEAD.
func (c *Corpus) HeadCachePath() (string, error) {
	gitDir, _, err := c.gitDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(gitDir, CacheDir, "head.json"), nil
}

// HistoryCachePath is the cache of the history. It is shared by every worktree of the
// repository, as they share their commits.
func (c *Corpus) HistoryCachePath() (string, error) {
	_, commonDir, err := c.gitDirs()
	if err != nil {
		return "", err
	}
	return filepath.Join(commonDir, CacheDir, "history.json"), nil
}

// headLayer returns the scanned files of commit's tree. With a cache of another tree, only
// what git diff-tree reports as changed is read; with no usable cache, every scanned file is.
func (c *Corpus) headLayer(commit string) (map[string]headFile, error) {
	if commit == "" {
		return map[string]headFile{}, nil
	}
	tree, err := c.repo.Tree(commit)
	if err != nil {
		return nil, err
	}
	path, err := c.HeadCachePath()
	if err != nil {
		return nil, err
	}
	fp, err := c.fingerprint()
	if err != nil {
		return nil, err
	}
	var cache headCache
	if !readJSON(path, &cache) || cache.Fingerprint != fp || cache.Files == nil {
		cache = headCache{}
	}
	if cache.Tree == tree {
		return cache.Files, nil
	}

	files := cache.Files
	toRead := map[string][]string{} // blob → paths
	if files != nil {
		changes, err := c.repo.DiffTree(cache.Tree, tree)
		if err != nil {
			files = nil // the cached tree is gone (pruned): start over
		}
		for _, ch := range changes {
			if ch.Path == ".gitattributes" || strings.HasSuffix(ch.Path, "/.gitattributes") {
				files = nil // what is binary may have changed for unchanged files too
				break
			}
			delete(files, ch.Path)
			if ch.OID != "" && c.cfg.Scanned(ch.Path) {
				toRead[ch.OID] = append(toRead[ch.OID], ch.Path)
			}
		}
	}
	if files == nil {
		files = map[string]headFile{}
		toRead = map[string][]string{}
		blobs, err := c.repo.LsTree(tree)
		if err != nil {
			return nil, err
		}
		for _, b := range blobs {
			if c.cfg.Scanned(b.Path) {
				toRead[b.OID] = append(toRead[b.OID], b.Path)
			}
		}
	}
	if err := c.scanBlobs(toRead, []string{"--source=" + tree}, func(oid, p string, occs []ids.Occurrence) {
		if len(occs) > 0 {
			files[p] = headFile{Blob: oid, Occurrences: occs}
		}
	}); err != nil {
		return nil, err
	}
	writeJSON(path, headCache{Fingerprint: fp, Tree: tree, Files: files})
	return files, nil
}

// historyBodies returns every body in any commit reachable from a ref. Commits reachable
// from the tips of the last run are not read again; tips that no longer exist (a deleted and
// pruned branch) are dropped from that set, and their bodies are kept.
func (c *Corpus) historyBodies() (map[string]struct{}, error) {
	path, err := c.HistoryCachePath()
	if err != nil {
		return nil, err
	}
	fp, err := c.fingerprint()
	if err != nil {
		return nil, err
	}
	var cache historyCache
	if !readJSON(path, &cache) || cache.Fingerprint != fp {
		cache = historyCache{}
	}
	bodies := make(map[string]struct{}, len(cache.Bodies))
	for _, b := range cache.Bodies {
		bodies[b] = struct{}{}
	}
	tips, err := c.repo.Tips()
	if err != nil {
		return nil, err
	}
	if slices.Equal(tips, cache.Tips) {
		return bodies, nil
	}
	seen, err := c.repo.Existing(cache.Tips)
	if err != nil {
		return nil, err
	}
	blobs, err := c.repo.HistoryBlobs(tips, seen)
	if err != nil {
		return nil, err
	}
	// A blob is judged at the first path git listed it under, with today's attributes.
	toRead := map[string][]string{}
	for _, b := range blobs {
		if _, seen := toRead[b.OID]; !seen && c.cfg.Scanned(b.Path) {
			toRead[b.OID] = []string{b.Path}
		}
	}
	if err := c.scanBlobs(toRead, nil, func(_, _ string, occs []ids.Occurrence) {
		for _, o := range occs {
			if o.Body != "" {
				bodies[o.Body] = struct{}{}
			}
		}
	}); err != nil {
		return nil, err
	}
	writeJSON(path, historyCache{Fingerprint: fp, Tips: tips, Bodies: keys(bodies)})
	return bodies, nil
}

func readJSON(path string, v any) bool {
	data, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(data, v) == nil
}

// writeJSON replaces a cache atomically, so a concurrent reader sees the old file or the new
// one, never a torn one. Failing to write a cache only costs time later, so errors are dropped.
func writeJSON(path string, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return
	}
	if tmp.Close() == nil {
		os.Rename(tmp.Name(), path)
	}
}
