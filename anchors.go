package main

import (
	"bytes"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/pmezard/go-difflib/difflib"
)

// Comments point at lines of a file, which move when the file is edited. Each file
// with comments has a snapshot of the content its comments' lines refer to. When the
// file on disk differs from its snapshot, syncAnchors diffs the two and moves the
// lines of the file's open threads to match, so a thread stays on its text after
// edits above it. A thread whose own lines changed keeps its mapped position and is
// marked outdated. Syncing happens lazily, whenever comments are read or created,
// so edits made while the server was not running are picked up too.

// remapLines maps the 1-based line range start..end of old onto new. changed reports
// whether the range's content differs between the two.
func remapLines(old, new []string, start, end int) (newStart, newEnd int, changed bool) {
	matcher := difflib.NewMatcherWithJunk(old, new, false, nil)
	opCodes := matcher.GetOpCodes()

	// mapLine maps one old line. Lines inside a changed block map to the block's
	// new start (atEnd false) or new end (atEnd true).
	mapLine := func(line int, atEnd bool) int {
		i := line - 1
		for _, op := range opCodes {
			if i < op.I1 || i >= op.I2 {
				continue
			}
			if op.Tag == 'e' {
				return op.J1 + (i - op.I1) + 1
			}
			if atEnd && op.J2 > op.J1 {
				return op.J2
			}
			return op.J1 + 1
		}
		// Past the end of old: keep the distance from the end
		return len(new) + line - len(old)
	}

	newStart = clampLine(mapLine(start, false), len(new))
	newEnd = clampLine(mapLine(end, true), len(new))
	if newEnd < newStart {
		newEnd = newStart
	}
	changed = !equalLines(sliceLines(old, start, end), sliceLines(new, newStart, newEnd))
	return newStart, newEnd, changed
}

func clampLine(line, count int) int {
	if line > count {
		line = count
	}
	if line < 1 {
		line = 1
	}
	return line
}

// sliceLines returns the 1-based lines start..end, cut to the lines that exist.
func sliceLines(lines []string, start, end int) []string {
	if start < 1 {
		start = 1
	}
	if end > len(lines) {
		end = len(lines)
	}
	if start > end {
		return nil
	}
	return lines[start-1 : end]
}

func equalLines(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// syncAnchors brings the open threads of a file in line with its content on disk.
// Files that are missing, binary or too large are left alone.
func syncAnchors(projectDir, filePath string) error {
	if filePath == "" {
		return nil
	}
	absPath := filepath.Join(projectDir, filePath)
	if !isWithinDir(projectDir, absPath) {
		return nil
	}
	info, err := os.Stat(absPath)
	if err != nil || info.IsDir() || info.Size() > maxContextFileSize {
		return nil
	}
	data, err := os.ReadFile(absPath)
	if err != nil || bytes.IndexByte(data, 0) >= 0 {
		return nil
	}
	current := string(data)

	var snapshot string
	query := "SELECT content FROM file_snapshots WHERE project_directory = ? AND file_path = ?"
	logQuery(query, projectDir, filePath)
	err = db.QueryRow(query, projectDir, filePath).Scan(&snapshot)
	if errors.Is(err, sql.ErrNoRows) {
		// First sight of the file: its comments refer to the current content
		query = "INSERT OR IGNORE INTO file_snapshots (project_directory, file_path, content) VALUES (?, ?, ?)"
		logQuery(query, projectDir, filePath, "<content>")
		_, err = db.Exec(query, projectDir, filePath, current)
		return err
	}
	if err != nil {
		return err
	}
	if snapshot == current {
		return nil
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Writing first takes the database's write lock. The condition makes a concurrent
	// sync of the same change (from the server or a CLI command) apply only once.
	query = "UPDATE file_snapshots SET content = ? WHERE project_directory = ? AND file_path = ? AND content = ?"
	logQuery(query, "<content>", projectDir, filePath, "<snapshot>")
	result, err := tx.Exec(query, current, projectDir, filePath, snapshot)
	if err != nil {
		return err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return err
	}

	query = `SELECT id, line_start, line_end FROM comments
		WHERE project_directory = ? AND file_path = ? AND root_id IS NULL AND resolved_at IS NULL
		AND line_start IS NOT NULL AND line_end IS NOT NULL`
	logQuery(query, projectDir, filePath)
	rows, err := tx.Query(query, projectDir, filePath)
	if err != nil {
		return err
	}
	type anchor struct{ id, start, end int }
	var anchors []anchor
	for rows.Next() {
		var a anchor
		if err := rows.Scan(&a.id, &a.start, &a.end); err != nil {
			_ = rows.Close()
			return err
		}
		anchors = append(anchors, a)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	oldLines, newLines := splitLines(snapshot), splitLines(current)
	for _, a := range anchors {
		start, end, changed := remapLines(oldLines, newLines, a.start, a.end)
		query = "UPDATE comments SET line_start = ?, line_end = ?, outdated = outdated OR ? WHERE id = ?"
		logQuery(query, start, end, changed, a.id)
		if _, err := tx.Exec(query, start, end, changed, a.id); err != nil {
			return fmt.Errorf("failed to move comment %d: %w", a.id, err)
		}
	}
	return tx.Commit()
}

// syncProjectAnchors syncs every file of a project that has open threads.
func syncProjectAnchors(projectDir string) error {
	query := `SELECT DISTINCT file_path FROM comments
		WHERE project_directory = ? AND root_id IS NULL AND resolved_at IS NULL AND line_start IS NOT NULL`
	logQuery(query, projectDir)
	rows, err := db.Query(query, projectDir)
	if err != nil {
		return err
	}
	var files []string
	for rows.Next() {
		var file string
		if err := rows.Scan(&file); err != nil {
			_ = rows.Close()
			return err
		}
		files = append(files, file)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	for _, file := range files {
		if err := syncAnchors(projectDir, file); err != nil {
			return err
		}
	}
	return nil
}
