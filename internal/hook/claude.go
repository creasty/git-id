package hook

import (
	"io"
	"path/filepath"
)

// claudeFields are the strings each Claude Code edit tool writes. MultiEdit writes the
// new_string of each of its edits.
var claudeFields = map[string]string{
	"Write":        "content",
	"Edit":         "new_string",
	"MultiEdit":    "new_string",
	"NotebookEdit": "new_source",
}

// Claude handles a Claude Code PreToolUse event (matcher: Edit|Write|MultiEdit|NotebookEdit).
//
// A rewrite is returned as updatedInput without a permissionDecision, so the edit still goes
// through the user's usual permission flow.
func Claude(stdin io.Reader, stdout io.Writer) error {
	e, err := readEvent(stdin)
	if err != nil {
		return err
	}
	field, ok := claudeFields[e.ToolName]
	if e.HookEventName != "PreToolUse" || !ok || e.ToolInput == nil {
		return nil
	}
	path, _ := e.ToolInput["file_path"].(string)
	if path == "" {
		path, _ = e.ToolInput["notebook_path"].(string)
	}
	if path == "" {
		return nil
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(e.Cwd, path)
	}
	r, err := findRepo(path)
	if err != nil || r == nil {
		return err
	}
	var texts []string
	if _, _, err := rewriteClaude(e.ToolName, field, e.ToolInput, func(s string) (string, error) {
		texts = append(texts, s)
		return s, nil
	}); err != nil {
		return err
	}
	targets, err := r.targets(map[string][]string{path: texts})
	if err != nil || !targets[path] {
		return err
	}

	res, sess := r.resolver(e.SessionID)
	input, changed, err := rewriteClaude(e.ToolName, field, e.ToolInput, func(s string) (string, error) {
		out, err := res.Resolve([]byte(s), "")
		return string(out), err
	})
	if saveErr := sess.save(res.SlotsChanged); err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if reason := denial(res); reason != "" {
		return writeOutput(stdout, &output{HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason})
	}
	if !changed {
		return nil
	}
	return writeOutput(stdout, &output{HookEventName: "PreToolUse", UpdatedInput: input, AdditionalContext: note(res)})
}

// rewriteClaude applies resolve to the strings the tool writes, leaving every other field as
// it came.
func rewriteClaude(tool, field string, in map[string]any, resolve func(string) (string, error)) (map[string]any, bool, error) {
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	changed := false
	apply := func(m map[string]any) error {
		s, ok := m[field].(string)
		if !ok {
			return nil
		}
		r, err := resolve(s)
		if err != nil {
			return err
		}
		changed = changed || r != s
		m[field] = r
		return nil
	}
	if tool != "MultiEdit" {
		return out, changed, apply(out)
	}
	edits, ok := in["edits"].([]any)
	if !ok {
		return out, false, nil
	}
	copied := make([]any, len(edits))
	for i, e := range edits {
		m, ok := e.(map[string]any)
		if !ok {
			copied[i] = e
			continue
		}
		c := make(map[string]any, len(m))
		for k, v := range m {
			c[k] = v
		}
		if err := apply(c); err != nil {
			return nil, false, err
		}
		copied[i] = c
	}
	out["edits"] = copied
	return out, changed, nil
}
