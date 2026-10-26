package git

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReadUntrackedFileNotFound(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	fe, err := readUntrackedFile(dir, "does_not_exist.txt")
	require.Error(t, err, "readUntrackedFile should return error when file is unreadable")
	assert.Equal(t, fileEntry{}, fe, "fileEntry should be zero-value on error")
}

func TestReadUntrackedFileText(t *testing.T) {
	t.Parallel()

	dir := initTestRepo(t)
	writeFile(t, dir, "new.txt", "hello\nworld\n")

	fe, err := readUntrackedFile(dir, "new.txt")
	require.NoError(t, err)
	assert.Equal(t, "added", fe.status)
	assert.Equal(t, 2, fe.additions)
	assert.Contains(t, fe.patch, "+hello")
	assert.Contains(t, fe.patch, "+world")
}
