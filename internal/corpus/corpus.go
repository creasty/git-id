// Package corpus reads the IDs of a working tree from git.
//
// Four sources, each read where it lives:
//
//	HEAD      committed            the object database, cached per tree
//	index     staged changes       the object database (blobs named by git status)
//	worktree  unstaged, untracked  the file system
//	history   every commit on      the object database, cached per set of ref tips
//	          every ref (opt-in)
//
// Minting avoids the union of all of them: reading the file system alone misses a definition
// deleted after it was committed, and staged content that was edited again since. The
// integrity checks look at the working tree only, since they ask whether what is written now
// is consistent.
//
// Every source skips the files git treats as binary, by git's own rule: the diff attribute
// decides when it is set either way (the binary macro unsets it), and otherwise a NUL in the
// first 8,000 bytes makes a file binary.
package corpus

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/git"
	"github.com/creasty/git-id/internal/ids"
)

// Corpus reads one working tree. Nothing is read until a method needs it, and what is read
// is kept for the Corpus's lifetime: one Corpus serves one command.
type Corpus struct {
	cfg  *config.Config
	repo *git.Repo

	status *git.Status
	head   map[string]headFile
	dirs   *[2]string
	fp     string
}

// New prepares to read the working tree at root. It runs nothing.
func New(root string, cfg *config.Config) *Corpus {
	return &Corpus{cfg: cfg, repo: &git.Repo{Root: root}}
}

// load reads the status and the HEAD layer once.
func (c *Corpus) load() error {
	if c.status != nil {
		return nil
	}
	st, err := c.repo.Status()
	if err != nil {
		return err
	}
	head, err := c.headLayer(st.Head)
	if err != nil {
		return err
	}
	c.status, c.head = st, head
	return nil
}

func (c *Corpus) gitDirs() (gitDir, commonDir string, err error) {
	if c.dirs == nil {
		g, cd, err := c.repo.Dirs()
		if err != nil {
			return "", "", err
		}
		c.dirs = &[2]string{g, cd}
	}
	return c.dirs[0], c.dirs[1], nil
}

// Taken is every body written in HEAD, the index or the working tree, and with history on,
// in any commit reachable from a ref. Placeholders take nothing: they are not IDs yet.
func (c *Corpus) Taken() (map[string]struct{}, error) {
	if err := c.load(); err != nil {
		return nil, err
	}
	taken := map[string]struct{}{}
	add := func(occs []ids.Occurrence) {
		for _, o := range occs {
			if o.Body != "" {
				taken[o.Body] = struct{}{}
			}
		}
	}
	for _, f := range c.head {
		add(f.Occurrences)
	}

	staged := map[string][]string{} // blob → paths
	for _, e := range c.status.Entries {
		if !c.cfg.Scanned(e.Path) {
			continue
		}
		if e.Unmerged() {
			for _, oid := range e.Stages {
				staged[oid] = append(staged[oid], e.Path)
			}
		} else if e.X != '.' && e.IndexBlob != "" {
			staged[e.IndexBlob] = append(staged[e.IndexBlob], e.Path)
		}
	}
	if err := c.scanBlobs(staged, []string{"--cached"}, func(_, _ string, occs []ids.Occurrence) {
		add(occs)
	}); err != nil {
		return nil, err
	}

	disk, err := c.scanDisk()
	if err != nil {
		return nil, err
	}
	for _, occs := range disk {
		add(occs)
	}

	if c.cfg.History {
		bodies, err := c.historyBodies()
		if err != nil {
			return nil, err
		}
		for b := range bodies {
			taken[b] = struct{}{}
		}
	}
	return taken, nil
}

