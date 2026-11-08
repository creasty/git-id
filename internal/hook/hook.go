// Package hook resolves an agent's ID placeholders before an edit is written.
//
// The agent writes [X-100000] (a new definition) or [X-100001] / X-100001 (a slot: the same
// number is the same ID across the session). The hook mints the IDs, rewrites the tool
// input, and tells the agent what each placeholder became; without that, the agent would
// keep using the placeholder in conversation. When the edit breaks the notation (a fresh
// placeholder used as a reference, a human placeholder, a hand-written definition), the hook
// denies it with the reason instead.
//
// Every edit passes through the hook, and almost none contain a placeholder, so nothing is
// read from git and no record is opened until a placeholder needs it.
package hook

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/creasty/git-id/internal/config"
	"github.com/creasty/git-id/internal/git"
	"github.com/creasty/git-id/internal/ids"
)

// event is the part of a PreToolUse event the hooks read.
type event struct {
	HookEventName string         `json:"hook_event_name"`
	ToolName      string         `json:"tool_name"`
	SessionID     string         `json:"session_id"`
	Cwd           string         `json:"cwd"`
	ToolInput     map[string]any `json:"tool_input"`
}

func readEvent(r io.Reader) (*event, error) {
	dec := json.NewDecoder(r)
	dec.UseNumber() // tool input is passed back; keep numbers exactly as they came
	var e event
	if err := dec.Decode(&e); err != nil {
		return nil, err
	}
	return &e, nil
}

// output is the hookSpecificOutput of a PreToolUse response.
type output struct {
	HookEventName            string         `json:"hookEventName"`
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
}

func writeOutput(w io.Writer, o *output) error {
	if o == nil {
		return nil
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false) // the input is passed back as written, "->" in the note too
	return enc.Encode(map[string]any{"hookSpecificOutput": o})
}

// repo is the working tree an edit lands in, with its configuration.
type repo struct {
	root string
	cfg  *config.Config
}

// findRepo returns the repository containing path, or nil when it does not use git id's
// notation: the hook may be installed for every repository, and those without a
// configuration, or without prefixes (where placeholders cannot be found), are left alone.
func findRepo(path string) (*repo, error) {
	dir := path
	for { // the file, and maybe its directories, may not exist yet
		if info, err := os.Stat(dir); err == nil && info.IsDir() {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return nil, nil
		}
		dir = parent
	}
	root, err := git.FindRoot(dir)
	if err != nil {
		return nil, nil
	}
	cfg, err := config.Load(root)
	if errors.Is(err, config.ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !cfg.Syntax.HasPrefixes() {
		return nil, nil
	}
	return &repo{root: root, cfg: cfg}, nil
}

// targets returns which of the edited files, by absolute path, the hook resolves: files the
// repository's checks read, as `git id resolve` does, since an ID minted into a file nothing
// reads would be invisible to the uniqueness it relies on. texts is the new text of each file.
//
// Whether git treats a file as binary takes a git call, so it is asked only for files whose
// text has something to resolve; the others are left out, as nothing in them would change.
func (r *repo) targets(texts map[string][]string) (map[string]bool, error) {
	pending := map[string]string{} // relative path → absolute path
	for abs, ts := range texts {
		rel, err := filepath.Rel(r.root, abs)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if rel = filepath.ToSlash(rel); r.cfg.Scanned(rel) && hasWork(r.cfg.Syntax, ts) {
			pending[rel] = abs
		}
	}
	out := map[string]bool{}
	if len(pending) == 0 {
		return out, nil
	}
	rels := make([]string, 0, len(pending))
	for rel := range pending {
		rels = append(rels, rel)
	}
	attrs, err := (&git.Repo{Root: r.root}).DiffAttrs(rels)
	if err != nil {
		return nil, err
	}
	for rel, abs := range pending {
		if attrs[rel] != "unset" {
			out[abs] = true
		}
	}
	return out, nil
}

// hasWork reports whether a resolver would replace or reject anything in texts.
func hasWork(s *ids.Syntax, texts []string) bool {
	for _, t := range texts {
		for _, o := range s.Scan([]byte(t)) {
			if s.Actionable(o) {
				return true
			}
		}
	}
	return false
}

// resolver returns a resolver for the agent's placeholders and the session that records its
// slots. Call session.save when done, whatever happened.
func (r *repo) resolver(sessionID string) (*ids.Resolver, *session) {
	s := &session{path: slotPath(r.root, sessionID), repo: r}
	return &ids.Resolver{Syntax: r.cfg.Syntax, Scope: ids.Agent, Mint: s.mint, LoadSlots: s.loadSlots}, s
}

// denial is the reason given to the agent when something cannot be resolved.
func denial(r *ids.Resolver) string {
	if len(r.Rejected) == 0 {
		return ""
	}
	parts := make([]string, len(r.Rejected))
	for i, rej := range r.Rejected {
		parts[i] = rej.Occurrence.Token() + " is " + rej.Reason.Description()
	}
	return "git id: ID notation violated: " + strings.Join(parts, "; ") + ". " + ids.Guide
}

// note tells the agent what each placeholder became.
func note(r *ids.Resolver) string {
	parts := make([]string, len(r.Replaced))
	for i, rep := range r.Replaced {
		parts[i] = rep.Occurrence.Token() + " -> " + rep.Token()
	}
	return "git id resolved ID placeholders before writing, so the file has the real IDs: " + strings.Join(parts, "; ")
}
