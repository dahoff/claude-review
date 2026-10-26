package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createRootComment adds a comment on a line of a file and returns its ID.
func createRootComment(t *testing.T, env *TestEnv, file string, line int, text string) int {
	t.Helper()
	resp := env.postJSON(t, "/api/comments", map[string]interface{}{
		"project_directory": env.ProjectDir,
		"file_path":         file,
		"line_start":        line,
		"line_end":          line,
		"selected_text":     "selected",
		"comment_text":      text,
	})
	defer func() { _ = resp.Body.Close() }()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var created struct {
		ID int `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
	return created.ID
}

func getPage(t *testing.T, u string) (int, string) {
	t.Helper()
	resp, err := http.Get(u)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp.StatusCode, string(body)
}

func TestE2E_ThreadsPage(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	waiting := createRootComment(t, env, "test.md", 3, "Waiting on Claude")
	answered := createRootComment(t, env, "simple.md", 1, "Answered by Claude")
	done := createRootComment(t, env, "test.md", 5, "Already done")
	_, err = env.runCLI(t, "reply", "--comment-id", fmt.Sprint(answered), "--message", "Agent answer")
	require.NoError(t, err)
	_, err = env.runCLI(t, "resolve", "--comment-id", fmt.Sprint(done))
	require.NoError(t, err)

	t.Run("lists open and resolved threads", func(t *testing.T) {
		status, body := getPage(t, env.BaseURL+"/threads"+env.ProjectDir)
		require.Equal(t, http.StatusOK, status)
		assert.Contains(t, body, `src="/static/threads.js"`)

		var open, resolved []struct {
			ID     int    `json:"id"`
			RootID *int   `json:"root_id"`
			Author string `json:"author"`
			HTML   string `json:"rendered_html"`
		}
		require.NoError(t, json.Unmarshal([]byte(bootstrapValue(t, body, "resolvedComments")), &resolved))
		openJSON := strings.TrimPrefix(body[strings.Index(body, "let openComments = "):], "let openComments = ")
		require.NoError(t, json.Unmarshal([]byte(openJSON[:strings.Index(openJSON, ";\n")]), &open))

		openIDs := map[int]bool{}
		for _, c := range open {
			if c.RootID == nil {
				openIDs[c.ID] = true
			} else {
				assert.Equal(t, answered, *c.RootID)
				assert.Equal(t, "agent", c.Author)
			}
			assert.NotEmpty(t, c.HTML, "comments are rendered for the page")
		}
		assert.Equal(t, map[int]bool{waiting: true, answered: true}, openIDs)
		require.Len(t, resolved, 1)
		assert.Equal(t, done, resolved[0].ID)
	})

	t.Run("project pages link to it with the open thread count", func(t *testing.T) {
		_, body := getPage(t, env.BaseURL+"/projects"+env.ProjectDir+"/")
		assert.Contains(t, body, `href="/threads`+env.ProjectDir+`"`)
		assert.Contains(t, body, `Threads <span class="link-count">2</span>`)

		_, body = getPage(t, env.BaseURL+"/projects"+env.ProjectDir+"/test.md")
		assert.Contains(t, body, `Threads <span class="link-count">2</span>`)
	})

	t.Run("only serves registered project roots", func(t *testing.T) {
		status, _ := getPage(t, env.BaseURL+"/threads"+env.ProjectDir+"/test.md")
		assert.Equal(t, http.StatusNotFound, status)
		status, _ = getPage(t, env.BaseURL+"/threads"+env.TempDir)
		assert.Equal(t, http.StatusNotFound, status)
	})
}

func TestE2E_FileLines(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	require.NoError(t, os.WriteFile(filepath.Join(env.TempDir, "secret.txt"), []byte("SECRET DATA\n"), 0644))
	require.NoError(t, os.WriteFile(filepath.Join(env.ProjectDir, "data.bin"), []byte{0, 1, 2}, 0644))

	fileLines := func(project, file string) (int, string) {
		params := url.Values{"project_directory": {project}, "file_path": {file}}
		return getPage(t, env.BaseURL+"/api/file-lines?"+params.Encode())
	}

	status, body := fileLines(env.ProjectDir, "simple.md")
	require.Equal(t, http.StatusOK, status)
	var result struct {
		Lines []string `json:"lines"`
	}
	require.NoError(t, json.Unmarshal([]byte(body), &result))
	assert.Equal(t, []string{"# Simple", "", "Just one paragraph."}, result.Lines)

	for name, tc := range map[string]struct {
		project, file string
		status        int
	}{
		"missing file":         {env.ProjectDir, "nope.md", http.StatusNotFound},
		"directory":            {env.ProjectDir, ".", http.StatusNotFound},
		"path traversal":       {env.ProjectDir, "../secret.txt", http.StatusNotFound},
		"unregistered project": {env.TempDir, "secret.txt", http.StatusNotFound},
		"binary file":          {env.ProjectDir, "data.bin", http.StatusUnsupportedMediaType},
		"missing parameters":   {env.ProjectDir, "", http.StatusBadRequest},
	} {
		t.Run(name, func(t *testing.T) {
			status, body := fileLines(tc.project, tc.file)
			assert.Equal(t, tc.status, status)
			assert.NotContains(t, body, "SECRET DATA")
		})
	}
}
