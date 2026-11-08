package hook

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
)

// Codex handles a Codex PreToolUse event for apply_patch. The whole patch is in
// tool_input.command; only its added lines are resolved, since context and removed lines must
// match the file as it is or the patch no longer applies.
//
// Codex accepts updatedInput only together with permissionDecision "allow". That allow lets
// the rewritten input through; approval and the sandbox still decide afterwards as usual.
func Codex(stdin io.Reader, stdout io.Writer) error {
	e, err := readEvent(stdin)
	if err != nil {
		return err
	}
	if e.HookEventName != "PreToolUse" || e.ToolName != "apply_patch" || e.ToolInput == nil {
		return nil
	}
	patch, ok := e.ToolInput["command"].(string)
	if !ok || e.Cwd == "" {
		return nil
	}
	r, err := findRepo(e.Cwd)
	if err != nil || r == nil {
		return err
	}
	abs := func(file string) string {
		if !filepath.IsAbs(file) {
			file = filepath.Join(e.Cwd, file)
		}
		return file
	}
	texts := map[string][]string{} // file → its added lines
	current := ""
	if _, err := resolvePatch(patch, func(file string) bool {
		current = abs(file)
		return true
	}, func(line []byte) ([]byte, error) {
		texts[current] = append(texts[current], string(line))
		return line, nil
	}); err != nil {
		return err
	}
	targets, err := r.targets(texts)
	if err != nil || len(targets) == 0 {
		return err
	}

	res, sess := r.resolver(e.SessionID)
	resolved, err := resolvePatch(patch, func(file string) bool { return targets[abs(file)] },
		func(line []byte) ([]byte, error) { return res.Resolve(line, "") })
	if saveErr := sess.save(res.SlotsChanged); err == nil {
		err = saveErr
	}
	if err != nil {
		return err
	}
	if reason := denial(res); reason != "" {
		return writeOutput(stdout, &output{HookEventName: "PreToolUse", PermissionDecision: "deny", PermissionDecisionReason: reason})
	}
	if resolved == patch {
		return nil
	}
	input := make(map[string]any, len(e.ToolInput))
	for k, v := range e.ToolInput {
		input[k] = v
	}
	input["command"] = resolved
	return writeOutput(stdout, &output{HookEventName: "PreToolUse", PermissionDecision: "allow", UpdatedInput: input, AdditionalContext: note(res)})
}

// patchFileHeaders name the file the following lines belong to.
var patchFileHeaders = []string{"*** Add File: ", "*** Update File: ", "*** Move to: ", "*** Delete File: "}

// resolvePatch resolves the added lines of the files scanned accepts. An ID never spans
// lines, so resolving line by line is exact, and one resolver keeps slots consistent across
// the whole patch.
func resolvePatch(patch string, scanned func(string) bool, resolve func([]byte) ([]byte, error)) (string, error) {
	var out bytes.Buffer
	include := false
	for _, line := range bytes.SplitAfter([]byte(patch), []byte("\n")) {
		for _, h := range patchFileHeaders {
			if name, ok := strings.CutPrefix(string(line), h); ok {
				include = scanned(strings.TrimRight(name, "\r\n"))
			}
		}
		if include && len(line) > 0 && line[0] == '+' {
			r, err := resolve(line[1:])
			if err != nil {
				return "", err
			}
			out.WriteByte('+')
			out.Write(r)
			continue
		}
		out.Write(line)
	}
	return out.String(), nil
}
