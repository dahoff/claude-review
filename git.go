package main

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
)

// emptyTreeSHA is git's well-known empty tree, the base for root commits and unborn branches.
const emptyTreeSHA = "4b825dc642cb6eb9a060e54bf8d69288fbee4904"

// Comparison modes offered by the compare page
const (
	ModeUncommitted = "uncommitted"
	ModeBase        = "base"
	ModeCommit      = "commit"
)

// errInvalidComparison marks user input (mode, branch, commit) that cannot be compared.
var errInvalidComparison = errors.New("invalid comparison")

// Comparison is a resolved pair of revisions to diff.
type Comparison struct {
	Mode       string
	BaseBranch string // ModeBase only
	Commit     string // ModeCommit only: full SHA
	BaseRev    string // full SHA or emptyTreeSHA
	TargetRev  string // full SHA, or "" for the working tree
	Notice     string // explains a fallback to another mode
	ReadOnly   bool   // comments cannot be placed or created
}

type CommitInfo struct {
	SHA      string
	ShortSHA string
	Subject  string
}

// gitOutput runs git in dir and returns its stdout. Paths in the output are not quoted,
// and pathspecs are taken literally.
func gitOutput(dir string, args ...string) (string, error) {
	cmd := exec.Command(
		"git",
		append([]string{"-C", dir, "-c", "core.quotePath=false", "--literal-pathspecs"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %w: %s", args[0], err, strings.TrimSpace(stderr.String()))
	}
	return string(out), nil
}

func isGitRepo(dir string) bool {
	out, err := gitOutput(dir, "rev-parse", "--is-inside-work-tree")
	return err == nil && strings.TrimSpace(out) == "true"
}

// currentBranch returns the checked-out branch, or "" when HEAD is detached.
func currentBranch(dir string) string {
	out, err := gitOutput(dir, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return ""
	}
	return strings.TrimSpace(out)
}

func localBranches(dir string) ([]string, error) {
	out, err := gitOutput(dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
	if err != nil {
		return nil, err
	}
	branches := strings.Fields(out)
	slices.Sort(branches)
	return branches, nil
}

// defaultBaseBranch picks the local branch that origin/HEAD names, else main, else master.
func defaultBaseBranch(dir string) string {
	branches, err := localBranches(dir)
	if err != nil {
		return ""
	}
	candidates := []string{"main", "master"}
	if out, err := gitOutput(dir, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		candidates = append([]string{strings.TrimPrefix(strings.TrimSpace(out), "origin/")}, candidates...)
	}
	for _, c := range candidates {
		if slices.Contains(branches, c) {
			return c
		}
	}
	return ""
}

// recentCommits lists up to limit commits reachable from HEAD, newest first.
func recentCommits(dir string, limit int) ([]CommitInfo, error) {
	if _, err := revParse(dir, "HEAD"); err != nil {
		return nil, nil // unborn HEAD has no commits
	}
	out, err := gitOutput(dir, "log", "-n", strconv.Itoa(limit), "--format=%H%x00%h%x00%s", "HEAD", "--")
	if err != nil {
		return nil, err
	}
	var commits []CommitInfo
	for _, line := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		parts := strings.SplitN(line, "\x00", 3)
		if len(parts) == 3 {
			commits = append(commits, CommitInfo{SHA: parts[0], ShortSHA: parts[1], Subject: parts[2]})
		}
	}
	return commits, nil
}

// revParse resolves rev to a full commit SHA.
func revParse(dir, rev string) (string, error) {
	out, err := gitOutput(dir, "rev-parse", "--verify", "--quiet", "--end-of-options", rev+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// resolveComparison turns the compare page's query values into revisions. An empty mode
// picks Since base when the current branch has commits beyond the default base, and
// Uncommitted otherwise.
func resolveComparison(dir, mode, baseBranch, commit string) (Comparison, error) {
	switch mode {
	case "":
		if base := defaultBaseBranch(dir); base != "" && currentBranch(dir) != "" && currentBranch(dir) != base {
			if mergeBase, err := mergeBaseWith(dir, base); err == nil {
				if out, err := gitOutput(dir, "rev-list", "--count", mergeBase+"..HEAD"); err == nil &&
					strings.TrimSpace(out) != "0" {
					return Comparison{Mode: ModeBase, BaseBranch: base, BaseRev: mergeBase}, nil
				}
			}
		}
		return uncommittedComparison(dir), nil

	case ModeUncommitted:
		return uncommittedComparison(dir), nil

	case ModeBase:
		if baseBranch == "" {
			baseBranch = defaultBaseBranch(dir)
		} else {
			branches, err := localBranches(dir)
			if err != nil {
				return Comparison{}, err
			}
			if !slices.Contains(branches, baseBranch) {
				return Comparison{}, fmt.Errorf("%w: unknown branch %q", errInvalidComparison, baseBranch)
			}
		}
		if baseBranch == "" {
			c := uncommittedComparison(dir)
			c.Notice = "No base branch found; showing uncommitted changes"
			return c, nil
		}
		mergeBase, err := mergeBaseWith(dir, baseBranch)
		if err != nil {
			c := uncommittedComparison(dir)
			c.Notice = fmt.Sprintf("No common ancestor with %s; showing uncommitted changes", baseBranch)
			return c, nil
		}
		return Comparison{Mode: ModeBase, BaseBranch: baseBranch, BaseRev: mergeBase}, nil

	case ModeCommit:
		if commit == "" || strings.HasPrefix(commit, "-") {
			return Comparison{}, fmt.Errorf("%w: invalid commit %q", errInvalidComparison, commit)
		}
		sha, err := revParse(dir, commit)
		if err != nil {
			return Comparison{}, fmt.Errorf("%w: unknown commit %q", errInvalidComparison, commit)
		}
		parent, err := revParse(dir, sha+"^")
		if err != nil {
			parent = emptyTreeSHA
		}
		return Comparison{Mode: ModeCommit, Commit: sha, BaseRev: parent, TargetRev: sha, ReadOnly: true}, nil
	}

	return Comparison{}, fmt.Errorf("%w: unknown mode %q", errInvalidComparison, mode)
}

func uncommittedComparison(dir string) Comparison {
	head, err := revParse(dir, "HEAD")
	if err != nil {
		head = emptyTreeSHA
	}
	return Comparison{Mode: ModeUncommitted, BaseRev: head}
}

func mergeBaseWith(dir, branch string) (string, error) {
	out, err := gitOutput(dir, "merge-base", "--end-of-options", "refs/heads/"+branch, "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}
