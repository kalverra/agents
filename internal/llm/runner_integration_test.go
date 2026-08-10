//go:build integration

package llm_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/llm"
)

// TestRunner_Routing_WindowsOllama_E2E tests real Wake-on-LAN to Windows PC,
// waits for Ollama server to be reachable, sends prompt, and asserts response.
// Automatically loads ACTIVITY_OLLAMA_HOST and ACTIVITY_WINDOWS_MAC from .env if present.
// Run with: go test -v -tags=integration ./internal/llm -run TestRunner_Routing_WindowsOllama_E2E
func TestRunner_Routing_WindowsOllama_E2E(t *testing.T) {
	host, mac := loadEnvSecrets()
	if host == "" || mac == "" {
		t.Skip("ACTIVITY_OLLAMA_HOST and ACTIVITY_WINDOWS_MAC environment variables required for E2E integration test")
	}

	model := os.Getenv("ACTIVITY_MODEL")
	if model == "" {
		model = "gemma4:31b"
	}

	r := llm.NewRunner(llm.Config{
		Provider:   llm.ProviderAuto,
		Model:      model,
		OllamaHost: host,
		WindowsMAC: mac,
		WoLTimeout: 2 * time.Minute,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	out, err := r.Run(ctx, "e2e-test", "Respond with exact word: PONG")
	require.NoError(t, err)
	assert.NotEmpty(t, out)
	assert.Contains(t, strings.ToUpper(out), "PONG")
}

func loadEnvSecrets() (string, string) {
	host := os.Getenv("ACTIVITY_OLLAMA_HOST")
	mac := os.Getenv("ACTIVITY_WINDOWS_MAC")
	if host != "" && mac != "" {
		return host, mac
	}

	dirsToSearch := []string{"./", "../", "../../"}
	if home, err := os.UserHomeDir(); err == nil {
		dirsToSearch = append(dirsToSearch, filepath.Join(home, ".agents"))
	}
	for _, dir := range dirsToSearch {
		envPath := filepath.Join(dir, ".env")
		data, err := os.ReadFile(envPath)
		if err != nil {
			continue
		}
		for line := range strings.SplitSeq(string(data), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || !strings.Contains(line, "=") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			key := strings.TrimSpace(parts[0])
			val := strings.Trim(strings.TrimSpace(parts[1]), `"'`)
			if key == "ACTIVITY_OLLAMA_HOST" && host == "" {
				host = val
			}
			if key == "ACTIVITY_WINDOWS_MAC" && mac == "" {
				mac = val
			}
		}
	}
	return host, mac
}
