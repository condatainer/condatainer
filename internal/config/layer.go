package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/condatainer/condatainer/internal/utils"
	"go.yaml.in/yaml/v3"
)

// Layer is one config file. Keys are dotted, case-insensitive, and may be written nested or flat.
type Layer struct {
	Path string
	Type string // "user", "extra-root", "app-root"
	root *yaml.Node
}

// ReadLayer reads the config file at path as a layer of the given type.
func ReadLayer(path, typ string) (*Layer, error) { return readLayer(path, typ) }

// Line returns the line of key's value in the file, or 0 when the key is not set.
func (l *Layer) Line(key string) int {
	if n := lookup(l.root, splitKey(key)); n != nil {
		return n.Line
	}
	return 0
}

// readLayer parses the file at path. An empty file is an empty layer.
func readLayer(path, typ string) (*Layer, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	_, root, err := parseRoot(data)
	if err != nil {
		return nil, fmt.Errorf("error reading config %s: %w", path, err)
	}
	return &Layer{Path: path, Type: typ, root: root}, nil
}

// parseRoot returns the document node and its top-level mapping, creating both for an empty file.
func parseRoot(data []byte) (doc, root *yaml.Node, err error) {
	doc = &yaml.Node{}
	if err := yaml.Unmarshal(data, doc); err != nil {
		return nil, nil, err
	}
	newMap := func() *yaml.Node { return &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"} }
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		root = newMap()
		return &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}, root, nil
	}
	root = deref(doc.Content[0])
	if root.Kind == yaml.ScalarNode && root.Tag == "!!null" {
		root = newMap()
		doc.Content[0] = root
	}
	if root.Kind != yaml.MappingNode {
		return nil, nil, errors.New("top level is not a mapping")
	}
	return doc, root, nil
}

func deref(n *yaml.Node) *yaml.Node {
	for n != nil && n.Kind == yaml.AliasNode {
		n = n.Alias
	}
	return n
}

// childIndex returns the index in m.Content of the value stored under name, or -1.
func childIndex(m *yaml.Node, name string) int {
	if m == nil || m.Kind != yaml.MappingNode {
		return -1
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if strings.EqualFold(m.Content[i].Value, name) {
			return i + 1
		}
	}
	return -1
}

// lookup finds the value of a dotted key, trying the longest flat prefix first.
func lookup(m *yaml.Node, parts []string) *yaml.Node {
	m = deref(m)
	for i := len(parts); i >= 1; i-- {
		idx := childIndex(m, strings.Join(parts[:i], "."))
		if idx < 0 {
			continue
		}
		val := deref(m.Content[idx])
		if i == len(parts) {
			return val
		}
		if n := lookup(val, parts[i:]); n != nil {
			return n
		}
	}
	return nil
}

func splitKey(key string) []string { return strings.Split(strings.ToLower(key), ".") }

// value returns the decoded value of key. A missing key and a null value are both unset.
func (l *Layer) value(key string) (any, bool) {
	n := lookup(l.root, splitKey(key))
	if n == nil {
		return nil, false
	}
	var v any
	if err := n.Decode(&v); err != nil || v == nil {
		return nil, false
	}
	return v, true
}

// Name returns the layer's type, for reports.
func (l *Layer) Name() string { return l.Type }

// Text returns a scalar key as text. A list or an unset key is false.
func (l *Layer) Text(key string) (string, bool) {
	v, ok := l.value(key)
	switch v.(type) {
	case []any, map[string]any:
		return "", false
	}
	return toString(v), ok
}

// List returns a list key. A plain string splits on whitespace.
func (l *Layer) List(key string) ([]string, bool) {
	v, ok := l.value(key)
	if !ok {
		return nil, false
	}
	return toStringSlice(v), true
}

// raw returns the decoded value of key as YAML produced it.
func (l *Layer) raw(key string) any { v, _ := l.value(key); return v }

// InConfig reports whether key is explicitly set in this file.
func (l *Layer) InConfig(key string) bool { _, ok := l.value(key); return ok }

// GetString returns key as a string, "" when unset.
func (l *Layer) GetString(key string) string { v, _ := l.value(key); return toString(v) }

// GetBool returns key as a bool, false when unset or not a boolean.
func (l *Layer) GetBool(key string) bool { v, _ := l.value(key); return toBool(v) }

// GetInt returns key as an int, 0 when unset or not a number.
func (l *Layer) GetInt(key string) int { v, _ := l.value(key); return toInt(v) }

// GetStringSlice returns key as a list. A plain string splits on whitespace.
func (l *Layer) GetStringSlice(key string) []string { v, _ := l.value(key); return toStringSlice(v) }

// Keys returns every dotted key set in the file, flattened to the leaves.
func (l *Layer) Keys() []string {
	var out []string
	var walk func(m *yaml.Node, prefix string)
	walk = func(m *yaml.Node, prefix string) {
		for i := 0; i+1 < len(m.Content); i += 2 {
			name := prefix + strings.ToLower(m.Content[i].Value)
			if v := deref(m.Content[i+1]); v.Kind == yaml.MappingNode && len(v.Content) > 0 {
				walk(v, name+".")
			} else {
				out = append(out, name)
			}
		}
	}
	walk(deref(l.root), "")
	return out
}

