package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
)

var templates *template.Template

// escapePathComponents escapes each component of a path individually,
// preserving the forward slashes between components.
func escapePathComponents(path string) string {
	parts := strings.Split(path, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}
	return strings.Join(parts, "/")
}

func initTemplates() error {
	funcMap := template.FuncMap{
		"urlquery":   url.QueryEscape,
		"pathescape": escapePathComponents,
		"base":       filepath.Base,
		"json": func(v interface{}) (template.JS, error) {
			b, err := json.Marshal(v)
			if err != nil {
				return "", err
			}
			return template.JS(b), nil
		},
	}

	// Parse templates from embedded FS
	templatesSubFS, err := fs.Sub(templatesFS, "frontend/templates")
	if err != nil {
		return err
	}

	templates, err = template.New("").Funcs(funcMap).ParseFS(templatesSubFS, "*.html")
	return err
}

// HTML Route Handlers

func handleHome(w http.ResponseWriter, r *http.Request) {
	projects, err := getAllProjects()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data := map[string]interface{}{
		"Projects": projects,
	}

	if err := templates.ExecuteTemplate(w, "index.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// findProject maps a URL path below /projects or /compare to the registered project
// it starts with and the path within that project ("" for the project root).
func findProject(urlPath string) (string, string, error) {
	// Ensure leading slash for absolute paths
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}

	// URL decode the full path (may already be decoded by Go's http server)
	decodedPath, err := url.PathUnescape(urlPath)
	if err != nil {
		return "", "", errInvalidPath
	}

	projects, err := getAllProjects()
	if err != nil {
		return "", "", err
	}

	// Find the first registered project that matches the beginning of the path
	for _, p := range projects {
		if decodedPath == p.Directory || strings.HasPrefix(decodedPath, p.Directory+"/") {
			childPath := strings.TrimPrefix(decodedPath, p.Directory)
			return p.Directory, strings.TrimPrefix(childPath, "/"), nil
		}
	}
	return "", "", errProjectNotFound
}

var (
	errInvalidPath     = errors.New("invalid path")
	errProjectNotFound = errors.New("project not found")
)

// writeFindProjectError responds to a findProject failure.
func writeFindProjectError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, errInvalidPath):
		http.Error(w, "Invalid path", http.StatusBadRequest)
	case errors.Is(err, errProjectNotFound):
		http.NotFound(w, r)
	default:
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleProjectFiles(w http.ResponseWriter, r *http.Request) {
	project, childPath, err := findProject(strings.TrimPrefix(r.URL.Path, "/projects/"))
	if err != nil {
		writeFindProjectError(w, r, err)
		return
	}

	// Build absolute path, rejecting ".." segments that escape the project
	absPath := filepath.Join(project, childPath)
	if !isWithinDir(project, absPath) {
		http.NotFound(w, r)
		return
	}

	// Check if path exists
	info, err := os.Stat(absPath)
	if err != nil {
		http.NotFound(w, r)
		return
	}

	// If directory, show markdown file listing
	if info.IsDir() {
		// Redirect to add trailing slash if needed (for proper relative URLs)
		if !strings.HasSuffix(r.URL.Path, "/") {
			http.Redirect(w, r, r.URL.Path+"/", http.StatusMovedPermanently)
			return
		}
		renderDirectoryListing(w, r, project, childPath)
		return
	}

	// If markdown file, render viewer
	if strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
		renderViewer(w, r, project, childPath)
		return
	}

	// Otherwise serve raw file
	prefix := "/projects/" + url.PathEscape(project)
	fs := http.FileServer(http.Dir(project))
	http.StripPrefix(prefix, fs).ServeHTTP(w, r)
}

