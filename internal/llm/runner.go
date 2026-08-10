// Package llm executes an external AI agent CLI with a prompt and returns its output.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"charm.land/huh/v2"
	"github.com/mattn/go-isatty"
)

const (
	// ProviderAgy is the agy cloud agent CLI.
	ProviderAgy = "agy"
	// ProviderLlama is a local llama.cpp CLI.
	ProviderLlama = "llama"
	// ProviderOllama is an Ollama HTTP server.
	ProviderOllama = "ollama"
	// ProviderAuto automatically selects provider based on model & host availability.
	ProviderAuto = "auto"
)

// Config controls which external agent CLI or LLM backend is used and how.
type Config struct {
	// Provider selects the LLM backend. Allowed values: "agy", "llama", "ollama", "auto".
	Provider string
	// Cmd is the agent binary. For agy this is "agy"; for llama this is "llama-cli".
	// Must be on PATH unless an absolute path is provided.
	Cmd string
	// Args are passed to Cmd before the prompt is sent.
	// For agy, use the literal "{{MODEL}}" anywhere in an argument to substitute Config.Model.
	Args []string
	// Model is the model identifier passed to the agent.
	Model string

	// OllamaHost is the host:port for Ollama HTTP server (e.g. from ACTIVITY_OLLAMA_HOST).
	OllamaHost string
	// WindowsMAC is the MAC address for sending Wake-on-LAN packets (e.g. from ACTIVITY_WINDOWS_MAC).
	WindowsMAC string
	// WoLBroadcast is the broadcast destination for WoL magic packet (default "255.255.255.255:9").
	WoLBroadcast string
	// WoLTimeout is max duration to wait for host to wake up after WoL packet (default 2m).
	WoLTimeout time.Duration
	// WoLPollInterval is how frequently to probe Ollama during wake-up (default 2s).
	WoLPollInterval time.Duration

	// OllamaKeepAlive is how long to keep the model loaded in VRAM (default "2h").
	OllamaKeepAlive string
	// OllamaLoadTimeout is max duration to wait for loading model into VRAM (default 5m).
	OllamaLoadTimeout time.Duration
	// OllamaGenTimeout is max duration to wait for response generation after model load (default 3m).
	OllamaGenTimeout time.Duration

	// PromptFn prompts the user interactively (returns true if user approves local fallback).
	PromptFn func(msg string) (bool, error)
	// HTTPClient overrides default http.Client for network requests (useful for testing).
	HTTPClient *http.Client
}

// Runner runs an external LLM agent CLI or HTTP server.
type Runner struct {
	cfg Config
	Out io.Writer
	Now func() time.Time
}

// NewRunner creates a runner from config.
func NewRunner(cfg Config) *Runner {
	if cfg.WoLTimeout == 0 {
		cfg.WoLTimeout = 2 * time.Minute
	}
	if cfg.WoLPollInterval == 0 {
		cfg.WoLPollInterval = 2 * time.Second
	}
	if cfg.OllamaKeepAlive == "" {
		cfg.OllamaKeepAlive = "2h"
	}
	if cfg.OllamaLoadTimeout == 0 {
		cfg.OllamaLoadTimeout = 5 * time.Minute
	}
	if cfg.OllamaGenTimeout == 0 {
		cfg.OllamaGenTimeout = 3 * time.Minute
	}
	return &Runner{cfg: cfg}
}

