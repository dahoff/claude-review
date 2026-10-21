package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2E_Security_CrossOriginRequestsRejected(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	body := fmt.Sprintf(
		`{"project_directory":%q,"file_path":"test.md","line_start":1,"line_end":1,"selected_text":"Test","comment_text":"injected"}`,
		env.ProjectDir,
	)

	t.Run("foreign origin", func(t *testing.T) {
		// A "simple" cross-site request a malicious page could send without a preflight
		req, err := http.NewRequest(http.MethodPost, env.BaseURL+"/api/comments", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "text/plain")
		req.Header.Set("Origin", "http://evil.example")

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("foreign host (DNS rebinding)", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodGet, env.BaseURL+"/", nil)
		require.NoError(t, err)
		req.Host = "evil.example:" + env.Port

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("same origin allowed", func(t *testing.T) {
		req, err := http.NewRequest(http.MethodPost, env.BaseURL+"/api/comments", strings.NewReader(body))
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", env.BaseURL)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	output, err := env.runCLI(t, "address", "--file", "test.md", "--project", env.ProjectDir)
	require.NoError(t, err)
	assert.Equal(t, 1, strings.Count(output, "injected"), "only the same-origin comment should be stored")
}

func TestE2E_Security_PathTraversalRejected(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	// A markdown file outside the registered project
	require.NoError(t, os.WriteFile(filepath.Join(env.TempDir, "outside.md"), []byte("# Outside secret\n"), 0644))

	// Raw request so ".." segments reach the server unnormalized
	for _, path := range []string{
		"/projects" + env.ProjectDir + "/../outside.md",
		"/projects" + env.ProjectDir + "/%2e%2e/outside.md",
	} {
		req, err := http.NewRequest(http.MethodGet, env.BaseURL+"/", nil)
		require.NoError(t, err)
		req.URL.Opaque = path

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		content, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode, path)
		assert.NotContains(t, string(content), "Outside secret", path)
	}
}

func TestE2E_Security_ScriptsBlocked(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	t.Run("viewer sends nonce-based CSP", func(t *testing.T) {
		resp, err := http.Get(env.BaseURL + "/projects" + env.ProjectDir + "/test.md")
		require.NoError(t, err)
		content, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		csp := resp.Header.Get("Content-Security-Policy")
		assert.Contains(t, csp, "script-src 'self' 'nonce-")
		assert.NotContains(t, csp, "unsafe-inline")

		match := regexp.MustCompile(`'nonce-([0-9a-f]+)'`).FindStringSubmatch(csp)
		require.Len(t, match, 2)
		nonce := match[1]
		assert.Contains(t, string(content), fmt.Sprintf(`<script nonce="%s">`, nonce))
	})

	t.Run("raw HTML in comments is omitted", func(t *testing.T) {
		resp := env.postJSON(t, "/api/comments", map[string]interface{}{
			"project_directory": env.ProjectDir,
			"file_path":         "test.md",
			"line_start":        1,
			"line_end":          1,
			"selected_text":     "Test",
			"comment_text":      "hello <img src=x onerror=alert(1)>",
		})
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)

		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		rendered := created["rendered_html"].(string)
		assert.Contains(t, rendered, "hello")
		assert.NotContains(t, rendered, "onerror")
	})
}

func TestE2E_Address_Pending(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	newComment := func(text string) int {
		resp := env.postJSON(t, "/api/comments", map[string]interface{}{
			"project_directory": env.ProjectDir,
			"file_path":         "test.md",
			"line_start":        1,
			"line_end":          1,
			"selected_text":     "Test",
			"comment_text":      text,
		})
		defer func() { _ = resp.Body.Close() }()
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		return int(created["id"].(float64))
	}

	answeredID := newComment("Already answered")
	newComment("Still waiting")
	_, err = env.runCLI(t, "reply", "--comment-id", fmt.Sprintf("%d", answeredID), "--message", "Done")
	require.NoError(t, err)

	output, err := env.runCLI(t, "address", "--pending", "--file", "test.md", "--project", env.ProjectDir)
	require.NoError(t, err)
	assert.Contains(t, output, "Still waiting")
	assert.NotContains(t, output, "Already answered")

	// Without --pending, all unresolved threads are shown
	output, err = env.runCLI(t, "address", "--file", "test.md", "--project", env.ProjectDir)
	require.NoError(t, err)
	assert.Contains(t, output, "Already answered")
}
