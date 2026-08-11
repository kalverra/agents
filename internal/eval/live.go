package eval

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mattn/go-isatty"
)

// LiveFeedbackRecord stores human rating and context for a live LLM execution.
type LiveFeedbackRecord struct {
	ID                string            `json:"id"`
	Timestamp         string            `json:"timestamp"`
	Command           string            `json:"command"`
	Mode              string            `json:"mode,omitempty"`
	FullPrompt        string            `json:"full_prompt"`
	PromptFile        string            `json:"prompt_file"`
	PromptHash        string            `json:"prompt_hash"`
	PromptVersion     string            `json:"prompt_version"`
	Model             string            `json:"model"`
	Provider          string            `json:"provider"`
	Response          string            `json:"response"`
	Thinking          string            `json:"thinking,omitempty"`
	TranscriptFile    string            `json:"transcript_file,omitempty"`
	CorrectedResponse string            `json:"corrected_response,omitempty"`
	UserScore         int               `json:"user_score"`
	UserReason        string            `json:"user_reason"`
	Metadata          map[string]string `json:"metadata,omitempty"`
}

// ComputePromptHash calculates SHA-256 digest of prompt template content.
func ComputePromptHash(body string) string {
	h := sha256.Sum256([]byte(body))
	return hex.EncodeToString(h[:])
}

// FormatTranscript formats full prompt, thinking tokens, response, and corrected response into a readable transcript string.
func FormatTranscript(rec LiveFeedbackRecord) string {
	var b strings.Builder
	b.WriteString("=== FULL PROMPT ===\n")
	b.WriteString(rec.FullPrompt)
	b.WriteString("\n")

	if rec.Thinking != "" {
		b.WriteString("\n=== THINKING / REASONING ===\n")
		b.WriteString(rec.Thinking)
		b.WriteString("\n")
	}

	b.WriteString("\n=== RESPONSE ===\n")
	b.WriteString(rec.Response)
	b.WriteString("\n")

	if rec.CorrectedResponse != "" {
		b.WriteString("\n=== CORRECTED RESPONSE ===\n")
		b.WriteString(rec.CorrectedResponse)
		b.WriteString("\n")
	}

	return b.String()
}

// SaveLiveFeedback persists a feedback record to a JSON file and transcript TXT file in dir.
func SaveLiveFeedback(dir string, rec LiveFeedbackRecord) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating live feedback dir: %w", err)
	}

	filename := fmt.Sprintf("live_%s.json", rec.ID)
	filePath := filepath.Join(dir, filename)

	transcriptFilename := fmt.Sprintf("live_%s.transcript.txt", rec.ID)
	transcriptPath := filepath.Join(dir, transcriptFilename)

	rec.TranscriptFile = transcriptFilename

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling live feedback record: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return "", fmt.Errorf("writing live feedback file: %w", err)
	}

	transcriptBody := FormatTranscript(rec)
	if err := os.WriteFile(transcriptPath, []byte(transcriptBody), 0o600); err != nil {
		return "", fmt.Errorf("writing live feedback transcript file: %w", err)
	}

	return filePath, nil
}

// SavePendingLiveFeedback persists a pending feedback record and transcript file before editor closes.
func SavePendingLiveFeedback(dir string, rec LiveFeedbackRecord) (string, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("creating live feedback dir: %w", err)
	}

	filename := fmt.Sprintf("pending_%s.json", rec.ID)
	filePath := filepath.Join(dir, filename)

	transcriptFilename := fmt.Sprintf("pending_%s.transcript.txt", rec.ID)
	transcriptPath := filepath.Join(dir, transcriptFilename)

	rec.TranscriptFile = transcriptFilename

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling pending live feedback record: %w", err)
	}

	if err := os.WriteFile(filePath, data, 0o600); err != nil {
		return "", fmt.Errorf("writing pending live feedback file: %w", err)
	}

	transcriptBody := FormatTranscript(rec)
	if err := os.WriteFile(transcriptPath, []byte(transcriptBody), 0o600); err != nil {
		return "", fmt.Errorf("writing pending live feedback transcript file: %w", err)
	}

	return filePath, nil
}

