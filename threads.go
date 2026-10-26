package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// handleThreads serves /threads/<project>: every comment thread of a project, grouped by file.
func handleThreads(w http.ResponseWriter, r *http.Request) {
	projectDir, childPath, err := findProject(strings.TrimPrefix(r.URL.Path, "/threads"))
	if err == nil && childPath != "" {
		err = errProjectNotFound
	}
	if err != nil {
		writeFindProjectError(w, r, err)
		return
	}

	if err := syncProjectAnchors(projectDir); err != nil {
		log.Printf("Failed to update comment lines: %v", err)
	}
	open, err := getProjectComments(projectDir, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	resolved, err := getProjectComments(projectDir, true)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := renderCommentsAsHTML(open); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := renderCommentsAsHTML(resolved); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	nonce, err := newNonce()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))

	data := map[string]interface{}{
		"ProjectDir": projectDir,
		"Nonce":      nonce,
		"IsGitRepo":  isGitRepo(projectDir),
		"Open":       nonNil(open),
		"Resolved":   nonNil(resolved),
	}
	if err := templates.ExecuteTemplate(w, "threads.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func nonNil(comments []Comment) []Comment {
	if comments == nil {
		return []Comment{}
	}
	return comments
}

// threadSummary is one open thread in the viewer's thread list.
type threadSummary struct {
	ID        int    `json:"id"`
	FilePath  string `json:"file_path"`
	LineStart *int   `json:"line_start,omitempty"`
	Status    string `json:"status"` // you (the agent wrote last) or claude
	Claimed   bool   `json:"claimed,omitempty"`
	Outdated  bool   `json:"outdated,omitempty"`
	Snippet   string `json:"snippet"`
}

// maxSnippetLength bounds the comment text shown in the thread list.
const maxSnippetLength = 80

// otherThreadSummaries returns the open threads of a project outside one file. The
// viewer builds that file's own entries from its comments, which it keeps current.
func otherThreadSummaries(projectDir, filePath string) ([]threadSummary, error) {
	comments, err := getProjectComments(projectDir, false)
	if err != nil {
		return nil, err
	}
	summaries := []threadSummary{}
	for _, thread := range groupCommentsByThread(comments) {
		root := thread[0]
		if root.FilePath == filePath {
			continue
		}
		status := "claude"
		if thread[len(thread)-1].Author == "agent" {
			status = "you"
		}
		summaries = append(summaries, threadSummary{
			ID:        root.ID,
			FilePath:  root.FilePath,
			LineStart: root.LineStart,
			Status:    status,
			Claimed:   root.Claimed,
			Outdated:  root.Outdated,
			Snippet:   snippet(root.CommentText),
		})
	}
	return summaries, nil
}

// snippet returns the start of a comment's first line.
func snippet(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	runes := []rune(line)
	if len(runes) > maxSnippetLength {
		return string(runes[:maxSnippetLength-1]) + "…"
	}
	return line
}

// openThreadCount returns the number of unresolved threads in a project, for the "Threads" links.
func openThreadCount(projectDir string) int {
	counts, err := getUnresolvedThreadCounts(projectDir)
	if err != nil {
		return 0
	}
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}

// maxContextFileSize bounds the files whose lines /api/file-lines returns.
const maxContextFileSize = 2 << 20

// handleFileLines returns the lines of a text file in a registered project, so the
// threads page can show the source around a comment.
func handleFileLines(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	projectDir := query.Get("project_directory")
	filePath := query.Get("file_path")
	if projectDir == "" || filePath == "" {
		http.Error(w, "Missing project_directory or file_path", http.StatusBadRequest)
		return
	}

	registered, err := isRegisteredProject(projectDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	absPath := filepath.Join(projectDir, filePath)
	if !registered || !isWithinDir(projectDir, absPath) {
		http.NotFound(w, r)
		return
	}

	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	if info.Size() > maxContextFileSize {
		http.Error(w, "File too large to show", http.StatusRequestEntityTooLarge)
		return
	}
	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if bytes.IndexByte(content, 0) >= 0 {
		http.Error(w, "Binary file", http.StatusUnsupportedMediaType)
		return
	}

	lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string][]string{"lines": lines}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func isRegisteredProject(dir string) (bool, error) {
	projects, err := getAllProjects()
	if err != nil {
		return false, err
	}
	for _, p := range projects {
		if p.Directory == dir {
			return true, nil
		}
	}
	return false, nil
}
