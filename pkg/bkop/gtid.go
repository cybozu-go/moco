package bkop

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// GetGTIDExecuted gets executed GTID set from the dump directory.
func GetGTIDExecuted(dir string) (string, error) {
	fname := filepath.Join(dir, "@.json")
	data, err := os.ReadFile(fname)
	if err != nil {
		return "", fmt.Errorf("could not read %s: %w", fname, err)
	}

	var t struct {
		GTIDExecuted string `json:"gtidExecuted"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return "", fmt.Errorf("failed to parse contents in @.json: %w", err)
	}

	return t.GTIDExecuted, nil
}
