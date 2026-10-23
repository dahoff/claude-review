package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Context lines shown around changes, and the size above which a file's diff starts collapsed
const (
	diffContextLines = 3
	largeDiffLines   = 1000
)

// minHiddenRows is the shortest run of unchanged rows worth folding away.
const minHiddenRows = 4

// ChangedFile is one file in a comparison. Paths are relative to the project directory.
type ChangedFile struct {
	Path      string
	OldPath   string // renames only
	Status    string // added, modified, deleted, renamed
	Binary    bool
	Additions int
	Deletions int
}

// DiffRow is one row of a split diff. A line number of 0 means that side is empty.
type DiffRow struct {
	Kind    string // context, add, del, change
	OldLine int
	NewLine int
	OldText string
	NewText string
}

// DiffSegment is a run of rows that is either shown or folded away.
type DiffSegment struct {
	Hidden bool
	Rows   []DiffRow
}

// diffArgs builds a diff command for the comparison, limited to the project directory.
func diffArgs(c Comparison, extra ...string) []string {
	args := append([]string{"diff", "--no-color", "--no-ext-diff", "-M", "--relative"}, extra...)
	args = append(args, "--end-of-options", c.BaseRev)
	if c.TargetRev != "" {
		args = append(args, c.TargetRev)
	}
	return append(args, "--")
}

// listChangedFiles lists the files that differ between the comparison's revisions,
// plus untracked files when the target is the working tree.
func listChangedFiles(dir string, c Comparison) ([]ChangedFile, error) {
	nameStatus, err := gitOutput(dir, diffArgs(c, "--name-status", "-z")...)
	if err != nil {
		return nil, err
	}
	numstat, err := gitOutput(dir, diffArgs(c, "--numstat", "-z")...)
	if err != nil {
		return nil, err
	}

	var files []ChangedFile
	fields := strings.Split(strings.TrimSuffix(nameStatus, "\x00"), "\x00")
	for i := 0; i+1 < len(fields); i += 2 {
		f := ChangedFile{Path: fields[i+1]}
		switch fields[i][0] {
		case 'A':
			f.Status = "added"
		case 'C':
			f.Status = "added"
			f.Path = fields[i+2]
			i++
		case 'D':
			f.Status = "deleted"
		case 'R':
			f.Status = "renamed"
			f.OldPath = fields[i+1]
			f.Path = fields[i+2]
			i++
		default:
			f.Status = "modified"
		}
		files = append(files, f)
	}

	stats := parseNumstat(numstat)
	for i := range files {
		if s, ok := stats[files[i].Path]; ok {
			files[i].Binary = s.binary
			files[i].Additions = s.additions
			files[i].Deletions = s.deletions
		}
	}

	if c.TargetRev == "" {
		untracked, err := gitOutput(dir, "ls-files", "--others", "--exclude-standard", "-z")
		if err != nil {
			return nil, err
		}
		for _, path := range strings.Split(strings.TrimSuffix(untracked, "\x00"), "\x00") {
			if path == "" {
				continue
			}
			f := ChangedFile{Path: path, Status: "added"}
			if content, err := os.ReadFile(filepath.Join(dir, path)); err == nil {
				if isBinary(content) {
					f.Binary = true
				} else {
					f.Additions = len(splitLines(string(content)))
				}
			}
			files = append(files, f)
		}
	}

	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	return files, nil
}

type fileStat struct {
	additions, deletions int
	binary               bool
}

// parseNumstat parses `diff --numstat -z` output, keyed by the new path.
func parseNumstat(out string) map[string]fileStat {
	stats := map[string]fileStat{}
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		parts := strings.SplitN(fields[i], "\t", 3)
		if len(parts) != 3 {
			continue
		}
		path := parts[2]
		if path == "" && i+2 < len(fields) {
			// Rename: the old and new paths follow as separate fields
			path = fields[i+2]
			i += 2
		}
		s := fileStat{binary: parts[0] == "-"}
		s.additions, _ = strconv.Atoi(parts[0])
		s.deletions, _ = strconv.Atoi(parts[1])
		stats[path] = s
	}
	return stats
}