func (r *Runner) writer() io.Writer {
	if r.Out != nil {
		return r.Out
	}
	return os.Stderr
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

func (r *Runner) httpClient(timeout time.Duration) *http.Client {
	if r.cfg.HTTPClient != nil {
		return r.cfg.HTTPClient
	}
	return &http.Client{Timeout: timeout}
}

// DefaultConfig returns sensible defaults for the agy CLI.
func DefaultConfig() Config {
	return Config{
		Provider: ProviderAgy,
		Cmd:      "agy",
		Args:     []string{"--print", "--output-format", "text", "--model", "{{MODEL}}"},
		Model:    "gpt-oss-120b-medium",
	}
}

// DefaultLlamaConfig returns sensible defaults for the llama.cpp CLI.
func DefaultLlamaConfig() Config {
	return Config{
		Provider: ProviderLlama,
		Cmd:      "llama-cli",
		Args: []string{
			"--offline",
			"--simple-io", "--no-display-prompt", "--log-disable", "--no-conversation", "--no-show-timings", "-st",
			"-ngl", "99",
			"-fa", "1",
			"-b", "2048", "-ub", "2048",
			"-lm", "mlock",
			"-c", "16384",
			"-n", "8192",
		},
		Model: "",
	}
}

// KnownModels lists example models for each provider.
func KnownModels() map[string][]string {
	return map[string][]string{
		ProviderAgy: {
			"gemini-3.6-flash-high",
			"gemini-3.6-flash-medium",
			"gemini-3.6-flash-low",
			"gemini-3.5-flash-high",
			"gemini-3.5-flash-medium",
			"gemini-3.5-flash-low",
			"gemini-3.1-pro-high",
			"gemini-3.1-pro-low",
			"claude-sonnet-4-6",
			"claude-opus-4-6-thinking",
			"gpt-oss-120b-medium",
		},
		ProviderOllama: {
			"gemma4:31b",
			"qwen3.6:35b",
			"gemma4",
			"qwen3.6",
		},
		ProviderLlama: {
			"gemma4:31b",
			"qwen3.6:35b",
			"gemma4",
			"qwen3.6",
		},
	}
}

// IsAgyModel returns true if the model name matches agy models.
func IsAgyModel(model string) bool {
	m := strings.ToLower(model)
	if strings.HasPrefix(m, "gemini-") || strings.HasPrefix(m, "claude-") || strings.HasPrefix(m, "gpt-") {
		return true
	}
	for _, known := range KnownModels()[ProviderAgy] {
		if strings.EqualFold(known, model) {
			return true
		}
	}
	return false
}

// Run sends prompt to the agent CLI / server and returns stdout.
func (r *Runner) Run(ctx context.Context, target, prompt string) (string, error) {
	if target == "" {
		target = "answer"
	}
	model := r.cfg.Model
	if model == "" {
		model = r.cfg.Cmd
	}

	w := r.writer()
	start := r.now()

	useColor := false
	if f, ok := w.(*os.File); ok {
		useColor = isatty.IsTerminal(f.Fd())
	}

	prog := NewProgress(w, target, model, useColor)
	defer prog.Stop()

	var res string
	var err error

	provider := strings.ToLower(r.cfg.Provider)
	if provider == "" {
		provider = ProviderAuto
	}

	switch provider {
	case ProviderAgy:
		res, err = r.runAgy(ctx, prompt, prog)
	case ProviderOllama:
		res, err = r.runOllamaWithWoL(ctx, prompt, prog)
	case ProviderLlama:
		res, err = r.runLlama(ctx, prompt, prog)
	case ProviderAuto:
		if IsAgyModel(r.cfg.Model) {
			res, err = r.runAgy(ctx, prompt, prog)
		} else {
			res, err = r.runAutoNonAgy(ctx, prompt, prog)
		}
	default:
		err = fmt.Errorf("unknown LLM provider %q", r.cfg.Provider)
	}

	if err == nil {
		prog.Finish(r.now().Sub(start))
	}

	return res, err
}

func (r *Runner) runAutoNonAgy(ctx context.Context, prompt string, prog *Progress) (string, error) {
	res, err := r.runOllamaWithWoL(ctx, prompt, prog)
	if err == nil {
		return res, nil
	}

	approved, promptErr := r.promptLocalFallback(fmt.Sprintf(
		"Windows PC (%s) is unreachable or failed to run %s. Install/use this model on local Mac?",
		r.cfg.OllamaHost,
		r.cfg.Model,
	))
	if promptErr != nil {
		return "", promptErr
	}
	if !approved {
		return "", fmt.Errorf("model execution declined for local Mac fallback")
	}

	llamaCfg := DefaultLlamaConfig()
	if r.cfg.Cmd != "" {
		llamaCfg.Cmd = r.cfg.Cmd
	}
	if len(r.cfg.Args) > 0 {
		llamaCfg.Args = r.cfg.Args
	}
	llamaCfg.Model = r.cfg.Model

	localRunner := NewRunner(llamaCfg)
	localRunner.Out = r.Out
	localRunner.Now = r.Now

	return localRunner.runLlama(ctx, prompt, prog)
}

func (r *Runner) promptLocalFallback(msg string) (bool, error) {
	if r.cfg.PromptFn != nil {
		return r.cfg.PromptFn(msg)
	}

	if !isatty.IsTerminal(os.Stdin.Fd()) {
		return false, fmt.Errorf("non-interactive terminal: cannot prompt for local Mac fallback")
	}

	var proceed bool
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Title(msg).
				Affirmative("Yes").
				Negative("No").
				Value(&proceed),
		),
	).WithTheme(huh.ThemeFunc(huh.ThemeCharm))

	if err := form.Run(); err != nil {
		return false, err
	}
	return proceed, nil
}

