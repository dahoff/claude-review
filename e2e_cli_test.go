package main_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestE2E_CLI_Register tests the register command variants
func TestE2E_CLI_Register(t *testing.T) {
	env := setupE2E(t)

	t.Run("register with explicit project flag", func(t *testing.T) {
		output, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "Registered project")
		assert.Contains(t, output, env.ProjectDir)
	})

	t.Run("register without project flag uses current directory", func(t *testing.T) {
		// Change to project directory
		oldDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldDir) }()
		_ = os.Chdir(env.ProjectDir)

		output, err := env.runCLI(t, "register")
		require.NoError(t, err)
		assert.Contains(t, output, "Registered project")
		assert.Contains(t, output, env.ProjectDir)
	})

	t.Run("register same project twice is idempotent", func(t *testing.T) {
		// First registration
		output1, err1 := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err1)
		assert.Contains(t, output1, "Registered project")

		// Second registration should also succeed (idempotent)
		output2, err2 := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err2)
		assert.Contains(t, output2, "Registered project")
	})

	t.Run("register with dot as project directory", func(t *testing.T) {
		oldDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldDir) }()
		_ = os.Chdir(env.ProjectDir)

		output, err := env.runCLI(t, "register", "--project", ".")
		require.NoError(t, err)
		assert.Contains(t, output, "Registered project")
		assert.Contains(t, output, env.ProjectDir)
	})
}

