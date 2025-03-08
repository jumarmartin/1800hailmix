package main

import (
	"bytes"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// Recording represents a phone recording with MP3 attachment
type Recording struct {
	ID            string `json:"id"`            // Unique identifier
	PhoneNumber   string `json:"phoneNumber"`   // Phone number of sender
	ReceivedAt    string `json:"receivedAt"`    // Date received
	MP3FileName   string `json:"mp3FileName"`   // Name of the MP3 file
	FilePath      string `json:"filePath"`      // Path to the saved MP3 file
	FileSize      int64  `json:"fileSize"`      // Size of the MP3 file in bytes
	Transcription string `json:"transcription"` // Whisper transcription of audio
	Title         string `json:"title"`         // LLM-generated title for the recording
}

// EmailData represents the structure of incoming email data
type EmailData struct {
	Subject     string           `json:"subject"`
	Sender      string           `json:"sender"`
	ReceivedAt  string           `json:"receivedAt"`
	Attachments []AttachmentData `json:"attachments,omitempty"`
}

// AttachmentData represents an email attachment
type AttachmentData struct {
	FileName string `json:"fileName"`
	Content  string `json:"content"` // Base64 encoded content
	MimeType string `json:"mimeType"`
}

// OpenAI API compatible request/response structures
type OpenAIRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	MaxTokens   int       `json:"max_tokens,omitempty"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type OpenAIResponse struct {
	ID      string   `json:"id"`
	Object  string   `json:"object"`
	Created int64    `json:"created"`
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
	Index        int     `json:"index"`
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// Configuration
const (
	dataDir       = "./data"
	attachmentDir = "./data/mp3s"
	dbPath        = "./data/recordings.duckdb"
)

// Phone number extraction regex
var phoneRegex = regexp.MustCompile(`\+?1?\s*\(?(\d{3})\)?[-.\s]?(\d{3})[-.\s]?(\d{4})`)

// Create a regexp that matches the thin non-breaking space
var nastyTimeRegex = regexp.MustCompile("\xe2\x80\xaf")

// Database connection
var db *sql.DB

func main() {
	// Ensure data directories exist
	os.MkdirAll(dataDir, 0755)
	os.MkdirAll(attachmentDir, 0755)

	// Initialize DuckDB and create tables
	initDuckDB()
	defer db.Close()

	// Setup routes
	http.HandleFunc("/", homeHandler)
	http.HandleFunc("/api/recordings", corsMiddleware(recordingsHandler))
	http.HandleFunc("/api/webhook", corsMiddleware(webhookHandler))
	http.HandleFunc("/api/play/", corsMiddleware(playHandler))

	// Start server
	port := "8080"
	fmt.Printf("Server starting on port %s...\n", port)
	log.Fatal(http.ListenAndServe(":"+port, nil))
}

// Initialize the DuckDB database
func initDuckDB() {
	var err error
	// Connect to DuckDB
	db, err = sql.Open("duckdb", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	// Create the recordings table if it doesn't exist
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS recordings (
			id VARCHAR PRIMARY KEY,
			phone_number VARCHAR,
			received_at VARCHAR,
			mp3_file_name VARCHAR,
			file_path VARCHAR,
			file_size BIGINT,
			transcription TEXT,
			title VARCHAR
		)
	`)
	if err != nil {
		log.Fatalf("Failed to create table: %v", err)
	}

	log.Println("DuckDB database initialized successfully")
}

// CORS middleware to allow frontend to communicate with backend
func corsMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*") // Allow any origin
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		// Call the next handler
		next(w, r)
	}
}

func homeHandler(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Phone Recording Service")
}

