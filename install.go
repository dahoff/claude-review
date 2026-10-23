package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func installSlashCommands() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	commandsDir := filepath.Join(homeDir, ".claude", "commands")

	// Create commands directory if it doesn't exist
	if err := os.MkdirAll(commandsDir, 0755); err != nil {
		return fmt.Errorf("failed to create commands directory: %w", err)
	}

	// Install all slash commands from embedded FS
	var installed []string
	err = fs.WalkDir(slashCommandsFS, "slash-commands", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		// Read embedded slash command
		commandContent, err := slashCommandsFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("failed to read %s: %w", path, err)
		}

		// Write to ~/.claude/commands/
		filename := filepath.Base(path)
		commandPath := filepath.Join(commandsDir, filename)
		if err := os.WriteFile(commandPath, commandContent, 0644); err != nil {
			return fmt.Errorf("failed to write %s: %w", filename, err)
		}

		installed = append(installed, filename)
		return nil
	})

	if err != nil {
		return err
	}

	fmt.Printf("Successfully installed %d slash command(s) to %s:\n", len(installed), commandsDir)
	for _, name := range installed {
		cmdName := strings.TrimSuffix(name, ".md")
		fmt.Printf("  /%s\n", cmdName)
	}

	return nil
}

func uninstallSlashCommands() error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	commandsDir := filepath.Join(homeDir, ".claude", "commands")

	// Collect all slash command filenames from embedded FS
	var toUninstall []string
	err = fs.WalkDir(slashCommandsFS, "slash-commands", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if d.IsDir() || !strings.HasSuffix(path, ".md") {
			return nil
		}

		toUninstall = append(toUninstall, filepath.Base(path))
		return nil
	})

	if err != nil {
		return err
	}

	// Remove each command file
	var removed []string
	for _, filename := range toUninstall {
		commandPath := filepath.Join(commandsDir, filename)
		err := os.Remove(commandPath)
		if err != nil {
			if os.IsNotExist(err) {
				// File doesn't exist, skip silently
				continue
			}
			return fmt.Errorf("failed to remove %s: %w", filename, err)
		}
		removed = append(removed, filename)
	}

	if len(removed) == 0 {
		fmt.Println("No slash commands were installed")
		return nil
	}

	fmt.Printf("Successfully uninstalled %d slash command(s) from %s:\n", len(removed), commandsDir)
	for _, name := range removed {
		cmdName := strings.TrimSuffix(name, ".md")
		fmt.Printf("  /%s\n", cmdName)
	}

	return nil
}

// uninstall removes everything install.sh installs: it stops the server, removes the
// slash commands and the binary in ~/.local/bin, and with purge also deletes the
// data directory (comments database, PID file and logs)
func uninstall(purge bool) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	dataDir, err := dataDirPath()
	if err != nil {
		return err
	}
	_, statErr := os.Stat(dataDir)
	dataDirExists := statErr == nil

	// Only look for a running server when the data directory exists, since the PID
	// helpers would otherwise create it
	if dataDirExists {
		if err := stopServerAndWait(); err != nil {
			return err
		}
	}

	if err := uninstallSlashCommands(); err != nil {
		return err
	}

	binaryPath := filepath.Join(homeDir, ".local", "bin", "claude-review")
	if err := os.Remove(binaryPath); err == nil {
		fmt.Printf("Removed binary %s\n", binaryPath)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("failed to remove %s: %w", binaryPath, err)
	}

	if !dataDirExists {
		return nil
	}

	if !purge {
		fmt.Printf("Kept data directory %s (comments database and logs); run with --purge to delete it\n", dataDir)
		return nil
	}

	if err := os.RemoveAll(dataDir); err != nil {
		return fmt.Errorf("failed to remove data directory %s: %w", dataDir, err)
	}
	fmt.Printf("Removed data directory %s\n", dataDir)

	return nil
}

// stopServerAndWait stops the daemon if it is running and waits for it to exit
func stopServerAndWait() error {
	if !isServerRunning() {
		return nil
	}

	pid, err := readPID()
	if err != nil {
		return fmt.Errorf("failed to read PID file: %w", err)
	}

	if err := stopDaemon(); err != nil {
		return fmt.Errorf("failed to stop server: %w", err)
	}

	for i := 0; i < 50 && isProcessRunning(pid); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if isProcessRunning(pid) {
		return fmt.Errorf("server (PID: %d) did not exit after SIGTERM", pid)
	}

	return nil
}
