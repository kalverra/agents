package install

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kalverra/agents/internal/paths"
	"github.com/kalverra/agents/internal/settings"
)

// Record is installed.json: what install wrote into each harness, so a later
// install or uninstall can undo exactly that.
type Record struct {
	Harnesses map[string]*HarnessRecord `json:"harnesses"`
}

// HarnessRecord is what install wrote into one harness.
type HarnessRecord struct {
	// Instructions is the file holding the managed rules block.
	Instructions string `json:"instructions,omitempty"`
	// Permissions are the settings entries agents added.
	Permissions []settings.Entry `json:"permissions,omitempty"`
	// Skills maps each skill gh installed to its source (see Plan.source).
	Skills map[string]string `json:"skills,omitempty"`
	// SkillsDir is where gh installed the skills.
	SkillsDir string `json:"skills_dir,omitempty"`
}

// RecordPath returns where installed.json lives.
func RecordPath() string {
	return filepath.Join(paths.StateDir(), "installed.json")
}

// LoadRecord reads installed.json. A missing file is an empty Record.
func LoadRecord() (*Record, error) {
	rec := &Record{Harnesses: map[string]*HarnessRecord{}}
	data, err := os.ReadFile(RecordPath())
	if errors.Is(err, fs.ErrNotExist) {
		return rec, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, rec); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", RecordPath(), err)
	}
	if rec.Harnesses == nil {
		rec.Harnesses = map[string]*HarnessRecord{}
	}
	return rec, nil
}

// Save writes installed.json, or deletes it when the record is empty.
func (r *Record) Save() error {
	if len(r.Harnesses) == 0 {
		err := os.Remove(RecordPath())
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	data, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(RecordPath()), 0o750); err != nil {
		return err
	}
	return os.WriteFile(RecordPath(), append(data, '\n'), 0o600)
}

// harness returns name's record, adding an empty one if missing.
func (r *Record) harness(name string) *HarnessRecord {
	hr, ok := r.Harnesses[name]
	if !ok {
		hr = &HarnessRecord{}
		r.Harnesses[name] = hr
	}
	return hr
}