// Handler for /api/recordings endpoint
func recordingsHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// Get all recordings from database
		rows, err := db.Query(`
			SELECT id, phone_number, received_at, mp3_file_name, file_path, file_size, transcription, title
			FROM recordings 
			ORDER BY received_at DESC
		`)
		if err != nil {
			http.Error(w, "Database query error", http.StatusInternalServerError)
			log.Printf("Database query error: %v", err)
			return
		}
		defer rows.Close()

		// Parse results into recordings slice
		var recordings []Recording
		for rows.Next() {
			var rec Recording
			var receivedAt string
			err := rows.Scan(&rec.ID, &rec.PhoneNumber, &receivedAt, &rec.MP3FileName, &rec.FilePath, &rec.FileSize, &rec.Transcription, &rec.Title)
			if err != nil {
				log.Printf("Error scanning row: %v", err)
				continue // Skip this record if there's an error
			}

			// Parse received_at string into time.Time
			t, err := time.Parse("Monday, January 2, 2006 at 3:04:05 PM", receivedAt)
			if err != nil {
				log.Printf("Error parsing date: %v", err)
				rec.ReceivedAt = time.Now().Format("2006-01-02 15:04:05") // Use current time as fallback
			} else {
				rec.ReceivedAt = t.Format("2006-01-02 15:04:05")
			}

			recordings = append(recordings, rec)
		}

		// If no recordings found, return an empty array
		if recordings == nil {
			recordings = []Recording{}
		}

		// Return recordings as JSON
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(recordings)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// Handler for /api/webhook endpoint
func webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Read the request body
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Error reading request body", http.StatusInternalServerError)
		return
	}
	defer r.Body.Close()

	// Log received data for debugging
	log.Printf("Received webhook data: %s", string(body))

	// Parse the JSON data
	var email EmailData

	body = nastyTimeRegex.ReplaceAll(body, []byte(" "))
	if err := json.Unmarshal(body, &email); err != nil {
		http.Error(w, "Error parsing JSON", http.StatusBadRequest)
		log.Printf("Error parsing JSON: %v", err)
		return
	}

	// Set received time if not provided
	if email.ReceivedAt == "" {
		email.ReceivedAt = time.Now().Format("2006-01-02 15:04:05")
	}

	// Extract phone number from sender field
	phoneNumber := extractPhoneNumber(email.Subject)
	if phoneNumber == "" {
		log.Printf("Could not extract phone number from sender: %s", email.Sender)
		phoneNumber = "Unknown"
	}

	// Process attachments (looking for MP3 files)
	var recordingID string
	log.Printf("Processing email with %d attachments", len(email.Attachments))
	for _, attachment := range email.Attachments {
		log.Printf("Processing attachment: %s", attachment.FileName)
		if strings.HasSuffix(strings.ToLower(attachment.FileName), ".mp3") {
			log.Printf("Found MP3 attachment: %s", attachment.FileName)

			// Generate a unique ID for this recording
			recordingID = generateID()

			// Save the MP3 file to disk
			filePath := filepath.Join(attachmentDir, recordingID+".mp3")
			if err := saveAttachment(attachment, filePath); err != nil {
				log.Printf("Error saving attachment: %v", err)
				continue
			}

			// Get file size
			fileInfo, err := os.Stat(filePath)
			if err != nil {
				log.Printf("Error getting file info: %v", err)
				continue
			}

			// Create recording record
			recording := Recording{
				ID:            recordingID,
				PhoneNumber:   phoneNumber,
				ReceivedAt:    email.ReceivedAt,
				MP3FileName:   attachment.FileName,
				FilePath:      filePath,
				FileSize:      fileInfo.Size(),
				Transcription: "",
				Title:         "",
			}

			// Transcribe the audio using whisper.cpp
			log.Printf("Transcribing audio file: %s", filePath)
			transcription, err := transcribeAudio(filePath)
			if err != nil {
				log.Printf("Error transcribing audio: %v", err)
				recording.Transcription = "Transcription failed: " + err.Error()
				recording.Title = "Untitled Voicemail"
			} else {
				recording.Transcription = transcription
				log.Printf("Transcription successful (%d characters)", len(transcription))

				// Only generate a title if we have a valid transcription
				if transcription != "" {
					// Generate a title using Ollama
					log.Printf("Generating title for recording")
					title, err := generateTitle(transcription)
					if err != nil {
						log.Printf("Error generating title: %v", err)
						recording.Title = "Untitled Voicemail"
					} else {
						recording.Title = title
						log.Printf("Title generated: %s", title)
					}
				} else {
					log.Printf("Empty transcription, skipping title generation")
					recording.Title = "Untitled Voicemail"
				}
			}

			// Save to DuckDB
			_, err = db.Exec(`
				INSERT INTO recordings (id, phone_number, received_at, mp3_file_name, file_path, file_size, transcription, title)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?)
			`, recording.ID, recording.PhoneNumber, recording.ReceivedAt,
				recording.MP3FileName, recording.FilePath, recording.FileSize, recording.Transcription, recording.Title)

			if err != nil {
				log.Printf("Error saving recording to database: %v", err)
			} else {
				log.Printf("Saved recording to database: %s", recording.ID)
			}

			break // Only process the first MP3 for now
		}
	}

	// Return success response
	w.WriteHeader(http.StatusCreated)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"message": "Recording data received",
		"id":      recordingID,
	})
}

