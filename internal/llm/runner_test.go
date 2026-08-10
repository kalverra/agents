package llm_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kalverra/agents/internal/llm"
)

func TestRunner_Run_Agy(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "echo-args"+ext())
	writeScript(t, script, echoAllArgs())

	r := llm.NewRunner(llm.Config{
		Provider: llm.ProviderAgy,
		Cmd:      script,
		Args:     []string{},
		Model:    "gemini-3.6-flash-high",
	})

	out, err := r.Run(context.Background(), "test", "prompt body")
	require.NoError(t, err)
	assert.Equal(t, "prompt body", out)
}

func TestRunner_Run_AgyModelSubstitution(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "echo-args"+ext())
	writeScript(t, script, echoAllArgs())

	r := llm.NewRunner(llm.Config{
		Provider: llm.ProviderAgy,
		Cmd:      script,
		Args:     []string{"--model", "{{MODEL}}"},
		Model:    "custom-model",
	})

	out, err := r.Run(context.Background(), "test", "x")
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{"--model", "custom-model", "x"}, "\n"), out)
}

func TestRunner_Run_Auto_AgyModel(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "echo-args"+ext())
	writeScript(t, script, echoAllArgs())

	r := llm.NewRunner(llm.Config{
		Provider: llm.ProviderAuto,
		Cmd:      script,
		Model:    "gemini-3.6-flash-high",
	})

	out, err := r.Run(context.Background(), "test", "hello agy")
	require.NoError(t, err)
	assert.Equal(t, "hello agy", out)
}

func TestRunner_Run_Ollama_Available(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4"}]}`))
			return
		}
		if r.URL.Path == "/api/generate" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			assert.Equal(t, "gemma4", body["model"])
			assert.Equal(t, "test prompt", body["prompt"])
			assert.Equal(t, "2h", body["keep_alive"])

			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"response":"response from Ollama"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	r := llm.NewRunner(llm.Config{
		Provider:   llm.ProviderAuto,
		Model:      "gemma4",
		OllamaHost: strings.TrimPrefix(ts.URL, "http://"),
		HTTPClient: ts.Client(),
	})

	out, err := r.Run(context.Background(), "test", "test prompt")
	require.NoError(t, err)
	assert.Equal(t, "response from Ollama", out)
}

func TestRunner_Run_Ollama_WakeOnLan_Success(t *testing.T) {
	t.Parallel()

	// UDP listener to receive WoL packet
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	require.NoError(t, err)
	conn, err := net.ListenUDP("udp", udpAddr)
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	var isServerUp atomic.Bool

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isServerUp.Load() {
			http.Error(w, "server off", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Path == "/api/generate" {
			_, _ = w.Write([]byte(`{"response":"wake up success"}`))
			return
		}
	}))
	defer ts.Close()

	go func() {
		buf := make([]byte, 2048)
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, _, readErr := conn.ReadFromUDP(buf)
		if readErr == nil {
			isServerUp.Store(true)
		}
	}()

	r := llm.NewRunner(llm.Config{
		Provider:        llm.ProviderAuto,
		Model:           "qwen3.6",
		OllamaHost:      strings.TrimPrefix(ts.URL, "http://"),
		WindowsMAC:      "AA-BB-CC-DD-EE-FF",
		WoLBroadcast:    conn.LocalAddr().String(),
		WoLTimeout:      3 * time.Second,
		WoLPollInterval: 50 * time.Millisecond,
		HTTPClient:      ts.Client(),
	})

	out, err := r.Run(context.Background(), "test", "wake up")
	require.NoError(t, err)
	assert.Equal(t, "wake up success", out)
}

func TestRunner_Run_Ollama_Unavailable_PromptYes(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "echo-args"+ext())
	writeScript(t, script, echoAllArgs())

	r := llm.NewRunner(llm.Config{
		Provider:        llm.ProviderAuto,
		Model:           "gemma4",
		Cmd:             script,
		OllamaHost:      "127.0.0.1:59999", // closed port
		WoLBroadcast:    "127.0.0.1:59998",
		WoLTimeout:      100 * time.Millisecond,
		WoLPollInterval: 20 * time.Millisecond,
		PromptFn: func(msg string) (bool, error) {
			assert.Contains(t, msg, "Windows PC")
			return true, nil
		},
	})

	out, err := r.Run(context.Background(), "test", "fallback local")
	require.NoError(t, err)
	assert.Contains(t, out, "-p\nfallback local")
}

