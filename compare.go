package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
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
func placeComments(
	files []ChangedFile,
	rows map[string][]DiffRow,
	comments []Comment,
	readOnly bool,
) map[int]commentPlacement {
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
	Comments  int // unresolved threads shown in this section
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
		"ProjectDir":  projectDir,
		"Nonce":       nonce,
		"IsGitRepo":   gitRepo,
		"OpenThreads": openThreadCount(projectDir),
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
	for i, f := range changed {
		// One unreadable file shouldn't hide the rest of the changes
		if rows[f.Path], err = fileRows(projectDir, comparison, f); err != nil {
			log.Printf("Failed to diff %s: %v", f.Path, err)
			changed[i].Skipped = "unreadable"
		}
	}

	if err := syncProjectAnchors(projectDir); err != nil {
		log.Printf("Failed to update comment lines: %v", err)
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
	threadCounts := map[string]int{}
	for _, p := range placements {
		if p.Where != "other" {
			threadCounts[p.File]++
		}
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
			Comments:    threadCounts[f.Path],
			// Never hide a file's comments behind a collapsed section
			Collapsed: f.Additions+f.Deletions > largeDiffLines && threadCounts[f.Path] == 0,
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
	// Since commit starts from HEAD's parent, so the link shows the latest commit plus uncommitted work
	if len(commits) > 1 {
		data["SinceDefault"] = commits[1].SHA
	} else if len(commits) == 1 {
		data["SinceDefault"] = commits[0].SHA
	}
	// Show the diffs in the same order as the file tree
	tree := buildFileTree(files)
	data["Files"] = treeFiles(tree)
	data["FileTree"] = tree
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

// fileTreeNode is a folder or file in the compare page's file tree.
type fileTreeNode struct {
	Name     string
	Dir      bool
	File     *compareFile // files only
	Children []fileTreeNode
}

// buildFileTree arranges files into folders, folders before files, merging folders
// that only hold one folder ("src/app") the way GitHub's PR file tree does.
func buildFileTree(files []compareFile) []fileTreeNode {
	root := &fileTreeNode{Dir: true}
	for i := range files {
		// An untracked nested repository is listed as "dir/"; keep it as one entry
		parts := strings.Split(strings.TrimSuffix(files[i].Path, "/"), "/")
		if strings.HasSuffix(files[i].Path, "/") {
			parts[len(parts)-1] += "/"
		}

		node := root
		for _, dir := range parts[:len(parts)-1] {
			node = childDir(node, dir)
		}
		node.Children = append(node.Children, fileTreeNode{Name: parts[len(parts)-1], File: &files[i]})
	}
	return compactTree(root.Children)
}

func childDir(node *fileTreeNode, name string) *fileTreeNode {
	for i := range node.Children {
		if node.Children[i].Dir && node.Children[i].Name == name {
			return &node.Children[i]
		}
	}
	node.Children = append(node.Children, fileTreeNode{Name: name, Dir: true})
	return &node.Children[len(node.Children)-1]
}

func compactTree(nodes []fileTreeNode) []fileTreeNode {
	for i := range nodes {
		if !nodes[i].Dir {
			continue
		}
		for len(nodes[i].Children) == 1 && nodes[i].Children[0].Dir {
			nodes[i].Name += "/" + nodes[i].Children[0].Name
			nodes[i].Children = nodes[i].Children[0].Children
		}
		nodes[i].Children = compactTree(nodes[i].Children)
	}
	sort.SliceStable(nodes, func(a, b int) bool {
		if nodes[a].Dir != nodes[b].Dir {
			return nodes[a].Dir
		}
		return nodes[a].Name < nodes[b].Name
	})
	return nodes
}

// treeFiles lists the files of a tree in display order.
func treeFiles(nodes []fileTreeNode) []compareFile {
	var files []compareFile
	for _, n := range nodes {
		if n.Dir {
			files = append(files, treeFiles(n.Children)...)
		} else {
			files = append(files, *n.File)
		}
	}
	return files
}
