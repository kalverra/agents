package eval

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// ImportLiveFeedbackToCases converts all JSON live feedback records in liveDir
// into YAML test cases under casesDir.
func ImportLiveFeedbackToCases(liveDir, casesDir string) (int, error) {
	entries, err := os.ReadDir(liveDir)
	if err != nil {
		return 0, fmt.Errorf("reading live feedback dir: %w", err)
	}

	if err := os.MkdirAll(casesDir, 0o750); err != nil {
		return 0, fmt.Errorf("creating cases dir: %w", err)
	}

	count := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}

		jsonPath := filepath.Join(liveDir, e.Name())
		rec, err := LoadLiveFeedback(jsonPath)
		if err != nil {
			return count, fmt.Errorf("reading live feedback %s: %w", jsonPath, err)
		}

		c := ConvertLiveToCase(rec)
		yamlData, err := yaml.Marshal(c)
		if err != nil {
			return count, fmt.Errorf("marshaling case %s: %w", c.Name, err)
		}

		outFileName := fmt.Sprintf("%s.yaml", c.Name)
		outPath := filepath.Join(casesDir, outFileName)
		if err := os.WriteFile(outPath, yamlData, 0o600); err != nil {
			return count, fmt.Errorf("writing case file %s: %w", outPath, err)
		}

		count++
	}

	return count, nil
}