func TestRunner_Run_Ollama_Unavailable_PromptNo(t *testing.T) {
	t.Parallel()

	r := llm.NewRunner(llm.Config{
		Provider:        llm.ProviderAuto,
		Model:           "gemma4",
		OllamaHost:      "127.0.0.1:59999",
		WoLBroadcast:    "127.0.0.1:59998",
		WoLTimeout:      100 * time.Millisecond,
		WoLPollInterval: 20 * time.Millisecond,
		PromptFn: func(_ string) (bool, error) {
			return false, nil
		},
	})

	_, err := r.Run(context.Background(), "test", "cancel")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "execution declined")
}

func TestRunner_Run_Llama(t *testing.T) {
	t.Parallel()

	script := filepath.Join(t.TempDir(), "echo-args"+ext())
	writeScript(t, script, echoAllArgs())

	r := llm.NewRunner(llm.Config{
		Provider: llm.ProviderLlama,
		Cmd:      script,
		Args:     []string{"--offline", "-st", "--no-display-prompt", "-n", "4096"},
		Model:    "gemma4",
	})

	out, err := r.Run(context.Background(), "test", "hello")
	require.NoError(t, err)
	assert.Equal(t, strings.Join([]string{
		"--offline", "-st", "--no-display-prompt", "-n", "4096",
		"-hf", "gemma4",
		"-p", "hello",
	}, "\n"), out)
}

func TestRunner_Run_UnknownProvider(t *testing.T) {
	t.Parallel()
	r := llm.NewRunner(llm.Config{Provider: "other"})
	_, err := r.Run(context.Background(), "test", "x")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown LLM provider")
}

func TestKnownModels(t *testing.T) {
	t.Parallel()
	models := llm.KnownModels()
	assert.Contains(t, models[llm.ProviderOllama], "gemma4")
	assert.Contains(t, models[llm.ProviderOllama], "qwen3.6")
}

func TestDefaultConfig(t *testing.T) {
	t.Parallel()
	cfg := llm.DefaultConfig()
	assert.Equal(t, llm.ProviderAgy, cfg.Provider)
	assert.Equal(t, "agy", cfg.Cmd)
	assert.Equal(t, "gpt-oss-120b-medium", cfg.Model)
}

func TestRunner_Ollama_Streaming_ProgressSteps(t *testing.T) {
	t.Parallel()

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/tags" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"models":[{"name":"gemma4"}]}`))
			return
		}
		if r.URL.Path == "/api/generate" {
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			assert.Equal(t, true, req["stream"])

			w.Header().Set("Content-Type", "application/x-ndjson")
			// Chunk 1: Model load completed with load_duration = 2s
			_, _ = w.Write([]byte(`{"model":"gemma4","response":"","done":false,"load_duration":2000000000}` + "\n"))
			// Chunk 2: First text token
			_, _ = w.Write([]byte(`{"model":"gemma4","response":"Hello ","done":false}` + "\n"))
			// Chunk 3: Second text token and done
			_, _ = w.Write([]byte(`{"model":"gemma4","response":"world","done":true,"eval_count":2}` + "\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer ts.Close()

	var buf bytes.Buffer
	r := llm.NewRunner(llm.Config{
		Provider:   llm.ProviderOllama,
		Model:      "gemma4",
		OllamaHost: strings.TrimPrefix(ts.URL, "http://"),
		HTTPClient: ts.Client(),
	})
	r.Out = &buf

	outText, err := r.Run(context.Background(), "commit message", "generate prompt")
	require.NoError(t, err)
	assert.Equal(t, "Hello world", outText)

	progressOutput := buf.String()
	assert.Contains(t, progressOutput, "Connect to Ollama")
	assert.Contains(t, progressOutput, "Connected in ")
	assert.Contains(t, progressOutput, "Load gemma4 into VRAM (Loaded in 2s)")
	assert.Contains(t, progressOutput, "Generate Response (11 bytes, 2 tokens)")
}

func ext() string {
	if runtime.GOOS == "windows" {
		return ".bat"
	}
	return ""
}

func echoAllArgs() string {
	return `#!/bin/sh
printf "%s\n" "$@"
`
}

func writeScript(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0700)) //nolint:gosec
}
