package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupGitProject turns the test project into a registered git repository with one commit on main.
func setupGitProject(t *testing.T, env *TestEnv) {
	t.Helper()
	runGit(t, env.ProjectDir, "init", "-q", "-b", "main")
	runGit(t, env.ProjectDir, "add", "-A")
	runGit(t, env.ProjectDir, "commit", "-q", "-m", "init")
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)
}

func gitHead(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}

func writeProjectFile(t *testing.T, env *TestEnv, path, content string) {
	t.Helper()
	full := filepath.Join(env.ProjectDir, path)
	require.NoError(t, os.MkdirAll(filepath.Dir(full), 0755))
	require.NoError(t, os.WriteFile(full, []byte(content), 0644))
}

func commitAll(t *testing.T, env *TestEnv, msg string) {
	t.Helper()
	runGit(t, env.ProjectDir, "add", "-A")
	runGit(t, env.ProjectDir, "commit", "-q", "-m", msg)
}

// getCompare fetches the compare page for the test project with the given query string.
func getCompare(t *testing.T, env *TestEnv, query string) (int, string) {
	t.Helper()
	u := env.BaseURL + "/compare" + env.ProjectDir
	if query != "" {
		u += "?" + query
	}
	resp, err := http.Get(u)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

// bootstrapValue extracts the JSON assigned to a bootstrap constant on the compare page.
func bootstrapValue(t *testing.T, body, name string) string {
	t.Helper()
	prefix := "const " + name + " = "
	start := strings.Index(body, prefix)
	require.GreaterOrEqual(t, start, 0, "bootstrap %s not found", name)
	rest := body[start+len(prefix):]
	return rest[:strings.Index(rest, ";\n")]
}

type placement struct {
	File  string `json:"file"`
	Line  int    `json:"line"`
	Where string `json:"where"`
}

func placementsOf(t *testing.T, body string) map[string]placement {
	t.Helper()
	var p map[string]placement
	require.NoError(t, json.Unmarshal([]byte(bootstrapValue(t, body, "placements")), &p))
	return p
}

func createComment(t *testing.T, env *TestEnv, file string, line int) int {
	t.Helper()
	resp := env.postJSON(t, "/api/comments", map[string]interface{}{
		"project_directory": env.ProjectDir,
		"file_path":         file,
		"line_start":        line,
		"line_end":          line,
		"selected_text":     "text",
		"comment_text":      "a comment",
	})
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var c struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&c))
	return c.ID
}

func TestE2E_Compare_Uncommitted(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)
	writeProjectFile(t, env, "test.md", "# Changed Document\n")
	writeProjectFile(t, env, "x.go", "package x\n")

	status, body := getCompare(t, env, "mode=uncommitted")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `data-path="test.md"`)
	assert.Contains(t, body, `data-path="x.go"`)
	assert.NotContains(t, body, `data-path="simple.md"`)
	assert.Contains(t, body, "diff-change")
	assert.Contains(t, body, "Changed Document")
	assert.Contains(t, body, `class="diff-status diff-status-added"`)
	assert.Contains(t, body, `class="diff-status diff-status-modified"`)
	assert.Equal(t, `"uncommitted"`, bootstrapValue(t, body, "compareMode"))
}

func TestE2E_Compare_SinceBase(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)
	runGit(t, env.ProjectDir, "checkout", "-q", "-b", "feat")
	writeProjectFile(t, env, "feature.go", "package feature\n")
	commitAll(t, env, "feature")
	runGit(t, env.ProjectDir, "checkout", "-q", "main")
	writeProjectFile(t, env, "mainonly.go", "package mainonly\n")
	commitAll(t, env, "main moves on")
	runGit(t, env.ProjectDir, "checkout", "-q", "feat")
	writeProjectFile(t, env, "simple.md", "# Simple, edited\n")

	status, body := getCompare(t, env, "mode=base&base=main")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `data-path="feature.go"`)
	assert.Contains(t, body, `data-path="simple.md"`)
	assert.NotContains(t, body, `data-path="mainonly.go"`)
}

