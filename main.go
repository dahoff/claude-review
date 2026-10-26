package main

import (
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Usage: claude-review <command>")
		fmt.Println("\nCommands:")
		fmt.Println("  server                   Start the web server")
		fmt.Println("  server --daemon          Start the web server as a background daemon")
		fmt.Println("  server --stop            Stop the running daemon")
		fmt.Println("  server --status          Check if the daemon is running")
		fmt.Println("  register                 Register the current project directory")
		fmt.Println("  review                   Start server, register project, and show file URL")
		fmt.Println("  address                  Show unresolved comments for a file")
		fmt.Println("  reply                    Reply to a comment thread")
		fmt.Println("  resolve                  Mark comments as resolved")
		fmt.Println("  install                  Install slash commands")
		fmt.Println("  uninstall [--purge]      Stop the server, remove slash commands and binary (--purge: also data)")
		fmt.Println("  version                  Show version information")
		os.Exit(1)
	}

	cmd := os.Args[1]

	switch cmd {
	case "server":
		runServer()
	case "register":
		runRegister()
	case "review":
		runReview()
	case "address":
		runAddress()
	case "reply":
		runReply()
	case "resolve":
		runResolve()
	case "install":
		runInstall()
	case "uninstall":
		runUninstall()
	case "version":
		runVersion()
	default:
		fmt.Printf("Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}

func runServer() {
	// Parse server flags
	serverCmd := flag.NewFlagSet("server", flag.ExitOnError)
	daemon := serverCmd.Bool("daemon", false, "Run server as a daemon")
	daemonChild := serverCmd.Bool("daemon-child", false, "Internal flag for daemon child process")
	stop := serverCmd.Bool("stop", false, "Stop the running daemon")
	status := serverCmd.Bool("status", false, "Check daemon status")

	if err := serverCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	// Handle --stop flag
	if *stop {
		if err := stopDaemon(); err != nil {
			log.Fatalf("Failed to stop daemon: %v", err)
		}
		return
	}

	// Handle --status flag
	if *status {
		if err := statusDaemon(); err != nil {
			log.Fatalf("Failed to check status: %v", err)
		}
		return
	}

	// Handle --daemon flag (parent process)
	if *daemon {
		if err := daemonize(); err != nil {
			log.Fatalf("Failed to daemonize: %v", err)
		}
		return
	}

	// Actual server logic (runs in foreground or as daemon child)
	// Setup signal handlers for graceful shutdown (always, not just daemon)
	setupSignalHandlers()

	if *daemonChild {
		// Write PID file
		if err := writePIDFile(); err != nil {
			log.Fatalf("Failed to write PID file: %v", err)
		}
	}

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Initialize templates
	if err := initTemplates(); err != nil {
		log.Fatalf("Failed to load templates: %v", err)
	}

	// Initialize file watcher
	if err := initFileWatcher(); err != nil {
		log.Fatalf("Failed to initialize file watcher: %v", err)
	}
	defer func() {
		_ = fileWatcher.close()
	}()

	port := os.Getenv("CR_LISTEN_PORT")
	if port == "" {
		port = "4779"
	}

	// Setup router
	r := chi.NewRouter()
	r.Use(middleware.Logger)
	r.Use(middleware.Recoverer)
	r.Use(localOriginOnly(port))

	// HTML Routes
	r.Get("/", handleHome)
	r.Get("/projects/*", handleProjectFiles)
	r.Get("/compare/*", handleCompare)

	// API Routes
	r.Post("/api/comments", handleCreateComment)
	r.Patch("/api/comments/{id}", handleUpdateComment)
	r.Patch("/api/comments/{id}/resolve", handleResolveThread)
	r.Delete("/api/comments/{id}", handleDeleteComment)
	r.Get("/api/events", handleSSE)
	r.Post("/api/events", handleBroadcast)

	// Static files from embedded FS
	staticSubFS, err := fs.Sub(staticFS, "frontend/static")
	if err != nil {
		log.Fatalf("Failed to create static sub-filesystem: %v", err)
	}
	r.Handle("/static/*", http.StripPrefix("/static/", http.FileServer(http.FS(staticSubFS))))

	// Start server
	if !*daemonChild {
		fmt.Printf("Starting server on http://localhost:%s\n", port)
	}
	log.Printf("Server listening on port %s", port)
	if err := http.ListenAndServe("127.0.0.1:"+port, r); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}

func runRegister() {
	// Parse flags
	registerCmd := flag.NewFlagSet("register", flag.ExitOnError)
	projectDir := registerCmd.String("project", "", "Project directory (defaults to current directory)")

	if err := registerCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	// Resolve project directory (default to current directory)
	if *projectDir == "" || *projectDir == "." {
		cwd, err := os.Getwd()
		if err != nil {
			log.Fatalf("Failed to get current directory: %v", err)
		}
		*projectDir = cwd
	}

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Register project
	_, err := createProject(*projectDir)
	if err != nil {
		log.Fatalf("Failed to register project: %v", err)
	}

	log.Printf("Registered project: %s", *projectDir)
}

func runReview() {
	// Parse flags
	reviewCmd := flag.NewFlagSet("review", flag.ExitOnError)
	projectFlag := reviewCmd.String("project", "", "Project directory (defaults to the git root or registered project)")
	filePath := reviewCmd.String("file", "", "File or directory to open (defaults to the project root)")
	diff := reviewCmd.Bool("diff", false, "Open the project's changes (git diff) instead of a file")

	if err := reviewCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	// Step 1: Start daemon if not running
	if !isServerRunning() {
		if err := daemonize(); err != nil {
			log.Fatalf("Failed to start server: %v", err)
		}
	}

	// Step 2: Initialize database and register project
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	projectDir, relPath, err := resolveTarget(*projectFlag, *filePath)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	if _, err := createProject(projectDir); err != nil {
		log.Fatalf("Failed to register project: %v", err)
	}

	// Step 3: Output URL
	port := os.Getenv("CR_LISTEN_PORT")
	if port == "" {
		port = "4779"
	}

	name := filepath.Base(projectDir)
	if *diff {
		reviewURL := fmt.Sprintf("http://localhost:%s/compare%s", port, escapePathComponents(projectDir))
		fmt.Printf("Open this URL in your browser to review changes in %s:\n\n%s\n", name, reviewURL)
		return
	}

	reviewURL := fmt.Sprintf("http://localhost:%s/projects%s/", port, escapePathComponents(projectDir))
	if relPath != "" {
		reviewURL += escapePathComponents(relPath)
		name = relPath
		if info, err := os.Stat(filepath.Join(projectDir, relPath)); err == nil && info.IsDir() {
			reviewURL += "/"
		}
	}
	fmt.Printf("Open this URL in your browser to start reviewing %s:\n\n%s\n", name, reviewURL)
}

func runAddress() {
	// Parse flags
	addressCmd := flag.NewFlagSet("address", flag.ExitOnError)
	projectFlag := addressCmd.String("project", "", "Project directory")
	filePath := addressCmd.String("file", "", "File path (defaults to all files in the project)")
	pending := addressCmd.Bool("pending", false, "Only show threads awaiting an agent response")
	claim := addressCmd.Bool(
		"claim",
		false,
		"Lock the listed threads against editing in the browser until the agent replies",
	)

	if err := addressCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	projectDir, relPath, err := resolveTarget(*projectFlag, *filePath)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}

	// Get unresolved comments, for one file or the whole project
	var comments []Comment
	scope := relPath
	if relPath == "" {
		scope = projectDir
		comments, err = getProjectComments(projectDir, false)
	} else {
		comments, err = getComments(projectDir, relPath, false)
	}
	if err != nil {
		log.Fatalf("Failed to get comments: %v", err)
	}

	// Format and output comments
	if len(comments) == 0 {
		fmt.Printf("No unresolved comments for %s\n", scope)
		return
	}

	// Group comments by thread (root comments and their replies)
	threads := groupCommentsByThread(comments)

	if *pending {
		threads = filterPendingThreads(threads)
		if len(threads) == 0 {
			fmt.Printf("No unresolved comments awaiting a response for %s\n", scope)
			return
		}
	}

	if *claim {
		claimAndNotify(threads)
	}

	if relPath != "" {
		fmt.Printf("Found %d unresolved comment(s) for %s:\n\n", len(threads), relPath)
		printThreads(threads)
		return
	}

	// Project-wide: list the files with comments, then each file's threads
	var files []string
	threadsByFile := make(map[string][][]Comment)
	for _, thread := range threads {
		file := thread[0].FilePath
		if _, seen := threadsByFile[file]; !seen {
			files = append(files, file)
		}
		threadsByFile[file] = append(threadsByFile[file], thread)
	}

	fmt.Printf("Found %d unresolved comment(s) in %d file(s) of %s:\n", len(threads), len(files), projectDir)
	for _, file := range files {
		fmt.Printf("- %s (%d)\n", file, len(threadsByFile[file]))
	}

	for _, file := range files {
		fmt.Printf("\n# File: %s\n\n", filepath.Join(projectDir, file))
		printThreads(threadsByFile[file])
	}
}

// claimAndNotify marks threads as being processed by the agent and tells open
// browsers, so they disable their edit buttons without reloading.
func claimAndNotify(threads [][]Comment) {
	rootIDs := make([]int, 0, len(threads))
	files := make(map[string]bool)
	for _, thread := range threads {
		rootIDs = append(rootIDs, thread[0].ID)
		files[thread[0].FilePath] = true
	}

	if err := claimThreads(rootIDs); err != nil {
		log.Fatalf("Failed to claim threads: %v", err)
	}

	for file := range files {
		notifyServer(threads[0][0].ProjectDirectory, file, "threads_claimed")
	}
}

func printThreads(threads [][]Comment) {
	for threadIndex, thread := range threads {
		rootComment := thread[0]

		// Show root comment with line numbers
		lineRange := ""
		if rootComment.LineStart != nil && rootComment.LineEnd != nil {
			lineRange = fmt.Sprintf(" (lines %d-%d)", *rootComment.LineStart, *rootComment.LineEnd)
		}
		fmt.Printf("## Comment #%d%s\n", rootComment.ID, lineRange)

		// Show selected text for root comment
		if rootComment.SelectedText != "" {
			selectedLines := strings.Split(rootComment.SelectedText, "\n")
			for _, line := range selectedLines {
				fmt.Printf("> %s\n", line)
			}
			fmt.Println()
		}

		// Show root comment text
		fmt.Printf("**%s:**\n", capitalizeFirst(rootComment.Author))
		fmt.Printf("%s\n", rootComment.CommentText)

		// Show replies
		if len(thread) > 1 {
			fmt.Println()
			for _, reply := range thread[1:] {
				fmt.Printf("\n**Reply from %s:**\n", capitalizeFirst(reply.Author))
				fmt.Printf("%s\n", reply.CommentText)
			}
		}

		if threadIndex < len(threads)-1 {
			fmt.Printf("\n---\n\n")
		}
	}
}

// filterPendingThreads drops threads where the agent wrote the last message.
func filterPendingThreads(threads [][]Comment) [][]Comment {
	pending := make([][]Comment, 0, len(threads))
	for _, thread := range threads {
		if thread[len(thread)-1].Author != "agent" {
			pending = append(pending, thread)
		}
	}
	return pending
}

func groupCommentsByThread(comments []Comment) [][]Comment {
	threads := make([][]Comment, 0)
	threadMap := make(map[int][]Comment)

	// First pass: group comments by root ID
	for _, comment := range comments {
		if comment.RootID == nil {
			// This is a root comment
			threadMap[comment.ID] = []Comment{comment}
		} else {
			// This is a reply
			threadMap[*comment.RootID] = append(threadMap[*comment.RootID], comment)
		}
	}

	// Second pass: create ordered list of threads
	seenRoots := make(map[int]bool)
	for _, comment := range comments {
		rootID := comment.ID
		if comment.RootID != nil {
			rootID = *comment.RootID
		}

		if !seenRoots[rootID] {
			seenRoots[rootID] = true
			if thread, exists := threadMap[rootID]; exists {
				threads = append(threads, thread)
			}
		}
	}

	return threads
}

func runReply() {
	// Parse flags
	replyCmd := flag.NewFlagSet("reply", flag.ExitOnError)
	commentID := replyCmd.Int("comment-id", 0, "ID of the comment to reply to")
	message := replyCmd.String("message", "", "Reply message")

	if err := replyCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	if *commentID == 0 {
		fmt.Println("Error: --comment-id flag is required")
		os.Exit(1)
	}

	if *message == "" {
		fmt.Println("Error: --message flag is required")
		os.Exit(1)
	}

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Get the comment to reply to
	parentComment, err := getCommentByID(*commentID)
	if err != nil {
		log.Fatalf("Failed to get comment: %v", err)
	}
	if parentComment == nil {
		fmt.Printf("Error: comment %d not found\n", *commentID)
		os.Exit(1)
	}

	// Ensure we're replying to a root comment (not a reply)
	if parentComment.RootID != nil {
		fmt.Println("Error: can only reply to root comments, not to replies")
		os.Exit(1)
	}

	// Create the reply
	reply := &Comment{
		ProjectDirectory: parentComment.ProjectDirectory,
		FilePath:         parentComment.FilePath,
		CommentText:      *message,
		Author:           "agent",
		RootID:           &parentComment.ID,
	}

	if err := createComment(reply); err != nil {
		log.Fatalf("Failed to create reply: %v", err)
	}

	// The agent has responded, so the user may edit the thread again
	if err := releaseThread(parentComment.ID); err != nil {
		log.Fatalf("Failed to release thread: %v", err)
	}

	fmt.Printf("Reply added to comment %d\n", *commentID)

	// Notify server about the new reply (if server is running)
	notifyServerCommentsChanged(parentComment.ProjectDirectory, parentComment.FilePath)
}

func runResolve() {
	// Parse flags
	resolveCmd := flag.NewFlagSet("resolve", flag.ExitOnError)
	projectFlag := resolveCmd.String("project", "", "Project directory")
	filePath := resolveCmd.String("file", "", "File path relative to project directory")
	commentID := resolveCmd.Int("comment-id", 0, "ID of specific comment to resolve")

	if err := resolveCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	// Initialize database
	if err := initDB(); err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}

	// Handle comment-id mode
	if *commentID != 0 {
		comment, err := getCommentByID(*commentID)
		if err != nil {
			log.Fatalf("Failed to get comment: %v", err)
		}
		if comment == nil {
			fmt.Printf("Error: comment %d not found\n", *commentID)
			os.Exit(1)
		}

		// Get the root comment ID
		rootID := *commentID
		if comment.RootID != nil {
			rootID = *comment.RootID
		}

		// Resolve the thread
		count, err := resolveThread(rootID, "user")
		if err != nil {
			log.Fatalf("Failed to resolve thread: %v", err)
		}

		if count == 0 {
			fmt.Printf("Thread %d was already resolved\n", rootID)
		} else {
			fmt.Printf("Resolved thread %d (%d comment(s))\n", rootID, count)

			// Notify server
			notifyServerCommentsChanged(comment.ProjectDirectory, comment.FilePath)
		}
		return
	}

	// Handle file mode
	if *filePath == "" {
		fmt.Println("Error: --file flag is required (or use --comment-id)")
		os.Exit(1)
	}

	projectDir, relPath, err := resolveTarget(*projectFlag, *filePath)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		os.Exit(1)
	}
	*filePath = relPath

	// Resolve comments
	count, err := resolveComments(projectDir, *filePath)
	if err != nil {
		log.Fatalf("Failed to resolve comments: %v", err)
	}

	if count == 0 {
		fmt.Printf("No unresolved comments found for %s\n", *filePath)
	} else {
		fmt.Printf("Resolved %d comment(s) for %s\n", count, *filePath)

		// Notify server about resolved comments (if server is running)
		notifyServerCommentsChanged(projectDir, *filePath)
	}
}

func runInstall() {
	if err := installSlashCommands(); err != nil {
		log.Fatalf("Failed to install slash commands: %v", err)
	}
}

func runUninstall() {
	uninstallCmd := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := uninstallCmd.Bool("purge", false, "Also delete the data directory (comments database, PID file and logs)")

	if err := uninstallCmd.Parse(os.Args[2:]); err != nil {
		log.Fatalf("Failed to parse flags: %v", err)
	}

	if err := uninstall(*purge); err != nil {
		log.Fatalf("Failed to uninstall: %v", err)
	}
}

func runVersion() {
	fmt.Println(Version)
}

func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
