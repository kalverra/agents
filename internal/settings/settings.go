// Package settings edits harness JSON settings files one entry at a time. It
// tracks which entries agents added, so it can later remove exactly those
// and leave the user's own entries alone.
package settings

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// Kind says how an Entry's Value sits at its Path.
type Kind string

// Entry kinds.
const (
	// KindList means Value is one element of the JSON array at Path.
	KindList Kind = "list"
	// KindKey means Value is the value of the object key Path[len(Path)-1].
	KindKey Kind = "key"
)

// Entry is one value in one settings file.
type Entry struct {
	File  string   `json:"file"`
	Path  []string `json:"path"`
	Kind  Kind     `json:"kind"`
	Value any      `json:"value"`
}

// Migrate rewrites a settings file's decoded content before Apply adds
// entries to it, for example to convert a legacy format. It must be
// idempotent.
type Migrate func(file string, m map[string]any)

// Apply makes the settings files hold every desired entry, and removes the
// owned entries that are no longer desired. owned is what an earlier Apply
// returned. Apply returns the entries agents now owns: the desired entries
// it added, plus desired entries it already owned. A desired entry the user
// already had is left alone and not owned, so Remove never deletes it. A
// key the user set to a different value is never overwritten.
//
// Values must be JSON-shaped (string, float64, bool, map[string]any, []any),
// so they compare equal to values decoded from the files.
func Apply(desired, owned []Entry, migrate Migrate) ([]Entry, error) {
	var nowOwned []Entry
	for _, file := range files(owned, desired) {
		m, err := read(file)
		if err != nil {
			return nil, err
		}
		before, err := encode(m)
		if err != nil {
			return nil, err
		}

		fileDesired := inFile(desired, file)
		fileOwned := inFile(owned, file)
		for _, e := range fileOwned {
			if !contains(fileDesired, e) {
				if err := remove(m, e); err != nil {
					return nil, err
				}
			}
		}
		if migrate != nil && len(fileDesired) > 0 {
			migrate(file, m)
		}
		for _, e := range fileDesired {
			added, err := add(m, e)
			if err != nil {
				return nil, err
			}
			if added || contains(fileOwned, e) {
				nowOwned = append(nowOwned, e)
			}
		}

		if err := writeIfChanged(file, m, before); err != nil {
			return nil, err
		}
	}
	return nowOwned, nil
}

// Remove deletes the entries from their files. An entry the file no longer
// holds unchanged is skipped. Lists and objects left empty are deleted, and a
// file left empty is deleted.
func Remove(entries []Entry) error {
	for _, file := range files(entries) {
		m, err := read(file)
		if err != nil {
			return err
		}
		before, err := encode(m)
		if err != nil {
			return err
		}
		for _, e := range inFile(entries, file) {
			if err := remove(m, e); err != nil {
				return err
			}
		}
		if err := writeIfChanged(file, m, before); err != nil {
			return err
		}
	}
	return nil
}

// Files returns the distinct files the entries touch, in first-seen order.
func Files(entries []Entry) []string {
	return files(entries)
}

func files(lists ...[]Entry) []string {
	var out []string
	for _, list := range lists {
		for _, e := range list {
			if !slices.Contains(out, e.File) {
				out = append(out, e.File)
			}
		}
	}
	return out
}

func inFile(entries []Entry, file string) []Entry {
	var out []Entry
	for _, e := range entries {
		if e.File == file {
			out = append(out, e)
		}
	}
	return out
}

func contains(entries []Entry, e Entry) bool {
	return slices.ContainsFunc(entries, func(o Entry) bool { return reflect.DeepEqual(o, e) })
}

// add puts e into m unless it is already there. It reports whether it added.
func add(m map[string]any, e Entry) (bool, error) {
	parent, key, err := parentOf(m, e, true)
	if err != nil {
		return false, err
	}
	switch e.Kind {
	case KindKey:
		if _, ok := parent[key]; ok {
			return false, nil
		}
		parent[key] = e.Value
		return true, nil
	case KindList:
		list, err := listAt(parent, key, e)
		if err != nil {
			return false, err
		}
		if slices.ContainsFunc(list, func(v any) bool { return reflect.DeepEqual(v, e.Value) }) {
			return false, nil
		}
		parent[key] = append(list, e.Value)
		return true, nil
	default:
		return false, fmt.Errorf("settings entry %s: unknown kind %q", describe(e), e.Kind)
	}
}

// remove deletes e from m if m still holds it, then prunes the containers
// on e's path that it left empty.
func remove(m map[string]any, e Entry) error {
	parent, key, err := parentOf(m, e, false)
	if err != nil || parent == nil {
		return err
	}
	switch e.Kind {
	case KindKey:
		if v, ok := parent[key]; ok && reflect.DeepEqual(v, e.Value) {
			delete(parent, key)
		}
	case KindList:
		list, err := listAt(parent, key, e)
		if err != nil {
			return err
		}
		list = slices.DeleteFunc(list, func(v any) bool { return reflect.DeepEqual(v, e.Value) })
		if len(list) == 0 {
			delete(parent, key)
		} else {
			parent[key] = list
		}
	default:
		return fmt.Errorf("settings entry %s: unknown kind %q", describe(e), e.Kind)
	}
	pruneEmpty(m, e.Path[:len(e.Path)-1])
	return nil
}

// parentOf walks e.Path to the object holding its last key. With create, it
// makes missing objects; without, it returns a nil parent.
func parentOf(m map[string]any, e Entry, create bool) (map[string]any, string, error) {
	if len(e.Path) == 0 {
		return nil, "", fmt.Errorf("settings entry in %s has an empty path", e.File)
	}
	cur := m
	for _, k := range e.Path[:len(e.Path)-1] {
		next, ok := cur[k]
		if !ok {
			if !create {
				return nil, "", nil
			}
			child := map[string]any{}
			cur[k] = child
			cur = child
			continue
		}
		child, ok := next.(map[string]any)
		if !ok {
			return nil, "", fmt.Errorf("settings %s: %q is not an object", describe(e), k)
		}
		cur = child
	}
	return cur, e.Path[len(e.Path)-1], nil
}

func listAt(parent map[string]any, key string, e Entry) ([]any, error) {
	v, ok := parent[key]
	if !ok {
		return nil, nil
	}
	list, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("settings %s: not a list", describe(e))
	}
	return list, nil
}

// pruneEmpty deletes empty objects along path, deepest first.
func pruneEmpty(m map[string]any, path []string) {
	for depth := len(path); depth > 0; depth-- {
		cur := m
		for _, k := range path[:depth-1] {
			next, ok := cur[k].(map[string]any)
			if !ok {
				return
			}
			cur = next
		}
		last := path[depth-1]
		if child, ok := cur[last].(map[string]any); ok && len(child) == 0 {
			delete(cur, last)
		}
	}
}

func describe(e Entry) string {
	return e.File + " " + strings.Join(e.Path, ".")
}

func read(path string) (map[string]any, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a harness settings file
	if errors.Is(err, fs.ErrNotExist) {
		return map[string]any{}, nil
	}
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	if m == nil {
		m = map[string]any{}
	}
	return m, nil
}

func encode(m map[string]any) ([]byte, error) {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

// writeIfChanged writes m to path when it differs from before. An empty m
// deletes the file instead.
func writeIfChanged(path string, m map[string]any, before []byte) error {
	data, err := encode(m)
	if err != nil {
		return err
	}
	if string(data) == string(before) {
		return nil
	}
	if len(m) == 0 {
		err := os.Remove(path)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o600)
}