// TestE2E_CLI_Address tests the address command edge cases
// Note: Full workflow testing is covered in TestE2E_CommentWorkflow
func TestE2E_CLI_Address(t *testing.T) {
	t.Run("address without file flag and no comments", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		output, err := env.runCLI(t, "address", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments for "+env.ProjectDir)
	})

	t.Run("address with no comments", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		output, err := env.runCLI(t, "address", "--file", "test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})

	t.Run("address strips @ prefix from file path", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		// Use @ prefix in CLI (should work identically to without prefix)
		output, err := env.runCLI(t, "address", "--file", "@test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})

	t.Run("address without project flag uses current directory", func(t *testing.T) {
		env := setupE2E(t)
		oldDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldDir) }()
		_ = os.Chdir(env.ProjectDir)

		_, err := env.runCLI(t, "register")
		require.NoError(t, err)

		output, err := env.runCLI(t, "address", "--file", "test.md")
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})
}

// TestE2E_CLI_Resolve tests the resolve command edge cases
// Note: Full workflow testing is covered in TestE2E_CommentWorkflow
func TestE2E_CLI_Resolve(t *testing.T) {
	t.Run("resolve without file flag shows error", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		output, err := env.runCLI(t, "resolve", "--project", env.ProjectDir)
		require.Error(t, err)
		assert.Contains(t, output, "--file flag is required")
	})

	t.Run("resolve with no comments", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		output, err := env.runCLI(t, "resolve", "--file", "test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})

	t.Run("resolve strips @ prefix from file path", func(t *testing.T) {
		env := setupE2E(t)
		_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
		require.NoError(t, err)

		// Resolve with @ prefix (should work identically to without prefix)
		output, err := env.runCLI(t, "resolve", "--file", "@test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})

	t.Run("resolve without project flag uses current directory", func(t *testing.T) {
		env := setupE2E(t)
		oldDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldDir) }()
		_ = os.Chdir(env.ProjectDir)

		_, err := env.runCLI(t, "register")
		require.NoError(t, err)

		output, err := env.runCLI(t, "resolve", "--file", "test.md")
		require.NoError(t, err)
		assert.Contains(t, output, "No unresolved comments")
	})
}

// TestE2E_CLI_Review tests the review command
func TestE2E_CLI_Review(t *testing.T) {
	// Create isolated environment without starting server
	tempDir := t.TempDir()
	dataDir := filepath.Join(tempDir, "data")
	projectDir := filepath.Join(tempDir, "project")
	binaryPath := filepath.Join(tempDir, "claude-review")

	require.NoError(t, os.MkdirAll(dataDir, 0755))
	require.NoError(t, os.MkdirAll(projectDir, 0755))
	createTestMarkdownFiles(t, projectDir)

	// Build binary
	buildCmd := exec.Command("go", "build", "-cover", "-o", binaryPath, ".")
	require.NoError(t, buildCmd.Run())

	env := &TestEnv{
		TempDir:    tempDir,
		DataDir:    dataDir,
		ProjectDir: projectDir,
		Port:       "14780", // Different port to avoid conflict
		BinaryPath: binaryPath,
	}

	// Ensure daemon is stopped on test completion
	t.Cleanup(func() {
		_, _ = env.runCLI(t, "server", "--stop")
		_ = waitForPIDFileRemoved(env.PIDFile(), 2*time.Second)
	})

	t.Run("review without file opens project root", func(t *testing.T) {
		output, err := env.runCLI(t, "review", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, fmt.Sprintf("http://localhost:%s/projects%s/\n", env.Port, env.ProjectDir))
	})

	t.Run("review --diff prints the compare URL", func(t *testing.T) {
		output, err := env.runCLI(t, "review", "--diff", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "review changes in project:")
		assert.Contains(t, output, fmt.Sprintf("http://localhost:%s/compare%s\n", env.Port, env.ProjectDir))
	})

	t.Run("review detects git root from a subdirectory", func(t *testing.T) {
		repo := filepath.Join(tempDir, "repo")
		subDir := filepath.Join(repo, "docs")
		require.NoError(t, os.MkdirAll(subDir, 0755))
		runGit(t, repo, "init", "-q")

		output, err := env.runCLIInDir(t, subDir, "review")
		require.NoError(t, err)
		assert.Contains(t, output, fmt.Sprintf("/projects%s/\n", repo))

		output, err = env.runCLIInDir(t, subDir, "review", "--file", "PLAN.md")
		require.NoError(t, err)
		assert.Contains(t, output, fmt.Sprintf("/projects%s/docs/PLAN.md\n", repo))
	})

	t.Run("review scopes to a git worktree", func(t *testing.T) {
		repo := filepath.Join(tempDir, "wt-repo")
		require.NoError(t, os.MkdirAll(repo, 0755))
		runGit(t, repo, "init", "-q")
		runGit(t, repo, "commit", "-q", "--allow-empty", "-m", "init")
		worktree := filepath.Join(repo, ".claude", "worktrees", "feature")
		runGit(t, repo, "worktree", "add", "-q", worktree)

		output, err := env.runCLIInDir(t, worktree, "review")
		require.NoError(t, err)
		assert.Contains(t, output, fmt.Sprintf("/projects%s/\n", worktree))

		// A directory argument opens that directory
		output, err = env.runCLIInDir(t, repo, "review", "--file", ".claude/worktrees/feature")
		require.NoError(t, err)
		assert.Contains(t, output, fmt.Sprintf("/projects%s/\n", worktree))
	})

	t.Run("review outputs URL and starts daemon", func(t *testing.T) {
		output, err := env.runCLI(t, "review", "--file", "test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "Open this URL")
		assert.Contains(t, output, fmt.Sprintf("http://localhost:%s/projects", env.Port))
		assert.Contains(t, output, "test.md")
	})

	t.Run("review strips @ prefix from file path", func(t *testing.T) {
		output, err := env.runCLI(t, "review", "--file", "@simple.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "simple.md")
		assert.NotContains(t, output, "@simple.md")
	})
}

// TestE2E_CLI_Version tests the version command
func TestE2E_CLI_Version(t *testing.T) {
	env := setupE2E(t)

	output, err := env.runCLI(t, "version")
	require.NoError(t, err)
	// Should output version string (currently "dev" or semver)
	assert.NotEmpty(t, strings.TrimSpace(output))
}

// TestE2E_CLI_UnknownCommand tests error handling for unknown commands
func TestE2E_CLI_UnknownCommand(t *testing.T) {
	env := setupE2E(t)

	output, err := env.runCLI(t, "nonexistent-command")
	require.Error(t, err)
	assert.Contains(t, output, "Unknown command")
}

// TestE2E_CLI_NoCommand tests help output when no command is provided
func TestE2E_CLI_NoCommand(t *testing.T) {
	env := setupE2E(t)

	// Run with no arguments
	cmd := exec.Command(env.BinaryPath)
	cmd.Env = append(os.Environ(),
		"CR_DATA_DIR="+env.DataDir,
		"CR_LISTEN_PORT="+env.Port,
		"GOCOVERDIR=tmp/coverage",
	)

	output, err := cmd.CombinedOutput()
	require.Error(t, err) // Should exit with error
	outputStr := string(output)

	assert.Contains(t, outputStr, "Usage: claude-review <command>")
	assert.Contains(t, outputStr, "Commands:")
	assert.Contains(t, outputStr, "server")
	assert.Contains(t, outputStr, "register")
	assert.Contains(t, outputStr, "address")
	assert.Contains(t, outputStr, "resolve")
	assert.Contains(t, outputStr, "review")
}

// TestE2E_CLI_Install tests the install command for slash commands
func TestE2E_CLI_Install(t *testing.T) {
	// Create isolated environment with custom home directory
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0755))

	// Build binary
	binaryPath := filepath.Join(tempDir, "claude-review")
	buildCmd := exec.Command("go", "build", "-cover", "-o", binaryPath, ".")
	require.NoError(t, buildCmd.Run())

	t.Run("install creates commands directory and files", func(t *testing.T) {
		// Run install command with custom HOME
		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"GOCOVERDIR=tmp/coverage",
		)

		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		outputStr := string(output)

		// Check output messages
		assert.Contains(t, outputStr, "Successfully installed")
		assert.Contains(t, outputStr, "slash command")
		assert.Contains(t, outputStr, filepath.Join(homeDir, ".claude", "commands"))

		// Verify commands directory was created
		commandsDir := filepath.Join(homeDir, ".claude", "commands")
		stat, err := os.Stat(commandsDir)
		require.NoError(t, err)
		assert.True(t, stat.IsDir())

		// Verify slash command files were installed
		entries, err := os.ReadDir(commandsDir)
		require.NoError(t, err)
		assert.Greater(t, len(entries), 0, "Should install at least one slash command")

		// Check for expected slash commands
		var foundCommands []string
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				foundCommands = append(foundCommands, entry.Name())
			}
		}

		// Verify expected commands exist
		assert.Contains(t, foundCommands, "cr-review.md")
		assert.Contains(t, foundCommands, "cr-address.md")
		assert.Contains(t, foundCommands, "cr-diff.md")

		// Verify output lists the installed commands
		assert.Contains(t, outputStr, "/cr-review")
		assert.Contains(t, outputStr, "/cr-address")
	})

	t.Run("install is idempotent and overwrites existing files", func(t *testing.T) {
		commandsDir := filepath.Join(homeDir, ".claude", "commands")

		// Get initial file count
		initialEntries, err := os.ReadDir(commandsDir)
		require.NoError(t, err)
		initialCount := len(initialEntries)

		// Modify one of the files
		testFile := filepath.Join(commandsDir, "cr-review.md")
		require.NoError(t, os.WriteFile(testFile, []byte("MODIFIED CONTENT"), 0644))

		// Run install again
		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"GOCOVERDIR=tmp/coverage",
		)

		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		// Verify file was overwritten
		content, err := os.ReadFile(testFile)
		require.NoError(t, err)
		assert.NotContains(t, string(content), "MODIFIED CONTENT")

		// Verify same number of files (no duplicates)
		finalEntries, err := os.ReadDir(commandsDir)
		require.NoError(t, err)
		assert.Equal(t, initialCount, len(finalEntries), "Should not create duplicate files")

		// Verify output still shows success
		outputStr := string(output)
		assert.Contains(t, outputStr, "Successfully installed")
	})

	t.Run("install creates missing parent directories", func(t *testing.T) {
		// Create fresh home directory without .claude
		freshHome := filepath.Join(tempDir, "fresh-home")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)

		output, err := cmd.CombinedOutput()
		require.NoError(t, err)

		// Verify .claude/commands was created
		commandsDir := filepath.Join(freshHome, ".claude", "commands")
		stat, err := os.Stat(commandsDir)
		require.NoError(t, err)
		assert.True(t, stat.IsDir())

		// Verify files were installed
		outputStr := string(output)
		assert.Contains(t, outputStr, "Successfully installed")
	})

	t.Run("install verifies file contents are correct", func(t *testing.T) {
		commandsDir := filepath.Join(homeDir, ".claude", "commands")

		// Read installed cr-address.md
		addressFile := filepath.Join(commandsDir, "cr-address.md")
		content, err := os.ReadFile(addressFile)
		require.NoError(t, err)
		contentStr := string(content)

		// Verify it contains expected content (should reference claude-review command)
		assert.Contains(t, contentStr, "claude-review")
		assert.Contains(t, contentStr, "address")

		// Read installed cr-review.md
		reviewFile := filepath.Join(commandsDir, "cr-review.md")
		content, err = os.ReadFile(reviewFile)
		require.NoError(t, err)
		contentStr = string(content)

		// Verify it contains expected content
		assert.Contains(t, contentStr, "claude-review")
		assert.Contains(t, contentStr, "review")
	})

	t.Run("install file permissions are correct", func(t *testing.T) {
		commandsDir := filepath.Join(homeDir, ".claude", "commands")

		// Check directory permissions
		dirStat, err := os.Stat(commandsDir)
		require.NoError(t, err)
		assert.Equal(t, os.FileMode(0755), dirStat.Mode().Perm())

		// Check file permissions
		entries, err := os.ReadDir(commandsDir)
		require.NoError(t, err)

		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				filePath := filepath.Join(commandsDir, entry.Name())
				fileStat, err := os.Stat(filePath)
				require.NoError(t, err)
				assert.Equal(t, os.FileMode(0644), fileStat.Mode().Perm(),
					"File %s should have 0644 permissions", entry.Name())
			}
		}
	})
}

