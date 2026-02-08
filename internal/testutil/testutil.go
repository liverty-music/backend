package testutil

import (
	"bufio"
	"os"
	"strings"
	"testing"
)

// LoadTestEnv loads environment variables from a file for testing purposes.
// It parses the file line by line, expecting KEY=VALUE format.
func LoadTestEnv(t *testing.T, filePath string) {
	t.Helper()

	file, err := os.Open(filePath)
	if err != nil {
		t.Logf("Skip loading env file %s: %v", filePath, err)
		return
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		// Remove quotes if present
		value = strings.Trim(value, `"'`)

		if err := os.Setenv(key, value); err != nil {
			t.Fatalf("failed to set env var %s: %v", key, err)
		}
	}

	if err := scanner.Err(); err != nil {
		t.Fatalf("error reading env file: %v", err)
	}
}
