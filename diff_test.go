package main

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseUnifiedRows_Pairing(t *testing.T) {
	diff := "@@ -1,4 +1,4 @@\n a\n-b\n-c\n+B\n d\n+e\n"

	assert.Equal(t, []DiffRow{
		{Kind: "context", OldLine: 1, NewLine: 1, OldText: "a", NewText: "a"},
		{Kind: "change", OldLine: 2, NewLine: 2, OldText: "b", NewText: "B"},
		{Kind: "del", OldLine: 3, OldText: "c"},
		{Kind: "context", OldLine: 4, NewLine: 3, OldText: "d", NewText: "d"},
		{Kind: "add", NewLine: 4, NewText: "e"},
	}, parseUnifiedRows(diff))
}

func TestParseUnifiedRows_IgnoresHeadersAndNoNewlineMarker(t *testing.T) {
	diff := "diff --git a/x b/x\nindex 1..2 100644\n--- a/x\n+++ b/x\n@@ -1 +1 @@\n-old\n\\ No newline at end of file\n+new\n\\ No newline at end of file\n"

	assert.Equal(t, []DiffRow{
		{Kind: "change", OldLine: 1, NewLine: 1, OldText: "old", NewText: "new"},
	}, parseUnifiedRows(diff))
}

func contextRows(n int) []DiffRow {
	rows := make([]DiffRow, n)
	for i := range rows {
		rows[i] = DiffRow{Kind: "context", OldLine: i + 1, NewLine: i + 1}
	}
	return rows
}

// visibleNewLines lists the NewLine of every row in a visible segment.
func visibleNewLines(segments []DiffSegment) []int {
	var lines []int
	for _, s := range segments {
		if !s.Hidden {
			for _, r := range s.Rows {
				lines = append(lines, r.NewLine)
			}
		}
	}
	return lines
}

func TestFoldRows(t *testing.T) {
	rows := contextRows(20)
	rows[10].Kind = "add"

	segments := foldRows(rows, nil, 3)
	require.Len(t, segments, 3)
	assert.True(t, segments[0].Hidden)
	assert.False(t, segments[1].Hidden)
	assert.True(t, segments[2].Hidden)
	assert.Equal(t, []int{8, 9, 10, 11, 12, 13, 14}, visibleNewLines(segments))

	// Rows around a kept line stay visible too
	segments = foldRows(rows, map[int]bool{18: true}, 3)
	assert.Equal(t, []int{8, 9, 10, 11, 12, 13, 14, 15, 16, 17, 18, 19, 20}, visibleNewLines(segments))

	// A hidden run shorter than 4 rows is shown instead
	rows = contextRows(9)
	rows[0].Kind = "add"
	rows[8].Kind = "add"
	segments = foldRows(rows, nil, 2)
	require.Len(t, segments, 1)
	assert.False(t, segments[0].Hidden)
}

func changedByPath(files []ChangedFile) map[string]ChangedFile {
	m := map[string]ChangedFile{}
	for _, f := range files {
		m[f.Path] = f
	}
	return m
}

func TestListChangedFiles_WorkingTree(t *testing.T) {
	dir := newTestRepo(t)
	writeFile(t, dir, ".gitignore", "ignored.log\n")
	writeFile(t, dir, "mod.txt", "a\nb\nc\n")
	writeFile(t, dir, "gone.txt", "x\ny\n")
	writeFile(t, dir, "old name.txt", strings.Repeat("same line\n", 10))
	writeFile(t, dir, "-dash.txt", "1\n")
	writeFile(t, dir, "ünï.txt", "1\n")
	git(t, dir, "add", "-A")
	git(t, dir, "commit", "-q", "-m", "init")

	writeFile(t, dir, "mod.txt", "a\nB\nc\nd\n")
	git(t, dir, "rm", "-q", "gone.txt")
	git(t, dir, "mv", "old name.txt", "new name.txt")
	writeFile(t, dir, "-dash.txt", "2\n")
	writeFile(t, dir, "ünï.txt", "2\n")
	writeFile(t, dir, "new file.txt", "one\ntwo\n")
	writeFile(t, dir, "empty.txt", "")
	writeFile(t, dir, "bin.dat", "ab\x00cd")
	writeFile(t, dir, "ignored.log", "noise\n")

	c, err := resolveComparison(dir, ModeUncommitted, "", "")
	require.NoError(t, err)
	files, err := listChangedFiles(dir, c)
	require.NoError(t, err)

	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	assert.Equal(t, []string{"-dash.txt", "bin.dat", "empty.txt", "gone.txt", "mod.txt", "new file.txt", "new name.txt", "ünï.txt"}, paths)

	byPath := changedByPath(files)
	assert.Equal(t, ChangedFile{Path: "mod.txt", Status: "modified", Additions: 2, Deletions: 1}, byPath["mod.txt"])
	assert.Equal(t, ChangedFile{Path: "gone.txt", Status: "deleted", Deletions: 2}, byPath["gone.txt"])
	assert.Equal(t, ChangedFile{Path: "new name.txt", OldPath: "old name.txt", Status: "renamed"}, byPath["new name.txt"])
	assert.Equal(t, ChangedFile{Path: "-dash.txt", Status: "modified", Additions: 1, Deletions: 1}, byPath["-dash.txt"])
	assert.Equal(t, ChangedFile{Path: "new file.txt", Status: "added", Additions: 2}, byPath["new file.txt"])
	assert.Equal(t, ChangedFile{Path: "empty.txt", Status: "added"}, byPath["empty.txt"])
	assert.Equal(t, ChangedFile{Path: "bin.dat", Status: "added", Binary: true}, byPath["bin.dat"])
}