// FinalizeLiveFeedback loads a pending feedback record, updates CorrectedResponse from commitMsgPath, and saves it.
func FinalizeLiveFeedback(evalDir, pendingPath, commitMsgPath string) (*LiveFeedbackRecord, error) {
	rec, err := LoadLiveFeedback(pendingPath)
	if err != nil {
		return nil, fmt.Errorf("loading pending live feedback: %w", err)
	}

	data, err := os.ReadFile(commitMsgPath) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("reading commit message file for finalization: %w", err)
	}

	rec.CorrectedResponse = StripGitComments(string(data))

	if _, err := SaveLiveFeedback(evalDir, *rec); err != nil {
		return nil, fmt.Errorf("saving finalized live feedback: %w", err)
	}

	_ = os.Remove(pendingPath)
	pendingTranscript := strings.TrimSuffix(pendingPath, ".json") + ".transcript.txt"
	_ = os.Remove(pendingTranscript)

	return rec, nil
}

// StripGitComments removes lines starting with # and trims whitespace.
func StripGitComments(s string) string {
	lines := strings.Split(s, "\n")
	var kept []string
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

// LoadLiveFeedback reads a feedback record from a JSON file.
func LoadLiveFeedback(path string) (*LiveFeedbackRecord, error) {
	data, err := os.ReadFile(path) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("reading live feedback file: %w", err)
	}

	var rec LiveFeedbackRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, fmt.Errorf("unmarshaling live feedback record: %w", err)
	}

	return &rec, nil
}

// PromptUserFeedback prompts the user for a 1-5 score and feedback reason.
func PromptUserFeedback(in io.Reader, out io.Writer) (int, string, error) {
	if in == nil || in == os.Stdin {
		if f, ok := in.(*os.File); !ok || !isatty.IsTerminal(f.Fd()) {
			if tty, err := os.Open("/dev/tty"); err == nil {
				defer func() { _ = tty.Close() }()
				in = tty
			}
		}
	}
	if in == nil {
		in = os.Stdin
	}

	scanner := bufio.NewScanner(in)

	_, _ = fmt.Fprint(out, "Score response (1-5, 1=bad, 5=good): ")
	if !scanner.Scan() {
		return 0, "", scanner.Err()
	}
	scoreStr := strings.TrimSpace(scanner.Text())
	score, err := strconv.Atoi(scoreStr)
	if err != nil || score < 1 || score > 5 {
		return 0, "", fmt.Errorf("invalid score %q: must be integer between 1 and 5", scoreStr)
	}

	_, _ = fmt.Fprint(out, "Reason / feedback rationale: ")
	if !scanner.Scan() {
		return score, "", scanner.Err()
	}
	reason := strings.TrimSpace(scanner.Text())

	return score, reason, nil
}

// ConvertLiveToCase converts a LiveFeedbackRecord into an eval Case.
func ConvertLiveToCase(rec *LiveFeedbackRecord) Case {
	return Case{
		Name:            rec.ID,
		Tags:            []string{"live", rec.Command},
		Description:     fmt.Sprintf("Live feedback for %s (score: %d/5)", rec.Command, rec.UserScore),
		UserMessage:     rec.FullPrompt,
		ReferenceAnswer: rec.Response,
		Criteria: Criteria{
			Name:        "Live Response Quality",
			Description: "Quality of response captured during live run.",
			Score1:      "Unusable response or major errors.",
			Score2:      "Poor response with significant defects.",
			Score3:      "Acceptable response with minor defects.",
			Score4:      "Good response meeting expectations.",
			Score5:      "Excellent response meeting human score of 5.",
		},
	}
}
