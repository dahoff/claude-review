package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// commentPlacement says where the compare page shows a comment thread: after the
// right-side row of a line, under its file's header, or among other files' comments.
type commentPlacement struct {
	File  string `json:"file"`
	Line  int    `json:"line,omitempty"`
	Where string `json:"where"` // line, file, other
}

// placeComments places each root comment, keyed by its ID.
func placeComments(files []ChangedFile, rows map[string][]DiffRow, comments []Comment, readOnly bool) map[int]commentPlacement {
	inDiff := map[string]bool{}
	for _, f := range files {
		inDiff[f.Path] = true
	}

	placements := map[int]commentPlacement{}
	for _, c := range comments {
		if c.RootID != nil {
			continue
		}
		p := commentPlacement{File: c.FilePath, Where: "other"}
		if inDiff[c.FilePath] {
			p.Where = "file"
			if !readOnly && c.LineEnd != nil && hasNewLine(rows[c.FilePath], *c.LineEnd) {
				p.Where = "line"
				p.Line = *c.LineEnd
			}
		}
		placements[c.ID] = p
	}
	return placements
}

func hasNewLine(rows []DiffRow, line int) bool {
	for _, r := range rows {
		if r.NewLine == line {
			return true
		}
	}
	return false
}

// compareFile is one file section of the compare page.
type compareFile struct {
	ChangedFile
	ID        string
	Segments  []DiffSegment
	Collapsed bool
}

func handleCompare(w http.ResponseWriter, r *http.Request) {
	projectDir, childPath, err := findProject(strings.TrimPrefix(r.URL.Path, "/compare"))
	if err == nil && childPath != "" {
		err = errProjectNotFound
	}
	if err != nil {
		writeFindProjectError(w, r, err)
		return
	}

	nonce, err := newNonce()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy(nonce))

	gitRepo := isGitRepo(projectDir)
	data := map[string]interface{}{
		"ProjectDir": projectDir,
		"Nonce":      nonce,
		"IsGitRepo":  gitRepo,
	}
	if !gitRepo {
		renderCompare(w, data)
		return
	}

	query := r.URL.Query()
	comparison, err := resolveComparison(projectDir, query.Get("mode"), query.Get("base"), query.Get("commit"))
	if errors.Is(err, errInvalidComparison) {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	changed, err := listChangedFiles(projectDir, comparison)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	rows := map[string][]DiffRow{}
	for _, f := range changed {
		if rows[f.Path], err = fileRows(projectDir, comparison, f); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	comments, err := getProjectComments(projectDir, false)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := renderCommentsAsHTML(comments); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	placements := placeComments(changed, rows, comments, comparison.ReadOnly)

	// Keep commented lines visible when folding unchanged lines away
	keep := map[string]map[int]bool{}
	for _, p := range placements {
		if p.Where == "line" {
			if keep[p.File] == nil {
				keep[p.File] = map[int]bool{}
			}
			keep[p.File][p.Line] = true
		}
	}

	files := make([]compareFile, len(changed))
	watchPaths := []string{}
	additions, deletions := 0, 0
	for i, f := range changed {
		files[i] = compareFile{
			ChangedFile: f,
			ID:          fmt.Sprintf("file-%d", i),
			Segments:    foldRows(rows[f.Path], keep[f.Path], diffContextLines),
			Collapsed:   f.Additions+f.Deletions > largeDiffLines,
		}
		if f.Status != "deleted" {
			watchPaths = append(watchPaths, f.Path)
		}
		additions += f.Additions
		deletions += f.Deletions
	}

	branches, err := localBranches(projectDir)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	commits, err := recentCommits(projectDir, recentCommitLimit)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	data["Comparison"] = comparison
	data["CurrentBranch"] = currentBranch(projectDir)
	data["DefaultBase"] = defaultBaseBranch(projectDir)
	data["Branches"] = branches
	data["Commits"] = commits
	data["Files"] = files
	data["Additions"] = additions
	data["Deletions"] = deletions
	data["Comments"] = comments
	data["Placements"] = placements
	data["WatchPaths"] = watchPaths
	renderCompare(w, data)
}

// recentCommitLimit bounds the commit picker on the compare page.
const recentCommitLimit = 50

func renderCompare(w http.ResponseWriter, data map[string]interface{}) {
	if err := templates.ExecuteTemplate(w, "compare.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
