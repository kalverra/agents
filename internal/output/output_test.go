package output

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func captureStdout(fn func()) string {
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	fn()

	_ = w.Close()
	os.Stdout = oldStdout

	var buf bytes.Buffer
	_, _ = io.Copy(&buf, r)
	return buf.String()
}

func TestWrite_AIMode_StringOutputRawWithoutFences(t *testing.T) { //nolint:paralleltest
	SetJSON(true)
	defer SetJSON(false)

	raw := `<status repo="acme/api" branch="main"/>`
	got := captureStdout(func() {
		Write("status", raw, nil)
	})

	assert.Equal(t, raw+"\n", got)
	assert.NotContains(t, got, "```")
	assert.NotContains(t, got, "```json")
}

func TestWrite_AIMode_CompactJSONWithoutFences(t *testing.T) { //nolint:paralleltest
	SetJSON(true)
	defer SetJSON(false)

	data := map[string]string{"key": "value"}
	got := captureStdout(func() {
		Write("cmd", data, nil)
	})

	assert.NotContains(t, got, "```")
	assert.NotContains(t, got, "```json")
	assert.Contains(t, got, `{"key":"value"}`)
}