func TestListChangedFiles_ProjectSubdirectory(t *testing.T) {
	repo := newTestRepo(t)
	writeFile(t, repo, "docs/a.md", "a\n")
	writeFile(t, repo, "src/b.go", "b\n")
	git(t, repo, "add", "-A")
	git(t, repo, "commit", "-q", "-m", "init")
	writeFile(t, repo, "docs/a.md", "A\n")
	writeFile(t, repo, "src/b.go", "B\n")
	writeFile(t, repo, "docs/new.md", "n\n")
	writeFile(t, repo, "src/new.go", "n\n")

	project := repo + "/docs"
	c, err := resolveComparison(project, ModeUncommitted, "", "")
	require.NoError(t, err)
	files, err := listChangedFiles(project, c)
	require.NoError(t, err)

	var paths []string
	for _, f := range files {
		paths = append(paths, f.Path)
	}
	assert.Equal(t, []string{"a.md", "new.md"}, paths)

	rows, err := fileRows(project, c, changedByPath(files)["a.md"])
	require.NoError(t, err)
	assert.Equal(t, []DiffRow{{Kind: "change", OldLine: 1, NewLine: 1, OldText: "a", NewText: "A"}}, rows)
}

func TestListChangedFiles_Commit(t *testing.T) {
	dir := newTestRepo(t)
	commitFile(t, dir, "a.txt", "a\n", "first")
	sha := commitFile(t, dir, "b.txt", "b\n", "second")
	commitFile(t, dir, "c.txt", "c\n", "third")
	writeFile(t, dir, "untracked.txt", "u\n")

	c, err := resolveComparison(dir, ModeCommit, "", sha)
	require.NoError(t, err)
	files, err := listChangedFiles(dir, c)
	require.NoError(t, err)
	assert.Equal(t, []ChangedFile{{Path: "b.txt", Status: "added", Additions: 1}}, files)

	rows, err := fileRows(dir, c, files[0])
	require.NoError(t, err)
	assert.Equal(t, []DiffRow{{Kind: "add", NewLine: 1, NewText: "b"}}, rows)
}

func TestFileRows_UntrackedFile(t *testing.T) {
	dir := newTestRepo(t)
	commitFile(t, dir, "a.txt", "a\n", "first")
	writeFile(t, dir, "new.txt", "one\ntwo\n")

	c, err := resolveComparison(dir, ModeUncommitted, "", "")
	require.NoError(t, err)
	rows, err := fileRows(dir, c, ChangedFile{Path: "new.txt", Status: "added", Additions: 2})
	require.NoError(t, err)
	assert.Equal(t, []DiffRow{
		{Kind: "add", NewLine: 1, NewText: "one"},
		{Kind: "add", NewLine: 2, NewText: "two"},
	}, rows)
}

func TestFileRows_FullContext(t *testing.T) {
	dir := newTestRepo(t)
	var lines []string
	for i := 1; i <= 50; i++ {
		lines = append(lines, fmt.Sprintf("line %d", i))
	}
	commitFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n", "first")
	lines[24] = "changed"
	writeFile(t, dir, "f.txt", strings.Join(lines, "\n")+"\n")

	c, err := resolveComparison(dir, ModeUncommitted, "", "")
	require.NoError(t, err)
	rows, err := fileRows(dir, c, ChangedFile{Path: "f.txt", Status: "modified", Additions: 1, Deletions: 1})
	require.NoError(t, err)
	require.Len(t, rows, 50)
	changes := 0
	for _, r := range rows {
		if r.Kind != "context" {
			changes++
			assert.Equal(t, DiffRow{Kind: "change", OldLine: 25, NewLine: 25, OldText: "line 25", NewText: "changed"}, r)
		}
	}
	assert.Equal(t, 1, changes)
}

func TestFileRows_Binary(t *testing.T) {
	rows, err := fileRows(t.TempDir(), Comparison{}, ChangedFile{Path: "bin.dat", Binary: true})
	require.NoError(t, err)
	assert.Nil(t, rows)
}
