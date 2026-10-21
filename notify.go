package main

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"os"
)

// notifyServerCommentsChanged sends a broadcast event to the server
// to notify connected clients that comments have changed
func notifyServerCommentsChanged(projectDir, filePath string) {
	notifyServer(projectDir, filePath, "comments_resolved")
}

// notifyServer asks the server to broadcast an SSE event to the browsers viewing a file
func notifyServer(projectDir, filePath, event string) {
	port := os.Getenv("CR_LISTEN_PORT")
	if port == "" {
		port = "4779"
	}

	payload := map[string]string{
		"project_directory": projectDir,
		"file_path":         filePath,
		"event":             event,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("Failed to marshal broadcast payload: %v", err)
		return
	}

	resp, err := http.Post("http://localhost:"+port+"/api/events", "application/json", bytes.NewReader(data))
	if err != nil {
		// Server might not be running - just log and continue
		log.Printf("Note: Could not notify server (server might not be running): %v", err)
		return
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		log.Printf("Server returned non-OK status: %d", resp.StatusCode)
	}
}
