package main

import (
	"context"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"
)

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080" // Default port if not set by Railway
		log.Printf("Defaulting to port %s", port)
	}

	token := os.Getenv("GITHUB_PERSONAL_ACCESS_TOKEN")
	if token == "" {
		log.Fatal("Error: GITHUB_PERSONAL_ACCESS_TOKEN environment variable not set.")
	}

	http.HandleFunc("/", handleRequest(token))

	log.Printf("Wrapper server starting on port %s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}

func handleRequest(token string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		log.Printf("Received request: %s %s", r.Method, r.URL.Path)

		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}

		requestBody, err := io.ReadAll(r.Body)
		if err != nil {
			log.Printf("Error reading request body: %v", err)
			http.Error(w, "Error reading request", http.StatusInternalServerError)
			return
		}
		r.Body.Close()

		log.Printf("Forwarding request body (size: %d) to github-mcp-server", len(requestBody))

		// --- Execute github-mcp-server --- 
		// Set a timeout for the command execution
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second) // 30 second timeout
		defer cancel()

		cmd := exec.CommandContext(ctx, "/server/github-mcp-server", "stdio")
		cmd.Env = append(os.Environ(), "GITHUB_PERSONAL_ACCESS_TOKEN="+token)

		stdin, err := cmd.StdinPipe()
		if err != nil {
			log.Printf("Error getting stdin pipe: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		stdout, err := cmd.StdoutPipe()
		if err != nil {
			log.Printf("Error getting stdout pipe: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		stderr, err := cmd.StderrPipe()
		if err != nil {
			log.Printf("Error getting stderr pipe: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		// --------------------------------

		// Start the command
		if err := cmd.Start(); err != nil {
			log.Printf("Error starting github-mcp-server: %v", err)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		// Goroutine to capture stderr
		go func() {
			stdErrBytes, _ := io.ReadAll(stderr)
			if len(stdErrBytes) > 0 {
				log.Printf("github-mcp-server stderr: %s", string(stdErrBytes))
			}
		}()

		// Write request body to stdin
		_, err = stdin.Write(requestBody)
		if err != nil {
			log.Printf("Error writing to stdin: %v", err)
			// Don't return http error yet, try to read stdout/wait for process
		}
		// Close stdin to signal end of input to the MCP server
		if err = stdin.Close(); err != nil {
		    log.Printf("Error closing stdin: %v", err)
		}


		// Read response from stdout
		responseBody, err := io.ReadAll(stdout)
		if err != nil {
			log.Printf("Error reading from stdout: %v", err)
			// Don't return http error yet, wait for process exit
		}

		// Wait for the command to finish
		if err := cmd.Wait(); err != nil {
			log.Printf("github-mcp-server exited with error: %v", err)
			// If we didn't get a response body, return an error
			if len(responseBody) == 0 {
			    http.Error(w, "Internal server error processing request", http.StatusInternalServerError)
			    return
			}
			// Otherwise, log the error but proceed with the response we got
		}

		log.Printf("Received response body (size: %d) from github-mcp-server", len(responseBody))

		// Send response back to client
		w.Header().Set("Content-Type", "application/json") // Assuming MCP uses JSON
		w.WriteHeader(http.StatusOK)
		_, err = w.Write(responseBody)
        if err != nil {
            log.Printf("Error writing HTTP response: %v", err)
        }
	}
} 