func TestE2E_Compare_Commit(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)
	writeProjectFile(t, env, "one.go", "package one\n")
	commitAll(t, env, "one")
	sha := gitHead(t, env.ProjectDir)
	writeProjectFile(t, env, "two.go", "package two\n")
	commitAll(t, env, "two")

	status, body := getCompare(t, env, "mode=commit&commit="+sha)
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, `data-path="one.go"`)
	assert.NotContains(t, body, `data-path="two.go"`)
	assert.Equal(t, "true", bootstrapValue(t, body, "readOnly"))
}

func TestE2E_Compare_DefaultMode(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)
	runGit(t, env.ProjectDir, "checkout", "-q", "-b", "feat")
	writeProjectFile(t, env, "feature.go", "package feature\n")
	commitAll(t, env, "feature")

	status, body := getCompare(t, env, "")
	require.Equal(t, http.StatusOK, status)
	assert.Equal(t, `"base"`, bootstrapValue(t, body, "compareMode"))
	assert.Contains(t, body, `data-path="feature.go"`)
}

func TestE2E_Compare_InvalidQuery(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)
	outFile := filepath.Join(env.TempDir, "x")

	for _, query := range []string{
		"mode=bogus",
		"mode=base&base=nope",
		"mode=base&base=" + url.QueryEscape("../../etc"),
		"mode=commit&commit=" + url.QueryEscape("--output="+outFile),
	} {
		status, _ := getCompare(t, env, query)
		assert.Equal(t, http.StatusBadRequest, status, query)
	}
	assert.NoFileExists(t, outFile)
}

func TestE2E_Compare_NotGitRepo(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	status, body := getCompare(t, env, "")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "not a git repository")

	resp, err := http.Get(fmt.Sprintf("%s/projects%s/", env.BaseURL, env.ProjectDir))
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	listing, _ := io.ReadAll(resp.Body)
	assert.NotContains(t, string(listing), "changes-link")
}

func TestE2E_Compare_UnregisteredPath(t *testing.T) {
	env := setupE2E(t)

	status, _ := getCompare(t, env, "")
	assert.Equal(t, http.StatusNotFound, status)
}

func TestE2E_Compare_NoChanges(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)

	status, body := getCompare(t, env, "mode=uncommitted")
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "No changes")
}

func TestE2E_Compare_CommentPlacement(t *testing.T) {
	env := setupE2E(t)
	writeProjectFile(t, env, "gone.go", "package gone\n")
	setupGitProject(t, env)
	require.NoError(t, os.Remove(filepath.Join(env.ProjectDir, "gone.go")))
	writeProjectFile(t, env, "test.md", "# Changed\n\nsecond line\n")

	onDeleted := createComment(t, env, "gone.go", 1)
	onUnchanged := createComment(t, env, "simple.md", 1)
	onLine := createComment(t, env, "test.md", 3)

	status, body := getCompare(t, env, "mode=uncommitted")
	require.Equal(t, http.StatusOK, status)
	p := placementsOf(t, body)
	assert.Equal(t, placement{File: "gone.go", Where: "file"}, p[fmt.Sprint(onDeleted)])
	assert.Equal(t, placement{File: "simple.md", Where: "other"}, p[fmt.Sprint(onUnchanged)])
	assert.Equal(t, placement{File: "test.md", Line: 3, Where: "line"}, p[fmt.Sprint(onLine)])
}

func TestE2E_Compare_ChangesLink(t *testing.T) {
	env := setupE2E(t)
	setupGitProject(t, env)

	for _, path := range []string{"/", "/test.md"} {
		resp, err := http.Get(env.BaseURL + "/projects" + env.ProjectDir + path)
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		assert.Contains(t, string(body), `class="changes-link"`, path)
	}
}
