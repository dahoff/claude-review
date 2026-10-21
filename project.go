package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// detectProjectDir finds the project that contains dir: the deepest of dir's git
// toplevel (which is the worktree root inside a git worktree) and any registered
// project containing dir. Falls back to dir itself.
func detectProjectDir(dir string) string {
	best := ""
	consider := func(candidate string) {
		if candidate != "" && isWithinDir(candidate, dir) && len(candidate) > len(best) {
			best = candidate
		}
	}

	if out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output(); err == nil {
		consider(strings.TrimSpace(string(out)))
	}

	if projects, err := getAllProjects(); err == nil {
		for _, p := range projects {
			consider(p.Directory)
		}
	}

	if best == "" {
		return dir
	}
	return best
}

// resolveTarget maps the --project and --file flags to a project directory and a
// path relative to it ("" for the project root, which is also the default target).
//
// With an explicit project, a relative target is taken relative to that project.
// Otherwise the target is relative to the current directory, and the project is
// detected from the target's location.
func resolveTarget(projectFlag, target string) (string, string, error) {
	target = strings.TrimPrefix(target, "@")

	if projectFlag != "" && projectFlag != "." {
		projectDir, err := filepath.Abs(projectFlag)
		if err != nil {
			return "", "", err
		}
		if target == "" {
			return projectDir, "", nil
		}
		if !filepath.IsAbs(target) {
			return projectDir, filepath.Clean(target), nil
		}
		return relativeTo(projectDir, target)
	}

	cwd, err := os.Getwd()
	if err != nil {
		return "", "", fmt.Errorf("failed to get current directory: %w", err)
	}

	if target == "" {
		return detectProjectDir(cwd), "", nil
	}

	absTarget := target
	if !filepath.IsAbs(target) {
		absTarget = filepath.Join(cwd, target)
	}

	startDir := absTarget
	if info, err := os.Stat(absTarget); err != nil || !info.IsDir() {
		startDir = filepath.Dir(absTarget)
	}

	return relativeTo(detectProjectDir(startDir), absTarget)
}

func relativeTo(projectDir, absTarget string) (string, string, error) {
	if !isWithinDir(projectDir, absTarget) {
		return "", "", fmt.Errorf("%s is outside project %s", absTarget, projectDir)
	}
	rel, err := filepath.Rel(projectDir, absTarget)
	if err != nil {
		return "", "", err
	}
	if rel == "." {
		rel = ""
	}
	return projectDir, rel, nil
}