// Handler for /api/play/:id endpoint
func playHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the recording ID from the URL
	recordingID := strings.TrimPrefix(r.URL.Path, "/api/play/")
	if recordingID == "" {
		http.Error(w, "Recording ID required", http.StatusBadRequest)
		return
	}

	// Look up file path in DuckDB
	var filePath string
	row := db.QueryRow(`
		SELECT file_path FROM recordings WHERE id = ?
	`, recordingID)

	err := row.Scan(&filePath)
	if err != nil {
		log.Printf("Error looking up recording ID %s: %v", recordingID, err)
		http.Error(w, "Recording not found", http.StatusNotFound)
		return
	}

	// Check if file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		log.Printf("MP3 file not found at path: %s", filePath)
		http.Error(w, "MP3 file not found", http.StatusNotFound)
		return
	}

	// Set the content type to audio/mpeg
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Content-Disposition", "inline")

	// Serve the file
	http.ServeFile(w, r, filePath)
}

// Helper functions

// Extract phone number from a string
func extractPhoneNumber(s string) string {
	matches := phoneRegex.FindStringSubmatch(s)
	if len(matches) >= 4 {
		switch {
		case matches[1] == "704" && matches[3][2:] == "90":
			return "I HATE THIS MAN"
		default:
			return fmt.Sprintf("(%s) %s-%s", matches[1], matches[2], matches[3])
		}
	}
	return ""
}

// Generate a unique ID
func generateID() string {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return base64.URLEncoding.EncodeToString(b)
}

// Save attachment to disk
func saveAttachment(attachment AttachmentData, filePath string) error {
	// Decode base64 content
	content, err := base64.StdEncoding.DecodeString(attachment.Content)
	if err != nil {
		return fmt.Errorf("base64 decode error: %w", err)
	}

	// Create file
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("file creation error: %w", err)
	}
	defer file.Close()

	// Write content to file
	_, err = file.Write(content)
	if err != nil {
		return fmt.Errorf("file write error: %w", err)
	}

	return nil
}

