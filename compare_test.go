package main

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
)

func rootComment(id int, file string, line int) Comment {
	return Comment{ID: id, FilePath: file, LineStart: &line, LineEnd: &line}
}

func TestPlaceComments(t *testing.T) {
	files := []ChangedFile{{Path: "a.go", Status: "modified"}}
	rows := map[string][]DiffRow{"a.go": {
		{Kind: "context", OldLine: 1, NewLine: 1},
		{Kind: "del", OldLine: 2},
		{Kind: "change", OldLine: 3, NewLine: 2},
		{Kind: "add", NewLine: 3},
		{Kind: "context", OldLine: 4, NewLine: 4},
		{Kind: "context", OldLine: 5, NewLine: 5},
	}}
	rootID := 1
	reply := Comment{ID: 5, FilePath: "a.go", RootID: &rootID}
	comments := []Comment{
		rootComment(1, "a.go", 3),
		rootComment(2, "a.go", 99),
		rootComment(3, "z.go", 1),
		reply,
	}

	assert.Equal(t, map[int]commentPlacement{
		1: {File: "a.go", Line: 3, Where: "line"},
		2: {File: "a.go", Where: "file"},
		3: {File: "z.go", Where: "other"},
	}, placeComments(files, rows, comments, false))

	assert.Equal(t, map[int]commentPlacement{
		1: {File: "a.go", Where: "file"},
		2: {File: "a.go", Where: "file"},
		3: {File: "z.go", Where: "other"},
	}, placeComments(files, rows, comments, true))
}

// treeShape renders a file tree as indented lines, folders ending in "/".
func treeShape(nodes []fileTreeNode, indent string) string {
	var out string
	for _, n := range nodes {
		if n.Dir {
			out += indent + n.Name + "/\n" + treeShape(n.Children, indent+"  ")
		} else {
			out += indent + n.Name + " -> " + n.File.ID + "\n"
		}
	}
	return out
}

func TestBuildFileTree(t *testing.T) {
	var files []compareFile
	for i, path := range []string{
		"README.md", "docs/guide/intro.md", "nested/", "src/app/main.go", "src/app/util.go", "src/lib/x.go", "a.go",
	} {
		files = append(files, compareFile{ChangedFile: ChangedFile{Path: path}, ID: fmt.Sprintf("file-%d", i)})
	}

	assert.Equal(t, `docs/guide/
  intro.md -> file-1
src/
  app/
    main.go -> file-3
    util.go -> file-4
  lib/
    x.go -> file-5
README.md -> file-0
a.go -> file-6
nested/ -> file-2
`, treeShape(buildFileTree(files), ""))
}