// TestE2E_CLI_Uninstall tests the uninstall command for slash commands
func TestE2E_CLI_Uninstall(t *testing.T) {
	// Create isolated environment with custom home directory
	tempDir := t.TempDir()
	homeDir := filepath.Join(tempDir, "home")
	require.NoError(t, os.MkdirAll(homeDir, 0755))

	// Build binary
	binaryPath := filepath.Join(tempDir, "claude-review")
	buildCmd := exec.Command("go", "build", "-cover", "-o", binaryPath, ".")
	require.NoError(t, buildCmd.Run())

	// Helper to run install
	runInstall := func() {
		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"GOCOVERDIR=tmp/coverage",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "Install failed: %s", string(output))
	}

	// Helper to run uninstall
	runUninstall := func() (string, error) {
		cmd := exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+homeDir,
			"GOCOVERDIR=tmp/coverage",
		)
		output, err := cmd.CombinedOutput()
		return string(output), err
	}

	t.Run("uninstall removes installed slash commands", func(t *testing.T) {
		// First install commands
		runInstall()

		commandsDir := filepath.Join(homeDir, ".claude", "commands")

		// Verify files exist before uninstall
		entries, err := os.ReadDir(commandsDir)
		require.NoError(t, err)
		require.Greater(t, len(entries), 0, "Should have installed files")

		// Run uninstall
		output, err := runUninstall()
		require.NoError(t, err)

		// Check output messages
		assert.Contains(t, output, "Successfully uninstalled")
		assert.Contains(t, output, "slash command")
		assert.Contains(t, output, filepath.Join(homeDir, ".claude", "commands"))
		assert.Contains(t, output, "/cr-review")
		assert.Contains(t, output, "/cr-address")

		// Verify files were removed
		crReviewPath := filepath.Join(commandsDir, "cr-review.md")
		_, err = os.Stat(crReviewPath)
		assert.True(t, os.IsNotExist(err), "cr-review.md should be removed")

		crAddressPath := filepath.Join(commandsDir, "cr-address.md")
		_, err = os.Stat(crAddressPath)
		assert.True(t, os.IsNotExist(err), "cr-address.md should be removed")
	})

	t.Run("uninstall when no commands are installed", func(t *testing.T) {
		// Create fresh home directory
		freshHome := filepath.Join(tempDir, "fresh-home-uninstall")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		cmd := exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)

		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		outputStr := string(output)

		// Should report that no commands were installed
		assert.Contains(t, outputStr, "No slash commands were installed")
	})

	t.Run("uninstall is idempotent", func(t *testing.T) {
		// Create fresh home and install
		freshHome := filepath.Join(tempDir, "home-idempotent")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		// Install
		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		_, err := cmd.CombinedOutput()
		require.NoError(t, err)

		// First uninstall
		cmd = exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		output1, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output1), "Successfully uninstalled")

		// Second uninstall (should succeed without error)
		cmd = exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		output2, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output2), "No slash commands were installed")
	})

	t.Run("uninstall only removes managed commands", func(t *testing.T) {
		// Create fresh home and install
		freshHome := filepath.Join(tempDir, "home-selective")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		_, err := cmd.CombinedOutput()
		require.NoError(t, err)

		// Add a user's custom command
		commandsDir := filepath.Join(freshHome, ".claude", "commands")
		customFile := filepath.Join(commandsDir, "custom-command.md")
		require.NoError(t, os.WriteFile(customFile, []byte("# Custom command"), 0644))

		// Run uninstall
		cmd = exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output), "Successfully uninstalled")

		// Verify custom command still exists
		_, err = os.Stat(customFile)
		assert.NoError(t, err, "Custom command should not be removed")

		// Verify managed commands are removed
		crReviewPath := filepath.Join(commandsDir, "cr-review.md")
		_, err = os.Stat(crReviewPath)
		assert.True(t, os.IsNotExist(err), "cr-review.md should be removed")
	})

	t.Run("uninstall and reinstall works correctly", func(t *testing.T) {
		// Create fresh home
		freshHome := filepath.Join(tempDir, "home-reinstall")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		commandsDir := filepath.Join(freshHome, ".claude", "commands")

		// Install
		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		_, err := cmd.CombinedOutput()
		require.NoError(t, err)

		// Verify installed
		crReviewPath := filepath.Join(commandsDir, "cr-review.md")
		_, err = os.Stat(crReviewPath)
		require.NoError(t, err, "Should be installed")

		// Uninstall
		cmd = exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		_, err = cmd.CombinedOutput()
		require.NoError(t, err)

		// Verify uninstalled
		_, err = os.Stat(crReviewPath)
		assert.True(t, os.IsNotExist(err), "Should be uninstalled")

		// Reinstall
		cmd = exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(),
			"HOME="+freshHome,
			"GOCOVERDIR=tmp/coverage",
		)
		output, err := cmd.CombinedOutput()
		require.NoError(t, err)
		assert.Contains(t, string(output), "Successfully installed")

		// Verify reinstalled
		_, err = os.Stat(crReviewPath)
		assert.NoError(t, err, "Should be reinstalled")

		// Verify content is correct
		content, err := os.ReadFile(crReviewPath)
		require.NoError(t, err)
		assert.Contains(t, string(content), "claude-review")
	})

	// Helper to lay out a home directory the way install.sh leaves it
	setupInstalledHome := func(t *testing.T, name string) (home, binPath, dataDir string) {
		home = filepath.Join(tempDir, name)
		binPath = filepath.Join(home, ".local", "bin", "claude-review")
		dataDir = filepath.Join(home, ".local", "share", "claude-review")
		require.NoError(t, os.MkdirAll(filepath.Dir(binPath), 0755))
		require.NoError(t, os.MkdirAll(dataDir, 0755))
		require.NoError(t, os.WriteFile(binPath, []byte("binary"), 0755))
		require.NoError(t, os.WriteFile(filepath.Join(dataDir, "comments.db"), []byte("db"), 0644))

		cmd := exec.Command(binaryPath, "install")
		cmd.Env = append(os.Environ(), "HOME="+home, "GOCOVERDIR=tmp/coverage", "CR_DATA_DIR=")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, "Install failed: %s", string(output))
		return home, binPath, dataDir
	}

	t.Run("uninstall removes the binary and keeps the data directory", func(t *testing.T) {
		home, binPath, dataDir := setupInstalledHome(t, "home-binary")

		cmd := exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(), "HOME="+home, "GOCOVERDIR=tmp/coverage", "CR_DATA_DIR=")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		assert.Contains(t, string(output), "Removed binary "+binPath)
		assert.Contains(t, string(output), "Kept data directory "+dataDir)
		assert.Contains(t, string(output), "--purge")

		_, err = os.Stat(binPath)
		assert.True(t, os.IsNotExist(err), "Binary should be removed")
		_, err = os.Stat(filepath.Join(dataDir, "comments.db"))
		assert.NoError(t, err, "Comments database should be kept")
	})

	t.Run("uninstall --purge removes the data directory", func(t *testing.T) {
		home, binPath, dataDir := setupInstalledHome(t, "home-purge")

		cmd := exec.Command(binaryPath, "uninstall", "--purge")
		cmd.Env = append(os.Environ(), "HOME="+home, "GOCOVERDIR=tmp/coverage", "CR_DATA_DIR=")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		assert.Contains(t, string(output), "Removed data directory "+dataDir)

		_, err = os.Stat(binPath)
		assert.True(t, os.IsNotExist(err), "Binary should be removed")
		_, err = os.Stat(dataDir)
		assert.True(t, os.IsNotExist(err), "Data directory should be removed")
	})

	t.Run("uninstall does not create the data directory", func(t *testing.T) {
		freshHome := filepath.Join(tempDir, "home-no-data")
		require.NoError(t, os.MkdirAll(freshHome, 0755))

		cmd := exec.Command(binaryPath, "uninstall")
		cmd.Env = append(os.Environ(), "HOME="+freshHome, "GOCOVERDIR=tmp/coverage")
		output, err := cmd.CombinedOutput()
		require.NoError(t, err, string(output))

		assert.NotContains(t, string(output), "data directory")
		_, err = os.Stat(filepath.Join(freshHome, ".local", "share", "claude-review"))
		assert.True(t, os.IsNotExist(err), "Data directory should not be created")
	})
}