func (r *Runner) probeOllama(ctx context.Context) bool {
	client := r.httpClient(5 * time.Second)
	url := fmt.Sprintf("http://%s/api/tags", r.cfg.OllamaHost)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	_ = resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

func (r *Runner) runOllamaWithWoL(ctx context.Context, prompt string, prog *Progress) (string, error) {
	if r.cfg.OllamaHost == "" {
		return "", fmt.Errorf("no Ollama host configured (set ACTIVITY_OLLAMA_HOST in .env)")
	}

	macLabel := r.cfg.WindowsMAC
	if macLabel == "" {
		macLabel = "unconfigured"
	}
	s1 := prog.AddStep(fmt.Sprintf("Wake-on-LAN (%s)", macLabel))

	if !r.probeOllama(ctx) {
		if r.cfg.WindowsMAC == "" {
			err := fmt.Errorf("no MAC address configured for WoL (set ACTIVITY_WINDOWS_MAC in .env)")
			s1.Fail(err)
			return "", err
		}
		_ = SendMagicPacket(r.cfg.WindowsMAC, r.cfg.WoLBroadcast)

		ticker := time.NewTicker(r.cfg.WoLPollInterval)
		defer ticker.Stop()

		deadline := time.Now().Add(r.cfg.WoLTimeout)
		woken := false
	forPoll:
		for time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				s1.Fail(ctx.Err())
				return "", ctx.Err()
			case <-ticker.C:
				if r.probeOllama(ctx) {
					woken = true
					break forPoll
				}
			}
		}

		if !woken {
			err := fmt.Errorf("windows PC at %s did not wake up within %s", r.cfg.OllamaHost, r.cfg.WoLTimeout)
			s1.Fail(err)
			return "", err
		}
		s1.Complete("Host woken up")
	} else {
		s1.Complete("Host already awake")
	}

	s2 := prog.AddStep(fmt.Sprintf("Connect to Ollama (%s)", r.cfg.OllamaHost))
	probeStart := time.Now()
	if !r.probeOllama(ctx) {
		err := fmt.Errorf("ollama at %s is unreachable", r.cfg.OllamaHost)
		s2.Fail(err)
		return "", err
	}
	latency := time.Since(probeStart).Round(time.Millisecond)
	s2.Complete(fmt.Sprintf("Connected in %v", latency))

	s3 := prog.AddStep(fmt.Sprintf("Load %s into VRAM", r.cfg.Model))

	loadCtx, loadCancel := context.WithTimeout(ctx, r.cfg.OllamaLoadTimeout)
	defer loadCancel()

	client := r.httpClient(0)
	url := fmt.Sprintf("http://%s/api/generate", r.cfg.OllamaHost)

	reqBody := map[string]any{
		"model":      r.cfg.Model,
		"prompt":     prompt,
		"stream":     true,
		"keep_alive": r.cfg.OllamaKeepAlive,
	}
	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		s3.Fail(err)
		return "", fmt.Errorf("marshaling Ollama request: %w", err)
	}

	req, err := http.NewRequestWithContext(loadCtx, http.MethodPost, url, bytes.NewReader(jsonBytes))
	if err != nil {
		s3.Fail(err)
		return "", fmt.Errorf("creating Ollama request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		s3.Fail(err)
		return "", fmt.Errorf("ollama API request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("ollama server returned status %d: %s", resp.StatusCode, string(respBytes))
		s3.Fail(err)
		return "", err
	}

	return decodeOllamaStream(ctx, resp.Body, prog, s3, r.cfg.OllamaLoadTimeout, r.cfg.OllamaGenTimeout)
}

type ollamaStreamChunk struct {
	Model        string `json:"model"`
	Response     string `json:"response"`
	Done         bool   `json:"done"`
	Error        string `json:"error,omitempty"`
	LoadDuration int64  `json:"load_duration,omitempty"`
	EvalCount    int    `json:"eval_count,omitempty"`
}

type chunkResult struct {
	chunk ollamaStreamChunk
	err   error
}

func decodeOllamaStream(
	ctx context.Context,
	r io.Reader,
	prog *Progress,
	loadStep *Step,
	loadTimeout, genTimeout time.Duration,
) (string, error) {
	chunkChan := make(chan chunkResult)

	go func() {
		defer close(chunkChan)
		decoder := json.NewDecoder(r)
		for decoder.More() {
			var chunk ollamaStreamChunk
			err := decoder.Decode(&chunk)
			chunkChan <- chunkResult{chunk: chunk, err: err}
			if err != nil || chunk.Done {
				break
			}
		}
	}()

	var (
		fullText   strings.Builder
		firstToken bool
		genStep    *Step
		finalChunk ollamaStreamChunk
	)

	loadTimer := time.NewTimer(loadTimeout)
	defer loadTimer.Stop()

	var genTimer *time.Timer

	for {
		var activeTimer <-chan time.Time
		if !firstToken {
			activeTimer = loadTimer.C
		} else if genTimer != nil {
			activeTimer = genTimer.C
		}

		select {
		case <-ctx.Done():
			err := ctx.Err()
			if !firstToken {
				loadStep.Fail(err)
			} else if genStep != nil {
				genStep.Fail(err)
			}
			return "", err

		case <-activeTimer:
			err := fmt.Errorf("context deadline exceeded")
			if !firstToken {
				loadStep.Fail(fmt.Errorf("loading into VRAM timed out after %v: %w", loadTimeout, err))
			} else if genStep != nil {
				genStep.Fail(fmt.Errorf("generation timed out after %v: %w", genTimeout, err))
			}
			return "", err

		case res, ok := <-chunkChan:
			if !ok || errors.Is(res.err, io.EOF) {
				completeGenStep(genStep, fullText.Len(), finalChunk)
				return strings.TrimSpace(fullText.String()), nil
			}

			if res.err != nil {
				failStep(loadStep, genStep, firstToken, res.err)
				return "", fmt.Errorf("reading Ollama stream: %w", res.err)
			}

			chunk := res.chunk
			if chunk.Error != "" {
				err := fmt.Errorf("ollama error: %s", chunk.Error)
				failStep(loadStep, genStep, firstToken, err)
				return "", err
			}

			if !firstToken && (chunk.Response != "" || chunk.Done || chunk.LoadDuration > 0) {
				firstToken = true
				stopTimer(loadTimer)
				completeLoadStep(loadStep, chunk.LoadDuration)
				genStep = prog.AddStep("Generate Response")
				genTimer = time.NewTimer(genTimeout)
				defer genTimer.Stop()
			}

			if chunk.Response != "" {
				fullText.WriteString(chunk.Response)
			}

			if chunk.Done {
				finalChunk = chunk
				completeGenStep(genStep, fullText.Len(), finalChunk)
				return strings.TrimSpace(fullText.String()), nil
			}
		}
	}
}

func failStep(loadStep, genStep *Step, firstToken bool, err error) {
	if !firstToken {
		loadStep.Fail(err)
	} else if genStep != nil {
		genStep.Fail(err)
	}
}

func stopTimer(t *time.Timer) {
	if t != nil && t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}

func completeLoadStep(loadStep *Step, loadDuration int64) {
	if loadDuration > 0 {
		loadDur := time.Duration(loadDuration).Round(time.Second)
		loadStep.Complete(fmt.Sprintf("Loaded in %v", loadDur))
	} else {
		loadStep.Complete("Model ready")
	}
}

func completeGenStep(genStep *Step, textLen int, finalChunk ollamaStreamChunk) {
	if genStep == nil {
		return
	}
	detail := fmt.Sprintf("%d bytes", textLen)
	if finalChunk.EvalCount > 0 {
		detail = fmt.Sprintf("%d bytes, %d tokens", textLen, finalChunk.EvalCount)
	}
	genStep.Complete(detail)
}

func (r *Runner) runAgy(ctx context.Context, prompt string, prog *Progress) (string, error) {
	if r.cfg.Cmd == "" {
		return "", fmt.Errorf("no agent command configured")
	}

	s1 := prog.AddStep(fmt.Sprintf("Execute %s CLI", r.cfg.Cmd))

	args := make([]string, len(r.cfg.Args))
	for i, a := range r.cfg.Args {
		args[i] = strings.Replace(a, "{{MODEL}}", r.cfg.Model, 1)
	}
	args = append(args, prompt)

	cmd := exec.CommandContext(ctx, r.cfg.Cmd, args...) //nolint:gosec

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			err = fmt.Errorf("agent command %q failed: %w: %s", r.cfg.Cmd, err, string(exitErr.Stderr))
		} else {
			err = fmt.Errorf("agent command %q failed: %w", r.cfg.Cmd, err)
		}
		s1.Fail(err)
		return "", err
	}
	s1.Complete("Output received")
	return strings.TrimSpace(string(out)), nil
}

func (r *Runner) runLlama(ctx context.Context, prompt string, prog *Progress) (string, error) {
	if r.cfg.Cmd == "" {
		return "", fmt.Errorf("no llama.cpp command configured")
	}
	if r.cfg.Model == "" {
		return "", fmt.Errorf("no llama.cpp model configured")
	}

	s1 := prog.AddStep(fmt.Sprintf("Execute local %s", r.cfg.Cmd))

	args := make([]string, len(r.cfg.Args))
	copy(args, r.cfg.Args)
	args = append(
		args,
		"-hf", r.cfg.Model,
		"-p", prompt,
	)

	cmd := exec.CommandContext(ctx, r.cfg.Cmd, args...) //nolint:gosec

	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			err = fmt.Errorf("llama.cpp command %q failed: %w: %s", r.cfg.Cmd, err, string(exitErr.Stderr))
		} else {
			err = fmt.Errorf("llama.cpp command %q failed: %w", r.cfg.Cmd, err)
		}
		s1.Fail(err)
		return "", err
	}
	s1.Complete("Output received")
	return strings.TrimSpace(string(out)), nil
}