func toString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case time.Time:
		return x.String()
	}
	return ""
}

func toBool(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case string:
		b, _ := strconv.ParseBool(x)
		return b
	case int:
		return x != 0
	case int64:
		return x != 0
	case uint64:
		return x != 0
	case float64:
		return x != 0
	}
	return false
}

func toInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case uint64:
		return int(x)
	case float64:
		return int(x)
	case bool:
		if x {
			return 1
		}
	case string:
		n, _ := strconv.ParseInt(strings.TrimSpace(x), 0, 0)
		return int(n)
	}
	return 0
}

func toStringSlice(v any) []string {
	switch x := v.(type) {
	case []any:
		out := make([]string, len(x))
		for i, e := range x {
			out[i] = toString(e)
		}
		return out
	case string:
		return strings.Fields(x)
	}
	return nil
}

// newValueNode encodes a Go value as a YAML node.
func newValueNode(value any) (*yaml.Node, error) {
	n := &yaml.Node{}
	if err := n.Encode(value); err != nil {
		return nil, err
	}
	return n, nil
}

// setKey stores value under a dotted key, replacing the node in place (and its trailing comment) when the key exists.
func setKey(root *yaml.Node, key string, value any) error {
	node, err := newValueNode(value)
	if err != nil {
		return err
	}
	return setKeyNode(root, key, node)
}

// setKeyNode is setKey for a value that is already a node.
func setKeyNode(root *yaml.Node, key string, node *yaml.Node) error {
	parts := splitKey(key)
	if replaceKey(root, parts, node) {
		return nil
	}
	m := root
	for _, p := range parts[:len(parts)-1] {
		idx := childIndex(m, p)
		if idx >= 0 && deref(m.Content[idx]).Kind == yaml.MappingNode {
			m = deref(m.Content[idx])
			continue
		}
		child := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		if idx >= 0 {
			m.Content[idx] = child
		} else {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: p}, child)
		}
		m = child
	}
	last := parts[len(parts)-1]
	if idx := childIndex(m, last); idx >= 0 {
		node.LineComment = m.Content[idx].LineComment
		m.Content[idx] = node
		return nil
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: last}, node)
	return nil
}

// replaceKey swaps the value of an existing key for node and reports whether the key was found.
func replaceKey(m *yaml.Node, parts []string, node *yaml.Node) bool {
	m = deref(m)
	for i := len(parts); i >= 1; i-- {
		idx := childIndex(m, strings.Join(parts[:i], "."))
		if idx < 0 {
			continue
		}
		if i == len(parts) {
			node.LineComment = m.Content[idx].LineComment
			m.Content[idx] = node
			return true
		}
		if replaceKey(m.Content[idx], parts[i:], node) {
			return true
		}
	}
	return false
}

// deleteKey removes a dotted key and any parent mapping it leaves empty. It reports whether the key was found.
func deleteKey(m *yaml.Node, parts []string) bool {
	m = deref(m)
	for i := len(parts); i >= 1; i-- {
		idx := childIndex(m, strings.Join(parts[:i], "."))
		if idx < 0 {
			continue
		}
		if i == len(parts) {
			m.Content = append(m.Content[:idx-1], m.Content[idx+1:]...)
			return true
		}
		child := deref(m.Content[idx])
		if child.Kind == yaml.MappingNode && deleteKey(child, parts[i:]) {
			if len(child.Content) == 0 {
				m.Content = append(m.Content[:idx-1], m.Content[idx+1:]...)
			}
			return true
		}
	}
	return false
}

// editLayerFile applies edit to the config file at path under an exclusive lock and writes the result back in place.
//   - A missing file is created.
//   - The write keeps the file's owner and mode, which a shared config file needs.
//   - A held lock is retried for a few seconds.
func editLayerFile(path string, edit func(root *yaml.Node) error) error {
	if err := utils.MkdirAllShared(filepath.Dir(path)); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, utils.PermFile)
	if err != nil {
		return fmt.Errorf("failed to write config to %s: %w", path, err)
	}
	lock, err := lockWithRetry(f)
	if err != nil {
		f.Close()
		return fmt.Errorf("failed to lock config %s: %w", path, err)
	}
	defer lock.Close()

	data, err := io.ReadAll(f)
	if err != nil {
		return fmt.Errorf("failed to read config %s: %w", path, err)
	}
	doc, root, err := parseRoot(data)
	if err != nil {
		return fmt.Errorf("failed to read config %s: %w", path, err)
	}
	if err := edit(root); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return fmt.Errorf("failed to encode config %s: %w", path, err)
	}
	if err := enc.Close(); err != nil {
		return err
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("failed to write config %s: %w", path, err)
	}
	if _, err := f.WriteAt(buf.Bytes(), 0); err != nil {
		return fmt.Errorf("failed to write config %s: %w", path, err)
	}
	utils.ShareWithParentGroup(path)
	return nil
}

func lockWithRetry(f *os.File) (*utils.FileLock, error) {
	deadline := time.Now().Add(5 * time.Second)
	for {
		lock, err := utils.LockOpenFile(f, true)
		if err == nil || !errors.Is(err, utils.ErrLockConflict) || time.Now().After(deadline) {
			return lock, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}