// TestE2E_CLI_Address_Project tests addressing comments across all files of a project
func TestE2E_CLI_Address_Project(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	require.NoError(t, os.MkdirAll(filepath.Join(env.ProjectDir, "docs"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(env.ProjectDir, "docs", "design.md"), []byte("# Design\n"), 0644))

	addComment := func(file, text string) int {
		resp := env.postJSON(t, "/api/comments", map[string]interface{}{
			"project_directory": env.ProjectDir,
			"file_path":         file,
			"line_start":        1,
			"line_end":          1,
			"selected_text":     "Test",
			"comment_text":      text,
		})
		defer func() { _ = resp.Body.Close() }()
		require.Equal(t, http.StatusOK, resp.StatusCode)
		var created map[string]interface{}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&created))
		return int(created["id"].(float64))
	}

	addComment("test.md", "Comment on test")
	addComment("docs/design.md", "Comment on design")
	answeredID := addComment("simple.md", "Answered comment on simple")
	_, err = env.runCLI(t, "reply", "--comment-id", fmt.Sprintf("%d", answeredID), "--message", "Done")
	require.NoError(t, err)

	t.Run("lists every file with unresolved comments", func(t *testing.T) {
		output, err := env.runCLI(t, "address", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "Found 3 unresolved comment(s) in 3 file(s)")
		assert.Contains(t, output, "- docs/design.md (1)")
		assert.Contains(t, output, "# File: "+filepath.Join(env.ProjectDir, "test.md"))
		assert.Contains(t, output, "Comment on design")
	})

	t.Run("pending skips files where the agent replied last", func(t *testing.T) {
		output, err := env.runCLI(t, "address", "--pending", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "Found 2 unresolved comment(s) in 2 file(s)")
		assert.NotContains(t, output, "simple.md")
	})

	t.Run("detects the project from the current directory", func(t *testing.T) {
		output, err := env.runCLIInDir(t, filepath.Join(env.ProjectDir, "docs"), "address")
		require.NoError(t, err)
		assert.Contains(t, output, "in 3 file(s) of "+env.ProjectDir)
	})

	t.Run("resolve accepts the absolute path from address output", func(t *testing.T) {
		output, err := env.runCLIInDir(t, env.TempDir, "resolve", "--file", filepath.Join(env.ProjectDir, "simple.md"))
		require.NoError(t, err)
		assert.Contains(t, output, "Resolved 2 comment(s) for simple.md")
	})

	t.Run("directory listing shows unresolved counts", func(t *testing.T) {
		resp, err := http.Get(env.BaseURL + "/projects" + env.ProjectDir + "/")
		require.NoError(t, err)
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()

		assert.Regexp(t, `docs/\s*</a>\s*<span class="entry-comment-count"[^>]*>1</span>`, string(body))
		assert.Regexp(t, `test\.md\s*</a>\s*<span class="entry-comment-count"[^>]*>1</span>`, string(body))
	})
}

