package main

import (
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	_ "github.com/marcboeker/go-duckdb"
)

// Recording represents a phone recording with MP3 attachment
type Recording struct {
	ID          string `json:"id"`          // Unique identifier
	PhoneNumber string `json:"phoneNumber"` // Phone number of sender
	ReceivedAt  string `json:"receivedAt"`  // Date received
	MP3FileName string `json:"mp3FileName"` // Name of the MP3 file
	FilePath    string `json:"filePath"`    // Path to the saved MP3 file
	FileSize    int64  `json:"fileSize"`    // Size of the MP3 file in bytes
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
			file_size BIGINT
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
			SELECT id, phone_number, received_at, mp3_file_name, file_path, file_size
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
			err := rows.Scan(&rec.ID, &rec.PhoneNumber, &receivedAt, &rec.MP3FileName, &rec.FilePath, &rec.FileSize)
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
				ID:          recordingID,
				PhoneNumber: phoneNumber,
				ReceivedAt:  email.ReceivedAt,
				MP3FileName: attachment.FileName,
				FilePath:    filePath,
				FileSize:    fileInfo.Size(),
			}

			// Save to DuckDB
			_, err = db.Exec(`
				INSERT INTO recordings (id, phone_number, received_at, mp3_file_name, file_path, file_size)
				VALUES (?, ?, ?, ?, ?, ?)
			`, recording.ID, recording.PhoneNumber, recording.ReceivedAt,
				recording.MP3FileName, recording.FilePath, recording.FileSize)

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
		return fmt.Sprintf("(%s) %s-%s", matches[1], matches[2], matches[3])
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
