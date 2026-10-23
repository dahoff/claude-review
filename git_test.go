package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestRepo creates an empty git repository on branch main.
func newTestRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	// Resolve symlinks (e.g. /tmp on macOS) so paths match git's output
	dir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	git(t, dir, "init", "-q", "-b", "main")
	return dir
}

// git runs a git command in dir and returns its trimmed output.
func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command(
		"git",
		append([]string{"-c", "user.name=test", "-c", "user.email=test@example.com"}, args...)...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))
	return strings.TrimSpace(string(out))
}

func writeFile(t *testing.T, dir, path, content string) {
	t.Helper()
	full := filepath.Join(dir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
}

// commitFile writes a file and commits it, returning the new commit's SHA.
func commitFile(t *testing.T, dir, path, content, msg string) string {
	t.Helper()
	writeFile(t, dir, path, content)
	git(t, dir, "add", "--", path)
	git(t, dir, "commit", "-q", "-m", msg)
	return git(t, dir, "rev-parse", "HEAD")
}

func TestResolveComparison_Uncommitted(t *testing.T) {
	dir := newTestRepo(t)
	head := commitFile(t, dir, "a.txt", "a\n", "first")

	c, err := resolveComparison(dir, ModeUncommitted, "", "")
	require.NoError(t, err)
	assert.Equal(t, ModeUncommitted, c.Mode)
	assert.Equal(t, head, c.BaseRev)
	assert.Equal(t, "", c.TargetRev)
	assert.False(t, c.ReadOnly)
}

func TestResolveComparison_UnbornHead(t *testing.T) {
	dir := newTestRepo(t)
	writeFile(t, dir, "a.txt", "a\n")

	c, err := resolveComparison(dir, ModeUncommitted, "", "")
	require.NoError(t, err)
	assert.Equal(t, emptyTreeSHA, c.BaseRev)

	c, err = resolveComparison(dir, ModeBase, "", "")
	require.NoError(t, err)
	assert.Equal(t, ModeUncommitted, c.Mode)
	assert.NotEmpty(t, c.Notice)
}

func TestResolveComparison_Base(t *testing.T) {
	dir := newTestRepo(t)
	fork := commitFile(t, dir, "a.txt", "a\n", "first")
	git(t, dir, "checkout", "-q", "-b", "feat")
	commitFile(t, dir, "b.txt", "b\n", "feature work")
	git(t, dir, "checkout", "-q", "main")
	commitFile(t, dir, "c.txt", "c\n", "main moves on")
	git(t, dir, "checkout", "-q", "feat")

	c, err := resolveComparison(dir, ModeBase, "main", "")
	require.NoError(t, err)
	assert.Equal(t, ModeBase, c.Mode)
	assert.Equal(t, "main", c.BaseBranch)
	assert.Equal(t, fork, c.BaseRev)
	assert.Equal(t, "", c.TargetRev)
}

func TestResolveComparison_Commit(t *testing.T) {
	dir := newTestRepo(t)
	first := commitFile(t, dir, "a.txt", "a\n", "first")
	second := commitFile(t, dir, "a.txt", "b\n", "second")

	c, err := resolveComparison(dir, ModeCommit, "", second[:7])
	require.NoError(t, err)
	assert.Equal(t, ModeCommit, c.Mode)
	assert.Equal(t, second, c.Commit)
	assert.Equal(t, first, c.BaseRev)
	assert.Equal(t, second, c.TargetRev)
	assert.True(t, c.ReadOnly)
}

func TestResolveComparison_RootCommit(t *testing.T) {
	dir := newTestRepo(t)
	first := commitFile(t, dir, "a.txt", "a\n", "first")

	c, err := resolveComparison(dir, ModeCommit, "", first)
	require.NoError(t, err)
	assert.Equal(t, emptyTreeSHA, c.BaseRev)
	assert.Equal(t, first, c.TargetRev)
}

func TestResolveComparison_DefaultMode(t *testing.T) {
	dir := newTestRepo(t)
	commitFile(t, dir, "a.txt", "a\n", "first")

	c, err := resolveComparison(dir, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, ModeUncommitted, c.Mode)

	git(t, dir, "checkout", "-q", "-b", "feat")
	commitFile(t, dir, "b.txt", "b\n", "feature work")

	c, err = resolveComparison(dir, "", "", "")
	require.NoError(t, err)
	assert.Equal(t, ModeBase, c.Mode)
	assert.Equal(t, "main", c.BaseBranch)
}

func TestResolveComparison_InvalidInput(t *testing.T) {
	dir := newTestRepo(t)
	commitFile(t, dir, "a.txt", "a\n", "first")
	outFile := filepath.Join(t.TempDir(), "x")

	tests := []struct {
		name, mode, base, commit string
	}{
		{"unknown mode", "bogus", "", ""},
		{"unknown base", ModeBase, "nope", ""},
		{"path-like base", ModeBase, "../../etc", ""},
		{"option-like commit", ModeCommit, "", "--output=" + outFile},
		{"nonexistent commit", ModeCommit, "", "deadbeef"},
		{"empty commit", ModeCommit, "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := resolveComparison(dir, tt.mode, tt.base, tt.commit)
			assert.True(t, errors.Is(err, errInvalidComparison), "got %v", err)
		})
	}
	assert.NoFileExists(t, outFile)
}

func TestDefaultBaseBranch(t *testing.T) {
	dir := t.TempDir()
	git(t, dir, "init", "-q", "-b", "master")
	commitFile(t, dir, "a.txt", "a\n", "first")
	assert.Equal(t, "master", defaultBaseBranch(dir))

	git(t, dir, "branch", "main")
	assert.Equal(t, "main", defaultBaseBranch(dir))
}

func TestRecentCommits(t *testing.T) {
	dir := newTestRepo(t)
	assert.Empty(t, mustRecentCommits(t, dir))

	commitFile(t, dir, "a.txt", "1\n", "one")
	commitFile(t, dir, "a.txt", "2\n", "two")
	third := commitFile(t, dir, "a.txt", "3\n", "three")

	commits := mustRecentCommits(t, dir)
	require.Len(t, commits, 3)
	assert.Equal(t, third, commits[0].SHA)
	assert.Equal(t, third[:len(commits[0].ShortSHA)], commits[0].ShortSHA)
	assert.Equal(
		t,
		[]string{"three", "two", "one"},
		[]string{commits[0].Subject, commits[1].Subject, commits[2].Subject},
	)
}

func mustRecentCommits(t *testing.T, dir string) []CommitInfo {
	t.Helper()
	commits, err := recentCommits(dir, 50)
	require.NoError(t, err)
	return commits
}
