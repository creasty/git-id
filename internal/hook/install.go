package hook

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

// Agents are the agents a hook can be installed for, in the order they are installed.
var Agents = []string{"claude", "codex"}

// settings is where each agent reads its project hooks, and which tools the hook watches.
var settings = map[string]struct{ file, matcher string }{
	"claude": {filepath.Join(".claude", "settings.json"), "Edit|Write|MultiEdit|NotebookEdit"},
	"codex":  {filepath.Join(".codex", "hooks.json"), "apply_patch"},
}

// Install adds an agent's git id hook to its project settings in the working tree at root,
// and returns the file, relative to root, and whether it changed. Everything else in the file
// is kept in its order; a file that already runs the hook is left as it is.
func Install(root, agent string) (file string, changed bool, err error) {
	s, ok := settings[agent]
	if !ok {
		return "", false, fmt.Errorf("unknown agent %q: claude or codex", agent)
	}
	command := "git id hook run " + agent
	path := filepath.Join(root, s.file)

	doc, mode := newObject(), os.FileMode(0o644)
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if doc, err = parseJSON(data); err != nil {
			return s.file, false, fmt.Errorf("%s: %w", s.file, err)
		}
		if !doc.object {
			return s.file, false, fmt.Errorf("%s: not a JSON object", s.file)
		}
		if info, err := os.Stat(path); err == nil {
			mode = info.Mode().Perm()
		}
	case !errors.Is(err, fs.ErrNotExist):
		return s.file, false, err
	}

	hooks, err := doc.child("hooks", false)
	if err != nil {
		return s.file, false, fmt.Errorf("%s: %w", s.file, err)
	}
	pre, err := hooks.child("PreToolUse", true)
	if err != nil {
		return s.file, false, fmt.Errorf("%s: %w", s.file, err)
	}
	if runs(pre, command) {
		return s.file, false, nil
	}
	hook := newObject()
	hook.set("type", &jsonNode{value: "command"})
	hook.set("command", &jsonNode{value: command})
	hook.set("timeout", &jsonNode{value: json.Number("30")})
	entry := newObject()
	entry.set("matcher", &jsonNode{value: s.matcher})
	entry.set("hooks", &jsonNode{array: true, items: []*jsonNode{hook}})
	pre.items = append(pre.items, entry)

	var b bytes.Buffer
	if err := doc.encode(&b, ""); err != nil {
		return s.file, false, err
	}
	b.WriteByte('\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return s.file, false, err
	}
	return s.file, true, os.WriteFile(path, b.Bytes(), mode)
}

// runs reports whether a PreToolUse list already has a hook running command, whatever its
// matcher.
func runs(pre *jsonNode, command string) bool {
	for _, entry := range pre.items {
		if !entry.object {
			continue
		}
		hooks, ok := entry.fields["hooks"]
		if !ok || !hooks.array {
			continue
		}
		for _, h := range hooks.items {
			if h.object && h.fields["command"] != nil && h.fields["command"].value == command {
				return true
			}
		}
	}
	return false
}

// jsonNode is a JSON value that keeps the order of object keys, so that adding one entry to a
// settings file changes nothing else in it.
type jsonNode struct {
	object, array bool
	keys          []string
	fields        map[string]*jsonNode
	items         []*jsonNode
	value         any // a scalar: string, json.Number, bool or nil
}

func newObject() *jsonNode { return &jsonNode{object: true, fields: map[string]*jsonNode{}} }

func (n *jsonNode) set(key string, v *jsonNode) {
	if _, ok := n.fields[key]; !ok {
		n.keys = append(n.keys, key)
	}
	n.fields[key] = v
}

// child returns the object (or array) under key, adding an empty one when it is missing.
func (n *jsonNode) child(key string, array bool) (*jsonNode, error) {
	c, ok := n.fields[key]
	if !ok {
		c = newObject()
		if array {
			c = &jsonNode{array: true}
		}
		n.set(key, c)
	}
	if array && !c.array || !array && !c.object {
		return nil, fmt.Errorf("%q has an unexpected type", key)
	}
	return c, nil
}

func parseJSON(data []byte) (*jsonNode, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber() // numbers are written back exactly as they were
	n, err := decodeNode(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("unexpected data after the JSON value")
	}
	return n, nil
}

func decodeNode(dec *json.Decoder) (*jsonNode, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return &jsonNode{value: tok}, nil
	}
	n := &jsonNode{array: delim == '['}
	if delim == '{' {
		n = newObject()
	}
	for dec.More() {
		if n.object {
			k, err := dec.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeNode(dec)
			if err != nil {
				return nil, err
			}
			n.set(k.(string), v)
		} else {
			v, err := decodeNode(dec)
			if err != nil {
				return nil, err
			}
			n.items = append(n.items, v)
		}
	}
	_, err = dec.Token() // the closing delimiter
	return n, err
}

// encode writes n the way JSON.stringify(value, null, 2) does, which is how Claude Code
// writes its settings: two spaces per level, one member per line, empty ones as {} and [].
func (n *jsonNode) encode(b *bytes.Buffer, indent string) error {
	open, close, count := "{", "}", len(n.keys)
	switch {
	case n.array:
		open, close, count = "[", "]", len(n.items)
	case !n.object:
		return writeScalar(b, n.value)
	}
	if count == 0 {
		b.WriteString(open + close)
		return nil
	}
	b.WriteString(open + "\n")
	for i := 0; i < count; i++ {
		b.WriteString(indent + "  ")
		var member *jsonNode
		if n.object {
			if err := writeScalar(b, n.keys[i]); err != nil {
				return err
			}
			b.WriteString(": ")
			member = n.fields[n.keys[i]]
		} else {
			member = n.items[i]
		}
		if err := member.encode(b, indent+"  "); err != nil {
			return err
		}
		if i < count-1 {
			b.WriteByte(',')
		}
		b.WriteByte('\n')
	}
	b.WriteString(indent + close)
	return nil
}

// writeScalar writes a string, number, bool or null, leaving characters such as & and <
// as they are.
func writeScalar(b *bytes.Buffer, v any) error {
	var t bytes.Buffer
	enc := json.NewEncoder(&t)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	b.Write(bytes.TrimSuffix(t.Bytes(), []byte("\n")))
	return nil
}