func isBinary(content []byte) bool {
	return bytes.IndexByte(content[:min(len(content), 8000)], 0) >= 0
}

func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// fileRows returns the split diff rows covering every line of both versions of a file.
func fileRows(dir string, c Comparison, f ChangedFile) ([]DiffRow, error) {
	if f.Binary {
		return nil, nil
	}

	// Files added in the working tree may be untracked, so git cannot diff them
	if c.TargetRev == "" && f.Status == "added" {
		content, err := os.ReadFile(filepath.Join(dir, f.Path))
		if err != nil {
			return nil, err
		}
		var rows []DiffRow
		for i, line := range splitLines(string(content)) {
			rows = append(rows, DiffRow{Kind: "add", NewLine: i + 1, NewText: line})
		}
		return rows, nil
	}

	args := diffArgs(c, "-U1000000")
	if f.OldPath != "" {
		args = append(args, f.OldPath)
	}
	out, err := gitOutput(dir, append(args, f.Path)...)
	if err != nil {
		return nil, err
	}
	return parseUnifiedRows(out), nil
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@`)

// parseUnifiedRows converts a single file's unified diff into split rows, pairing each run
// of removed lines with the run of added lines that follows it.
func parseUnifiedRows(diff string) []DiffRow {
	var rows, dels, adds []DiffRow
	oldLine, newLine := 0, 0
	inHunk := false

	flush := func() {
		for i := 0; i < max(len(dels), len(adds)); i++ {
			switch {
			case i < len(dels) && i < len(adds):
				rows = append(
					rows,
					DiffRow{
						Kind:    "change",
						OldLine: dels[i].OldLine,
						OldText: dels[i].OldText,
						NewLine: adds[i].NewLine,
						NewText: adds[i].NewText,
					},
				)
			case i < len(dels):
				rows = append(rows, dels[i])
			default:
				rows = append(rows, adds[i])
			}
		}
		dels, adds = nil, nil
	}

	for _, line := range strings.Split(strings.TrimSuffix(diff, "\n"), "\n") {
		if m := hunkHeader.FindStringSubmatch(line); m != nil {
			flush()
			oldLine, _ = strconv.Atoi(m[1])
			newLine, _ = strconv.Atoi(m[2])
			inHunk = true
			continue
		}
		if !inHunk || line == "" {
			continue
		}
		switch line[0] {
		case ' ':
			flush()
			rows = append(
				rows,
				DiffRow{Kind: "context", OldLine: oldLine, NewLine: newLine, OldText: line[1:], NewText: line[1:]},
			)
			oldLine++
			newLine++
		case '-':
			if len(adds) > 0 {
				flush()
			}
			dels = append(dels, DiffRow{Kind: "del", OldLine: oldLine, OldText: line[1:]})
			oldLine++
		case '+':
			adds = append(adds, DiffRow{Kind: "add", NewLine: newLine, NewText: line[1:]})
			newLine++
		}
	}
	flush()
	return rows
}

// foldRows hides unchanged rows more than context rows away from any change or kept line.
func foldRows(rows []DiffRow, keepNewLines map[int]bool, context int) []DiffSegment {
	visible := make([]bool, len(rows))
	show := func(center int) {
		for j := max(0, center-context); j <= min(len(rows)-1, center+context); j++ {
			visible[j] = true
		}
	}
	for i, r := range rows {
		if r.Kind != "context" || (r.NewLine != 0 && keepNewLines[r.NewLine]) {
			show(i)
		}
	}

	// Folding a few rows saves nothing, so show short hidden runs
	for i := 0; i < len(rows); {
		if visible[i] {
			i++
			continue
		}
		j := i
		for j < len(rows) && !visible[j] {
			j++
		}
		if j-i < minHiddenRows {
			for k := i; k < j; k++ {
				visible[k] = true
			}
		}
		i = j
	}

	var segments []DiffSegment
	for i, r := range rows {
		hidden := !visible[i]
		if len(segments) == 0 || segments[len(segments)-1].Hidden != hidden {
			segments = append(segments, DiffSegment{Hidden: hidden})
		}
		segments[len(segments)-1].Rows = append(segments[len(segments)-1].Rows, r)
	}
	return segments
}
