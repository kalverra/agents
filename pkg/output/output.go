// Package output handles command result rendering and progress display.
//
// Commands call Write() once to handle both AI and human output:
//
//	output.Write("eval", data, func() {
//	    fmt.Println("human-friendly output here")
//	})
//
// In JSON mode (--ai-output), Write emits {"status":"ok","command":"...","data":...}
// and skips humanFn. In human mode, it calls humanFn and ignores data.
package output

import (
	"encoding/json"
	"fmt"
	"os"
)

var jsonMode bool

// SetJSON configures whether output is JSON (for AI/LLM consumption).
func SetJSON(enabled bool) { jsonMode = enabled }

// JSON returns true if output is in JSON mode.
func JSON() bool { return jsonMode }

type envelope struct {
	Status  string `json:"status"`
	Command string `json:"command"`
	Data    any    `json:"data"`
}

// Write renders the final command result.
// AI mode (--ai-output): if data is a string, prints it directly. Otherwise emits compact single-line JSON. Never uses markdown code fences.
// Human mode: calls humanFn for display.
func Write(_ string, data any, humanFn func()) {
	if jsonMode {
		if s, ok := data.(string); ok {
			fmt.Println(s)
			return
		}
		_ = json.NewEncoder(os.Stdout).Encode(data)
		return
	}
	if humanFn != nil {
		humanFn()
	}
}

// WriteIndent is like Write but emits indented JSON in AI mode for easier reading.
func WriteIndent(command string, data any, humanFn func()) {
	if jsonMode {
		out, err := json.MarshalIndent(envelope{
			Status:  "ok",
			Command: command,
			Data:    data,
		}, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "ERROR: encoding JSON: %v\n", err)
			return
		}
		fmt.Println(string(out))
		return
	}
	if humanFn != nil {
		humanFn()
	}
}

// Printf prints to stdout; suppressed in JSON mode.
func Printf(format string, a ...any) {
	if !jsonMode {
		fmt.Printf(format, a...)
	}
}

// Println prints to stdout; suppressed in JSON mode.
func Println(a ...any) {
	if !jsonMode {
		fmt.Println(a...)
	}
}

// Successf prints a success message to stdout; suppressed in JSON mode.
func Successf(format string, a ...any) {
	if !jsonMode {
		fmt.Printf("✅ "+format, a...)
	}
}

// Warnf prints a warning to stderr (always shown).
func Warnf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "⚠️  "+format, a...)
}

// Errorf prints an error to stderr (always shown).
func Errorf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "ERROR: "+format, a...)
}

// Verbosef prints to stdout if verbose and not in JSON mode.
func Verbosef(verbose bool, format string, a ...any) {
	if verbose && !jsonMode {
		fmt.Printf(format, a...)
	}
}
