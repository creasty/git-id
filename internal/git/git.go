// Package git runs the git plumbing git id reads repositories with. It knows nothing about IDs.
package git

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// Repo is a working tree.
type Repo struct {
	Root string // absolute path of the working tree's top directory
}

// FindRoot returns the top of the working tree containing dir. It walks up to the nearest
// .git, which costs no process; only when there is none (GIT_DIR and friends) it asks git.
func FindRoot(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for d := abs; ; d = filepath.Dir(d) {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d, nil
		}
		if filepath.Dir(d) == d {
			break
		}
	}
	out, err := (&Repo{Root: abs}).run(nil, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("not in a git working tree: %s", abs)
	}
	return strings.TrimSpace(string(out)), nil
}

// command runs git in the working tree. GIT_OPTIONAL_LOCKS=0 keeps status from refreshing
// the index, so concurrent hooks and editors never fight over index.lock.
func (r *Repo) command(args ...string) *exec.Cmd {
	cmd := exec.Command("git", append([]string{"-C", r.Root}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_OPTIONAL_LOCKS=0")
	return cmd
}

func (r *Repo) run(stdin []byte, args ...string) ([]byte, error) {
	cmd := r.command(args...)
	if stdin != nil {
		cmd.Stdin = bytes.NewReader(stdin)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}

// Dirs returns the working tree's own git directory and the one all its worktrees share.
func (r *Repo) Dirs() (gitDir, commonDir string, err error) {
	out, err := r.run(nil, "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) != 2 {
		return "", "", fmt.Errorf("git rev-parse: unexpected output %q", out)
	}
	return lines[0], lines[1], nil
}

// Entry is one changed or unmerged path in the status.
type Entry struct {
	Path string
	// X and Y are the index and working tree states, '.' for unchanged.
	X, Y byte
	// IndexBlob is the staged content; empty when the path is not a regular file in the index.
	IndexBlob string
	// Stages are the conflicting blobs of an unmerged path.
	Stages []string
	// Symlink reports a symbolic link in the working tree.
	Symlink bool
}

// Unmerged reports a conflict.
func (e Entry) Unmerged() bool { return e.Stages != nil }

// Status is what changed relative to HEAD.
type Status struct {
	Head      string // commit; empty before the first one
	Entries   []Entry
	Untracked []string
}

// Status reads `git status`. Renames are off, so a rename is a deletion and an addition, and
// submodules are skipped: their content is another repository.
func (r *Repo) Status() (*Status, error) {
	out, err := r.run(nil, "status", "--porcelain=v2", "-z", "--branch", "--untracked-files=all", "--no-renames", "--ignore-submodules=all")
	if err != nil {
		return nil, err
	}
	st := &Status{}
	records := strings.Split(string(out), "\x00")
	for i := 0; i < len(records); i++ {
		rec := records[i]
		switch {
		case strings.HasPrefix(rec, "# branch.oid "):
			if oid := strings.TrimPrefix(rec, "# branch.oid "); oid != "(initial)" {
				st.Head = oid
			}
		case strings.HasPrefix(rec, "1 "), strings.HasPrefix(rec, "2 "):
			n := 9
			if rec[0] == '2' {
				n, i = 10, i+1 // a rename's original path is the next record
			}
			f := strings.SplitN(rec, " ", n)
			if len(f) != n {
				return nil, fmt.Errorf("git status: malformed record %q", rec)
			}
			e := Entry{Path: f[n-1], X: f[1][0], Y: f[1][1], Symlink: f[5] == "120000"}
			if regular(f[4]) {
				e.IndexBlob = f[7]
			}
			st.Entries = append(st.Entries, e)
		case strings.HasPrefix(rec, "u "):
			f := strings.SplitN(rec, " ", 11)
			if len(f) != 11 {
				return nil, fmt.Errorf("git status: malformed record %q", rec)
			}
			e := Entry{Path: f[10], X: f[1][0], Y: f[1][1], Stages: []string{}, Symlink: f[6] == "120000"}
			for k := 0; k < 3; k++ {
				if regular(f[3+k]) {
					e.Stages = append(e.Stages, f[7+k])
				}
			}
			st.Entries = append(st.Entries, e)
		case strings.HasPrefix(rec, "? "):
			st.Untracked = append(st.Untracked, rec[2:])
		}
	}
	return st, nil
}

// regular reports whether a mode is a regular file: symlinks and submodules are not text.
func regular(mode string) bool { return mode == "100644" || mode == "100755" }

// Tree returns the tree of a commit.
func (r *Repo) Tree(commit string) (string, error) {
	out, err := r.run(nil, "rev-parse", "--verify", "--quiet", commit+"^{tree}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Blob is a regular file in a tree.
type Blob struct {
	Path string
	OID  string
}

// LsTree lists every regular file of a tree.
func (r *Repo) LsTree(tree string) ([]Blob, error) {
	out, err := r.run(nil, "ls-tree", "-r", "-z", "--full-tree", tree)
	if err != nil {
		return nil, err
	}
	var blobs []Blob
	for _, rec := range strings.Split(string(out), "\x00") {
		meta, path, ok := strings.Cut(rec, "\t")
		if !ok {
			continue
		}
		f := strings.Fields(meta) // mode type oid
		if len(f) == 3 && f[1] == "blob" && regular(f[0]) {
			blobs = append(blobs, Blob{Path: path, OID: f[2]})
		}
	}
	return blobs, nil
}

// Change is a path that differs between two trees. OID is its new content, or empty when it
// is gone or is no longer a regular file.
type Change struct {
	Path string
	OID  string
}

// DiffTree lists what changed from one tree to another. git skips every subtree whose hash
// is unchanged, so the cost follows the size of the change, not of the tree.
func (r *Repo) DiffTree(from, to string) ([]Change, error) {
	out, err := r.run(nil, "diff-tree", "-r", "-z", "--no-renames", "--no-commit-id", from, to)
	if err != nil {
		return nil, err
	}
	var changes []Change
	records := strings.Split(string(out), "\x00")
	for i := 0; i+1 < len(records); i += 2 {
		f := strings.Fields(strings.TrimPrefix(records[i], ":")) // srcmode dstmode srcoid dstoid status
		if len(f) != 5 {
			return nil, fmt.Errorf("git diff-tree: malformed record %q", records[i])
		}
		c := Change{Path: records[i+1]}
		if f[4] != "D" && regular(f[1]) {
			c.OID = f[3]
		}
		changes = append(changes, c)
	}
	return changes, nil
}

// ReadBlobs streams the content of each object through one `git cat-file --batch`. Missing
// objects are skipped.
func (r *Repo) ReadBlobs(oids []string, fn func(oid string, data []byte) error) error {
	if len(oids) == 0 {
		return nil
	}
	cmd := r.command("cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		w := bufio.NewWriter(stdin)
		for _, oid := range oids {
			w.WriteString(oid)
			w.WriteByte('\n')
		}
		w.Flush()
		stdin.Close()
	}()
	readErr := readBatch(bufio.NewReaderSize(stdout, 1<<16), fn)
	if readErr != nil {
		io.Copy(io.Discard, stdout) // let git finish writing before waiting on it
	}
	if err := cmd.Wait(); err != nil && readErr == nil {
		return fmt.Errorf("git cat-file --batch: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return readErr
}

func readBatch(br *bufio.Reader, fn func(oid string, data []byte) error) error {
	for {
		header, err := br.ReadString('\n')
		if errors.Is(err, io.EOF) && header == "" {
			return nil
		}
		if err != nil {
			return err
		}
		f := strings.Fields(header) // oid type size, or name missing
		if len(f) == 2 && f[1] == "missing" {
			continue
		}
		if len(f) != 3 {
			return fmt.Errorf("git cat-file: unexpected header %q", header)
		}
		size, err := strconv.Atoi(f[2])
		if err != nil {
			return fmt.Errorf("git cat-file: unexpected header %q", header)
		}
		data := make([]byte, size+1) // content and the trailing newline
		if _, err := io.ReadFull(br, data); err != nil {
			return err
		}
		if err := fn(f[0], data[:size]); err != nil {
			return err
		}
	}
}

// DiffAttrs returns the diff attribute of each path, which decides whether git treats a file
// as binary: "unset" (also set by the binary macro), "set", a driver name, or "unspecified".
// from selects where attributes are read: nil for the working tree, "--cached" for the index,
// "--source=<tree>" for a tree.
func (r *Repo) DiffAttrs(paths []string, from ...string) (map[string]string, error) {
	attrs := make(map[string]string, len(paths))
	if len(paths) == 0 {
		return attrs, nil
	}
	args := append(append([]string{"check-attr", "-z", "--stdin"}, from...), "diff")
	out, err := r.run([]byte(strings.Join(paths, "\x00")+"\x00"), args...)
	if err != nil {
		return nil, err
	}
	f := strings.Split(string(out), "\x00") // path, attribute, value, ...
	for i := 0; i+2 < len(f); i += 3 {
		attrs[f[i]] = f[i+2]
	}
	return attrs, nil
}

// Existing returns the subset of oids present in the object database.
func (r *Repo) Existing(oids []string) ([]string, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	out, err := r.run([]byte(strings.Join(oids, "\n")+"\n"), "cat-file", "--batch-check=%(objectname)")
	if err != nil {
		return nil, err
	}
	var found []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" && !strings.HasSuffix(line, " missing") {
			found = append(found, line)
		}
	}
	return found, nil
}

// Tips returns what every ref and HEAD point at, sorted and without duplicates.
func (r *Repo) Tips() ([]string, error) {
	out, err := r.run(nil, "for-each-ref", "--format=%(objectname)")
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, line := range strings.Fields(string(out)) {
		set[line] = true
	}
	if head, err := r.run(nil, "rev-parse", "--verify", "--quiet", "HEAD"); err == nil {
		set[strings.TrimSpace(string(head))] = true
	}
	tips := make([]string, 0, len(set))
	for t := range set {
		tips = append(tips, t)
	}
	sort.Strings(tips)
	return tips, nil
}

// HistoryBlobs lists the regular-file blobs reachable from tips but not from seen, each with
// the first path git found it at. Objects reachable from seen can still be listed when they
// are not in the trees of its commits; listing extra is harmless, missing one is not.
func (r *Repo) HistoryBlobs(tips, seen []string) ([]Blob, error) {
	if len(tips) == 0 {
		return nil, nil
	}
	var in bytes.Buffer
	for _, t := range tips {
		in.WriteString(t + "\n")
	}
	for _, s := range seen {
		in.WriteString("^" + s + "\n")
	}
	out, err := r.run(in.Bytes(), "rev-list", "--objects", "--filter=object:type=blob", "--stdin")
	if err != nil {
		return nil, err
	}
	var blobs []Blob
	for _, line := range strings.Split(string(out), "\n") {
		oid, path, ok := strings.Cut(line, " ")
		if ok && path != "" { // commits come without a path
			blobs = append(blobs, Blob{Path: path, OID: oid})
		}
	}
	return blobs, nil
}