// Transcribe audio using whisper.cpp
func transcribeAudio(filePath string) (string, error) {
	// Check if whisper.cpp command-line tool is available
	if _, err := exec.LookPath("../whisper.cpp/build/bin/whisper-cli"); err != nil {
		return "", fmt.Errorf("whisper.cpp not found in PATH: %w", err)
	}

	// Convert MP3 to WAV for better compatibility with whisper.cpp (if needed)
	// This assumes ffmpeg is installed
	wavPath := strings.TrimSuffix(filePath, filepath.Ext(filePath)) + ".wav"
	ffmpegCmd := exec.Command("ffmpeg", "-i", filePath, "-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wavPath)
	if err := ffmpegCmd.Run(); err != nil {
		return "", fmt.Errorf("failed to convert MP3 to WAV: %w", err)
	}
	defer os.Remove(wavPath) // Clean up temporary WAV file

	// Run whisper.cpp transcription using the base model for now
	// Adjust model path and parameters as needed based on your system configuration
	// Common whisper.cpp parameters:
	// -m MODEL_PATH: path to the model file
	// -f AUDIO_PATH: path to the audio file
	// --output-txt: output results to a text file
	// --model MODEL: use a specific model (tiny, base, small, medium, large)
	// --language LANG: force a specific language
	cmd := exec.Command("../whisper.cpp/build/bin/whisper-cli", "--model", "../whisper.cpp/models/ggml-base.en.bin", "--output-txt", "-f", wavPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("whisper transcription failed: %w\nstderr: %s", err, stderr.String())
	}

	// Parse output to get transcription text
	// When using --output-txt, whisper.cpp creates a .txt file with the same name as the input
	transcriptionFile := strings.TrimSuffix(wavPath, filepath.Ext(wavPath)) + ".txt"

	// Check if the transcription file exists
	if _, err := os.Stat(transcriptionFile); os.IsNotExist(err) {
		// If file doesn't exist, use stdout as fallback
		transcription := stdout.String()
		return strings.TrimSpace(transcription), nil
	}

	// Read the transcription file
	transcriptionBytes, err := os.ReadFile(transcriptionFile)
	if err != nil {
		return "", fmt.Errorf("failed to read transcription file: %w", err)
	}

	// Clean up the transcription file
	os.Remove(transcriptionFile)

	// Clean up the transcription text if needed
	transcription := strings.TrimSpace(string(transcriptionBytes))

	return transcription, nil
}

// Generate a title for the voicemail using Ollama via OpenAI-compatible API
func generateTitle(transcription string) (string, error) {
	// Ollama endpoint for OpenAI-compatible chat completions
	url := "http://localhost:11434/v1/chat/completions"

	// Create the system prompt
	systemPrompt := "You are a helpful assistant that generates very short, clear titles for voicemail transcriptions. Keep your titles under 8 words, be descriptive but concise, focusing on the main point. Don't use quotes in your response and only return the title, no other text preceded by a colon."

	// Create the user prompt with the transcription
	userPrompt := fmt.Sprintf("Please create a title for this voicemail transcription:\n\n%s", transcription)

	// Create the request payload
	requestData := OpenAIRequest{
		Model: "llama2", // Use installed Ollama model name, e.g., "llama3", "mistral", "gemma:7b"
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature: 0.7,
		MaxTokens:   50,
	}

	// Convert request to JSON
	requestJSON, err := json.Marshal(requestData)
	if err != nil {
		return "Untitled Voicemail", fmt.Errorf("failed to marshal JSON request: %w", err)
	}

	// Create the HTTP request
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(requestJSON))
	if err != nil {
		return "Untitled Voicemail", fmt.Errorf("failed to create HTTP request: %w", err)
	}

	// Add headers
	req.Header.Set("Content-Type", "application/json")

	// Send the request
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "Untitled Voicemail", fmt.Errorf("failed to send request to Ollama: %w", err)
	}
	defer resp.Body.Close()

	// Read the response
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "Untitled Voicemail", fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if the response is successful
	if resp.StatusCode != http.StatusOK {
		return "Untitled Voicemail", fmt.Errorf("Ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Parse the response
	var openAIResponse OpenAIResponse
	if err := json.Unmarshal(body, &openAIResponse); err != nil {
		return "Untitled Voicemail", fmt.Errorf("failed to parse Ollama response: %w", err)
	}

	// Extract the title from the response
	if len(openAIResponse.Choices) == 0 {
		return "Untitled Voicemail", fmt.Errorf("no content in Ollama response")
	}

	// Get the response content
	title := openAIResponse.Choices[0].Message.Content

	// Clean up the title
	title = strings.TrimSpace(title)

	// Remove any quotes that might be in the response
	title = strings.Trim(title, "\"'")

	// remove anything preceded by a colon
	title = strings.TrimPrefix(title, ":")

	// If the title is empty, provide a default
	if title == "" {
		title = "Untitled Voicemail"
	}

	return title, nil
}
