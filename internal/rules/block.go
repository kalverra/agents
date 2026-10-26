package rules

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Markers around the block agents manages in an instructions file. Text
// outside the markers belongs to the user and is never changed.
const (
	BeginMarker = "<!-- agents:begin -->"
	EndMarker   = "<!-- agents:end -->"
	blockNote   = "<!-- Managed by `agents install`. Edits inside this block are overwritten. -->"
)

// WriteBlock puts body in the managed block of the file at path. It replaces
// an existing block in place, or appends one after the user's text. It
// creates the file and its dir if needed. A symlink at path (from older
// agents versions) is replaced by a regular file holding only the block, so
// the link's target is never written.
func WriteBlock(path, body string) error {
	content, mode, err := readRegular(path)
	if err != nil {
		return err
	}
	before, after, found, err := splitBlock(content)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !found && strings.TrimSpace(content) != "" {
		before = strings.TrimRight(content, "\n") + "\n\n"
	} else if !found {
		before = ""
	}
	block := BeginMarker + "\n" + blockNote + "\n\n" + strings.TrimSpace(body) + "\n" + EndMarker + "\n"
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return os.WriteFile(path, []byte(before+block+after), mode)
}

// RemoveBlock deletes the managed block from the file at path. It deletes the
// file when nothing but whitespace is left. A missing file or a file without
// a block is left as is.
func RemoveBlock(path string) error {
	content, mode, err := readRegular(path)
	if err != nil {
		return err
	}
	before, after, found, err := splitBlock(content)
	if err != nil {
		return fmt.Errorf("%s: %w", path, err)
	}
	if !found {
		return nil
	}
	rest := strings.TrimRight(before, "\n")
	if tail := strings.TrimLeft(after, "\n"); tail != "" {
		if rest != "" {
			rest += "\n\n"
		}
		rest += tail
	} else if rest != "" {
		rest += "\n"
	}
	if strings.TrimSpace(rest) == "" {
		return os.Remove(path)
	}
	return os.WriteFile(path, []byte(rest), mode)
}

// readRegular returns the content and mode of the regular file at path. A
// missing file or a symlink reads as empty, with the default mode.
func readRegular(path string) (string, fs.FileMode, error) {
	const defaultMode = 0o644
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return "", defaultMode, nil
	}
	if err != nil {
		return "", 0, err
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return "", defaultMode, nil
	}
	data, err := os.ReadFile(path) //nolint:gosec // path is a harness instructions file
	if err != nil {
		return "", 0, err
	}
	return string(data), info.Mode().Perm(), nil
}

// splitBlock returns the text before the begin marker and after the end
// marker's line. found is false when there is no block.
func splitBlock(content string) (before, after string, found bool, err error) {
	begin := strings.Index(content, BeginMarker)
	end := strings.Index(content, EndMarker)
	switch {
	case begin < 0 && end < 0:
		return content, "", false, nil
	case begin < 0 || end < begin:
		return "", "", false, errors.New("agents block markers are out of order; fix them by hand")
	case strings.Count(content, BeginMarker) > 1:
		return "", "", false, errors.New("more than one agents block; remove the extras by hand")
	}
	after = content[end+len(EndMarker):]
	after = strings.TrimPrefix(after, "\n")
	return content[:begin], after, true, nil
}