// Worktree is the occurrences of every scanned file as it is in the working tree, in path
// order. Files without any occurrence are left out.
func (c *Corpus) Worktree() ([]ids.File, error) {
	if err := c.load(); err != nil {
		return nil, err
	}
	view := make(map[string][]ids.Occurrence, len(c.head))
	for path, f := range c.head {
		view[path] = f.Occurrences
	}

	// Staged content replaces HEAD's, unless the working tree changed the file again.
	fromIndex := map[string][]string{} // blob → paths
	for _, e := range c.status.Entries {
		if !c.cfg.Scanned(e.Path) || e.Unmerged() || e.Y != '.' || e.X == '.' {
			continue
		}
		delete(view, e.Path)
		if e.IndexBlob != "" {
			fromIndex[e.IndexBlob] = append(fromIndex[e.IndexBlob], e.Path)
		}
	}
	if err := c.scanBlobs(fromIndex, []string{"--cached"}, func(_, path string, occs []ids.Occurrence) {
		view[path] = occs
	}); err != nil {
		return nil, err
	}

	for _, e := range c.status.Entries {
		if c.cfg.Scanned(e.Path) && e.Y == 'D' {
			delete(view, e.Path)
		}
	}
	disk, err := c.scanDisk()
	if err != nil {
		return nil, err
	}
	for path, occs := range disk {
		view[path] = occs
	}

	files := make([]ids.File, 0, len(view))
	for _, path := range keys(view) {
		if len(view[path]) > 0 {
			files = append(files, ids.File{Path: path, Occurrences: view[path]})
		}
	}
	return files, nil
}

// scanBlobs reads blobs from the object database and calls fn for each path of each one
// that git treats as text there. from says where the attributes come from (see
// git.Repo.DiffAttrs). A blob is scanned once however many paths share it.
func (c *Corpus) scanBlobs(paths map[string][]string, from []string, fn func(oid, path string, occs []ids.Occurrence)) error {
	var all []string
	for _, ps := range paths {
		all = append(all, ps...)
	}
	attrs, err := c.repo.DiffAttrs(all, from...)
	if err != nil {
		return err
	}
	var oids []string
	for _, oid := range keys(paths) {
		for _, p := range paths[oid] {
			if attrs[p] != "unset" {
				oids = append(oids, oid)
				break
			}
		}
	}
	return c.repo.ReadBlobs(oids, func(oid string, data []byte) error {
		var occs []ids.Occurrence
		scanned := false
		for _, p := range paths[oid] {
			if !textual(attrs[p], data) {
				continue
			}
			if !scanned {
				occs, scanned = c.cfg.Syntax.Scan(data), true
			}
			fn(oid, p, occs)
		}
		return nil
	})
}

// scanDisk reads the scanned files whose working tree content differs from the index:
// changed, conflicting and untracked files. Deleted ones are not on disk.
func (c *Corpus) scanDisk() (map[string][]ids.Occurrence, error) {
	var paths []string
	for _, e := range c.status.Entries {
		if c.cfg.Scanned(e.Path) && (e.Unmerged() || e.Y != '.' && e.Y != 'D') {
			paths = append(paths, e.Path)
		}
	}
	for _, p := range c.status.Untracked {
		if c.cfg.Scanned(p) {
			paths = append(paths, p)
		}
	}
	attrs, err := c.repo.DiffAttrs(paths)
	if err != nil {
		return nil, err
	}
	out := make(map[string][]ids.Occurrence, len(paths))
	for _, p := range paths {
		data, err := c.readText(p, attrs[p])
		if err != nil {
			return nil, err
		}
		out[p] = c.cfg.Syntax.Scan(data) // nil for a binary, vanished or non-regular file
	}
	return out, nil
}

// readText reads a working tree file if git would treat it as text. Only the first bytes are
// read to tell, so a large binary file costs little. A file that vanished or is not a
// regular file (a symlink) reads as nothing.
func (c *Corpus) readText(path, attr string) ([]byte, error) {
	if attr == "unset" {
		return nil, nil
	}
	full := filepath.Join(c.repo.Root, filepath.FromSlash(path))
	info, err := os.Lstat(full)
	if errors.Is(err, fs.ErrNotExist) || err == nil && !info.Mode().IsRegular() {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(f, head)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
		return nil, err
	}
	if !textual(attr, head[:n]) {
		return nil, nil
	}
	rest, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return append(head[:n], rest...), nil
}

// sniffLen is how much of a file git looks at for a NUL when deciding it is binary.
const sniffLen = 8000

// textual applies git's rule for binary files to a file's diff attribute and content.
func textual(attr string, data []byte) bool {
	switch attr {
	case "unset":
		return false
	case "set":
		return true
	}
	return bytes.IndexByte(data[:min(len(data), sniffLen)], 0) < 0
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
