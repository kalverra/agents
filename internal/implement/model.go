package implement

import (
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"charm.land/lipgloss/v2"
)

// Primary accent colors for harnesses.
const (
	ColorClaude   = "#CC785C" // Terracotta / Coral
	ColorAgy      = "#7C3AED" // Gravity Purple / Violet
	ColorOpencode = "#10B981" // Terminal Emerald / Mint
)

// HarnessColor returns the primary accent hex color for a given harness name.
func HarnessColor(harness string) string {
	switch strings.ToLower(strings.TrimSpace(harness)) {
	case "claude":
		return ColorClaude
	case "agy", "antigravity":
		return ColorAgy
	case "opencode":
		return ColorOpencode
	default:
		return ""
	}
}

// StyleModel applies harness primary accent color styling to a model name when useColor is true.
func StyleModel(model, harness string, useColor bool) string {
	if model == "" || !useColor {
		return model
	}
	hex := HarnessColor(harness)
	if hex == "" {
		return model
	}
	return lipgloss.NewStyle().Foreground(lipgloss.Color(hex)).Render(model)
}

var (
	pPattern = regexp.MustCompile(`^(\d+)p(\d+)$`)
	kPattern = regexp.MustCompile(`^(?i)k(\d+)$`)
	oPattern = regexp.MustCompile(`^(?i)o(\d+)$`)
)

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func formatEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "high":
		return "High"
	case "medium":
		return "Medium"
	case "low":
		return "Low"
	default:
		if effort == "" {
			return ""
		}
		runes := []rune(effort)
		runes[0] = unicode.ToUpper(runes[0])
		return string(runes)
	}
}

// modelFamily strips reasoning-effort suffixes (e.g. "-high") so cost
// attribution groups by model family, not effort variant.
func modelFamily(model string) string {
	for _, suffix := range agyEffortSuffixes {
		if stripped, ok := strings.CutSuffix(model, suffix); ok {
			return stripped
		}
	}
	return model
}

// FormatModelName converts raw model identifiers (e.g. "gemini-3.8-flash-high",
// "fireworks-ai/accounts/fireworks/models/kimi-k3") to human-readable names
// like "Gemini 3.8 Flash (High)", "Opus 5 (Medium)", "Kimi K3 (High)".
func FormatModelName(rawModel string, effort ...string) string {
	raw := strings.TrimSpace(rawModel)
	if raw == "" {
		return ""
	}

	var eff string
	if len(effort) > 0 {
		eff = strings.TrimSpace(effort[0])
	}

	// Strip provider path if present (e.g. fireworks-ai/accounts/fireworks/models/...)
	if idx := strings.LastIndex(raw, "/"); idx != -1 {
		raw = raw[idx+1:]
	}

	// If already formatted and has parentheses, keep it
	if strings.Contains(raw, "(") && strings.Contains(raw, " ") {
		return raw
	}

	// Check for effort suffix embedded in the model name (e.g. -high, -medium, -low)
	lower := strings.ToLower(raw)
	for _, suffix := range []string{"-high", "-medium", "-low"} {
		if strings.HasSuffix(lower, suffix) {
			if eff == "" {
				eff = strings.TrimPrefix(suffix, "-")
			}
			raw = raw[:len(raw)-len(suffix)]
			break
		}
	}

	// If already nicely spaced words without hyphens
	if !strings.Contains(raw, "-") && strings.Contains(raw, " ") {
		if eff != "" {
			return raw + fmt.Sprintf(" (%s)", formatEffort(eff))
		}
		return raw
	}

	// Split slug by "-"
	parts := strings.Split(raw, "-")

	// Merge adjacent pure digits like ["3", "7"] -> "3.7"
	var merged []string
	for i := 0; i < len(parts); i++ {
		if i+1 < len(parts) && isNumeric(parts[i]) && isNumeric(parts[i+1]) {
			merged = append(merged, parts[i]+"."+parts[i+1])
			i++
		} else {
			merged = append(merged, parts[i])
		}
	}

	var formatted []string
	for _, p := range merged {
		switch {
		case strings.EqualFold(p, "glm"):
			formatted = append(formatted, "GLM")
		case strings.EqualFold(p, "gpt"):
			formatted = append(formatted, "GPT")
		case strings.EqualFold(p, "agy"):
			formatted = append(formatted, "AGY")
		case strings.EqualFold(p, "api"):
			formatted = append(formatted, "API")
		case strings.EqualFold(p, "llm"):
			formatted = append(formatted, "LLM")
		case pPattern.MatchString(p):
			formatted = append(formatted, pPattern.ReplaceAllString(p, "${1}.${2}"))
		case kPattern.MatchString(p):
			formatted = append(formatted, kPattern.ReplaceAllString(p, "K${1}"))
		case oPattern.MatchString(p):
			formatted = append(formatted, strings.ToLower(p))
		default:
			if len(p) > 0 {
				runes := []rune(p)
				runes[0] = unicode.ToUpper(runes[0])
				formatted = append(formatted, string(runes))
			}
		}
	}

	result := strings.Join(formatted, " ")
	if eff != "" {
		result += fmt.Sprintf(" (%s)", formatEffort(eff))
	}
	return result
}