func renderViewer(w http.ResponseWriter, r *http.Request, projectDir, filePath string) {
	absPath := filepath.Join(projectDir, filePath)

	// Read markdown file
	content, err := os.ReadFile(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Render markdown to HTML
	html, err := RenderMarkdownWithLineNumbers(content)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get comments for this file
	comments, err := getComments(projectDir, filePath, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Render comment markdown to HTML for web UI
	if err := renderCommentsAsHTML(comments); err != nil {
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
		"ProjectDir":  projectDir,
		"FilePath":    filePath,
		"HTMLContent": template.HTML(html),
		"Comments":    comments,
		"Nonce":       nonce,
		"IsGitRepo":   isGitRepo(projectDir),
		"OpenThreads": openThreadCount(projectDir),
	}

	if err := templates.ExecuteTemplate(w, "viewer.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

var skipDirs = map[string]bool{
	".git":          true,
	"node_modules":  true,
	"vendor":        true,
	".next":         true,
	".nuxt":         true,
	"dist":          true,
	"build":         true,
	"target":        true,
	".venv":         true,
	"venv":          true,
	"__pycache__":   true,
	".pytest_cache": true,
	".idea":         true,
	".vscode":       true,
	".DS_Store":     true,
	".direnv":       true,
	".mypy_cache":   true,
	".vim":          true,
	".ruff_cache":   true,
}

func shouldSkipDir(name string) bool {
	return skipDirs[name]
}

func hasMarkdownFiles(dirPath string) bool {
	// Use filepath.WalkDir for efficient traversal
	found := false
	_ = filepath.WalkDir(dirPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return filepath.SkipDir
		}
		// Skip common directories
		if d.IsDir() && shouldSkipDir(d.Name()) {
			return filepath.SkipDir
		}
		if !d.IsDir() && strings.HasSuffix(strings.ToLower(d.Name()), ".md") {
			found = true
			return filepath.SkipAll // Stop walking once we find one
		}
		return nil
	})
	return found
}

func renderDirectoryListing(w http.ResponseWriter, r *http.Request, projectDir, childPath string) {
	absPath := filepath.Join(projectDir, childPath)

	// Read directory contents
	entries, err := os.ReadDir(absPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Unresolved thread counts, so files awaiting attention stand out
	threadCounts, err := getUnresolvedThreadCounts(projectDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	countUnder := func(dirPath string) int {
		total := 0
		for file, count := range threadCounts {
			if strings.HasPrefix(file, dirPath+"/") {
				total += count
			}
		}
		return total
	}

	// Filter for directories and markdown files
	type Entry struct {
		Name     string
		IsDir    bool
		Path     string
		Comments int
	}

	var filteredEntries []Entry
	for _, entry := range entries {
		if entry.IsDir() {
			// Skip common directories
			if shouldSkipDir(entry.Name()) {
				continue
			}
			// Only include directories that contain markdown files
			dirFullPath := filepath.Join(absPath, entry.Name())
			if hasMarkdownFiles(dirFullPath) {
				entryPath := filepath.Join(childPath, entry.Name())
				filteredEntries = append(filteredEntries, Entry{
					Name:     entry.Name(),
					IsDir:    true,
					Path:     entryPath,
					Comments: countUnder(entryPath),
				})
			}
		} else if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			// Include only markdown files
			entryPath := filepath.Join(childPath, entry.Name())
			filteredEntries = append(filteredEntries, Entry{
				Name:     entry.Name(),
				IsDir:    false,
				Path:     entryPath,
				Comments: threadCounts[entryPath],
			})
		}
	}

	openThreads := 0
	for _, count := range threadCounts {
		openThreads += count
	}

	data := map[string]interface{}{
		"ProjectDir":  projectDir,
		"ChildPath":   childPath,
		"Entries":     filteredEntries,
		"IsGitRepo":   isGitRepo(projectDir),
		"OpenThreads": openThreads,
	}

	if err := templates.ExecuteTemplate(w, "directory.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// API Handlers

func handleCreateComment(w http.ResponseWriter, r *http.Request) {
	var comment Comment

	if err := json.NewDecoder(r.Body).Decode(&comment); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Validate required fields
	if comment.ProjectDirectory == "" {
		http.Error(w, "project_directory is required", http.StatusBadRequest)
		return
	}
	if comment.FilePath == "" {
		http.Error(w, "file_path is required", http.StatusBadRequest)
		return
	}

	// For root comments, line numbers and selected text are required
	// For replies (root_id is set), they are optional
	if comment.RootID == nil {
		if comment.LineStart == nil || *comment.LineStart <= 0 {
			http.Error(w, "line_start must be positive", http.StatusBadRequest)
			return
		}
		if comment.LineEnd == nil || *comment.LineEnd <= 0 {
			http.Error(w, "line_end must be positive", http.StatusBadRequest)
			return
		}
		if *comment.LineEnd < *comment.LineStart {
			http.Error(w, "line_end must be >= line_start", http.StatusBadRequest)
			return
		}
		if comment.SelectedText == "" {
			http.Error(w, "selected_text is required for root comments", http.StatusBadRequest)
			return
		}
	}

	if comment.CommentText == "" {
		http.Error(w, "comment_text is required", http.StatusBadRequest)
		return
	}

	// Default author to 'user' if not provided (for API calls from web UI)
	if comment.Author == "" {
		comment.Author = "user"
	}

	if err := createComment(&comment); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Render comment markdown to HTML for web UI response
	rendered, err := RenderMarkdown([]byte(comment.CommentText))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to render markdown: %v", err), http.StatusInternalServerError)
		return
	}
	comment.RenderedHTML = strings.TrimSpace(string(rendered))

	// Don't broadcast reload for comment creation - the frontend handles it locally
	// Only broadcast for external changes (CLI resolve, file updates)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(comment); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleUpdateComment(w http.ResponseWriter, r *http.Request) {
	// Extract comment ID from URL path
	commentIDStr := chi.URLParam(r, "id")

	// Parse comment ID
	var commentID int
	if _, err := fmt.Sscanf(commentIDStr, "%d", &commentID); err != nil {
		http.Error(w, "Invalid comment ID", http.StatusBadRequest)
		return
	}

	existing, err := getCommentByID(commentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if existing == nil {
		http.Error(w, "Comment not found", http.StatusNotFound)
		return
	}
	if status, msg, err := checkEditable(existing); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	} else if status != 0 {
		http.Error(w, msg, status)
		return
	}

	var req struct {
		CommentText string `json:"comment_text"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.CommentText) == "" {
		http.Error(w, "comment_text is required", http.StatusBadRequest)
		return
	}

	if err := updateComment(commentIDStr, req.CommentText); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Get the updated comment
	comment, err := getCommentByID(commentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if comment == nil {
		http.Error(w, "Comment not found", http.StatusNotFound)
		return
	}

	// Render comment markdown to HTML for web UI response
	rendered, err := RenderMarkdown([]byte(comment.CommentText))
	if err != nil {
		http.Error(w, fmt.Sprintf("failed to render markdown: %v", err), http.StatusInternalServerError)
		return
	}
	comment.RenderedHTML = strings.TrimSpace(string(rendered))

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(comment); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

const threadClaimedMessage = "Claude is processing this thread; try again once it has replied"

// isThreadClaimed reports whether the agent is processing the comment's thread.
func isThreadClaimed(c *Comment) bool {
	if c.RootID == nil {
		return c.Claimed
	}
	root, err := getCommentByID(*c.RootID)
	return err == nil && root != nil && root.Claimed
}

// checkEditable returns a non-zero HTTP status and message when the user may not
// edit the comment: only the user's own latest message in an open thread that the
// agent is not processing can be edited.
func checkEditable(c *Comment) (int, string, error) {
	if c.Author != "user" {
		return http.StatusForbidden, "Only your own comments can be edited", nil
	}
	if c.ResolvedAt != nil {
		return http.StatusBadRequest, "Cannot edit a resolved comment", nil
	}
	last, err := isLastInThread(c)
	if err != nil {
		return 0, "", err
	}
	if !last {
		return http.StatusBadRequest, "Cannot edit comment with replies", nil
	}
	if isThreadClaimed(c) {
		return http.StatusConflict, threadClaimedMessage, nil
	}
	return 0, "", nil
}

func handleDeleteComment(w http.ResponseWriter, r *http.Request) {
	// Extract comment ID from URL path
	commentID := chi.URLParam(r, "id")

	// Refuse to delete from a thread the agent is processing
	var id int
	if _, err := fmt.Sscanf(commentID, "%d", &id); err == nil {
		if comment, err := getCommentByID(id); err == nil && comment != nil && isThreadClaimed(comment) {
			http.Error(w, threadClaimedMessage, http.StatusConflict)
			return
		}
	}

	if err := deleteComment(commentID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]string{"status": "deleted"}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func handleResolveThread(w http.ResponseWriter, r *http.Request) {
	// Extract comment ID from URL path
	commentIDStr := chi.URLParam(r, "id")

	// Parse comment ID
	var commentID int
	if _, err := fmt.Sscanf(commentIDStr, "%d", &commentID); err != nil {
		http.Error(w, "Invalid comment ID", http.StatusBadRequest)
		return
	}

	// Get comment to retrieve project_directory and file_path for SSE broadcast
	comment, err := getCommentByID(commentID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if comment == nil {
		http.Error(w, "Comment not found", http.StatusNotFound)
		return
	}

	// Resolve the thread (marked as resolved by 'user' since it's from web UI)
	count, err := resolveThread(commentID, "user")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	// Don't broadcast reload for web UI resolution - the frontend handles it locally
	// Only broadcast for CLI resolution (via notify endpoint)

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"status": "resolved",
		"count":  count,
	}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