// TestE2E_CLI_Address_General tests general comments: on the project, not on a file
func TestE2E_CLI_Address_General(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	t.Run("general comments cannot have lines or a selection", func(t *testing.T) {
		resp := env.postJSON(t, "/api/comments", map[string]interface{}{
			"project_directory": env.ProjectDir,
			"line_start":        1,
			"line_end":          1,
			"comment_text":      "Misplaced",
		})
		defer func() { _ = resp.Body.Close() }()
		assert.Equal(t, http.StatusBadRequest, resp.StatusCode)
	})

	resp := env.postJSON(t, "/api/comments", map[string]interface{}{
		"project_directory": env.ProjectDir,
		"comment_text":      "You should also have updated README.md",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	var general struct {
		ID       int    `json:"id"`
		FilePath string `json:"file_path"`
		Author   string `json:"author"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&general))
	_ = resp.Body.Close()
	assert.Equal(t, "", general.FilePath)
	assert.Equal(t, "user", general.Author)

	resp = env.postJSON(t, "/api/comments", map[string]interface{}{
		"project_directory": env.ProjectDir,
		"file_path":         "test.md",
		"line_start":        1,
		"line_end":          1,
		"selected_text":     "Test",
		"comment_text":      "Comment on test",
	})
	require.Equal(t, http.StatusOK, resp.StatusCode)
	_ = resp.Body.Close()

	t.Run("address lists general comments first, under their own header", func(t *testing.T) {
		output, err := env.runCLI(t, "address", "--pending", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.Contains(t, output, "Found 2 unresolved comment(s) in 1 file(s)")
		assert.Contains(t, output, "- (general) (1)")
		assert.Contains(t, output, "You should also have updated README.md")
		general := strings.Index(output, "# General")
		file := strings.Index(output, "# File: "+filepath.Join(env.ProjectDir, "test.md"))
		require.NotEqual(t, -1, general)
		assert.Less(t, general, file)
	})

	t.Run("address for one file leaves general comments out", func(t *testing.T) {
		output, err := env.runCLI(t, "address", "--file", "test.md", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.NotContains(t, output, "README.md")
	})

	t.Run("the agent replies to and resolves general threads", func(t *testing.T) {
		id := fmt.Sprintf("%d", general.ID)
		_, err := env.runCLI(t, "reply", "--comment-id", id, "--message", "Updated README.md")
		require.NoError(t, err)
		output, err := env.runCLI(t, "address", "--pending", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.NotContains(t, output, "# General")

		_, err = env.runCLI(t, "resolve", "--comment-id", id)
		require.NoError(t, err)
		output, err = env.runCLI(t, "address", "--project", env.ProjectDir)
		require.NoError(t, err)
		assert.NotContains(t, output, "# General")
	})
}

// TestE2E_CLI_Address_FollowsEdits tests that threads move with their lines when the file is edited
func TestE2E_CLI_Address_FollowsEdits(t *testing.T) {
	env := setupE2E(t)
	_, err := env.runCLI(t, "register", "--project", env.ProjectDir)
	require.NoError(t, err)

	path := filepath.Join(env.ProjectDir, "anchors.md")
	require.NoError(t, os.WriteFile(path, []byte("# Plan\n\nKeep this.\n\nChange this.\n"), 0644))

	for _, c := range []struct {
		line int
		text string
	}{{3, "Keep this."}, {5, "Change this."}} {
		resp := env.postJSON(t, "/api/comments", map[string]interface{}{
			"project_directory": env.ProjectDir,
			"file_path":         "anchors.md",
			"line_start":        c.line,
			"line_end":          c.line,
			"selected_text":     c.text,
			"comment_text":      "About " + c.text,
		})
		require.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	}

	// Two lines added above both comments, and the second comment's line rewritten
	require.NoError(t, os.WriteFile(path, []byte("# Plan\n\nIntro.\n\nKeep this.\n\nChanged.\n"), 0644))

	output, err := env.runCLI(t, "address", "--file", "anchors.md", "--project", env.ProjectDir)
	require.NoError(t, err)
	assert.Contains(t, output, "(lines 5-5)\n> Keep this.")
	assert.Contains(t, output, "(lines 7-7, outdated")
	assert.Contains(t, output, "> Change this.")

	// Syncing again without further edits leaves the lines where they are
	again, err := env.runCLI(t, "address", "--file", "anchors.md", "--project", env.ProjectDir)
	require.NoError(t, err)
	assert.Equal(t, output, again)
}
