# Phone Voice Messages Pipeline

This project creates a pipeline from Apple Mail to a web application for processing and playing phone voice messages (MP3 files). It consists of two main components:

1. **Backend (Go)**: A Go server that receives MP3 attachments from Mail.app, stores them on disk, and saves metadata in DuckDB
2. **Frontend (React)**: A React application that displays and plays the voice recordings

## Key Features

- Automatically extracts MP3 attachments from emails in Mail.app
- Extracts phone numbers from sender information
- Stores recordings in DuckDB database for persistence
- Provides a clean web interface to play voice messages

## Setup and Installation

### Backend Setup

1. Navigate to the backend directory:
   ```
   cd backend
   ```

2. Install dependencies:
   ```
   go mod tidy
   ```

3. Run the Go server:
   ```
   go run main.go
   ```

4. Set up the Mail.app integration:
   ```
   ./setup_mail_integration.sh
   ```

### Frontend Setup

1. Navigate to the frontend directory:
   ```
   cd frontend
   ```

2. Install dependencies:
   ```
   npm install
   ```

3. Start the development server:
   ```
   npm start
   ```

## How It Works

1. The AppleScript (`mail_to_backend.scpt`) monitors your Mail.app inbox for new emails
2. When an email with an MP3 attachment is detected, it:
   - Extracts the sender information (phone number)
   - Saves the MP3 attachment in base64 format
   - Sends this data to the backend API endpoint (`/api/webhook`)
3. The backend:
   - Extracts phone numbers using regex
   - Saves the MP3 file to disk
   - Stores metadata in DuckDB (phone number, timestamp, file path)
4. The frontend:
   - Displays a list of voice recordings
   - Shows phone number and received time for each recording
   - Allows playing the recordings directly in the browser

## API Endpoints

- `GET /api/recordings` - Get a list of all voice recordings
- `POST /api/webhook` - Webhook for receiving new voice recordings from Mail.app
- `GET /api/play/:id` - Stream an MP3 file for playback

## Customizing

### Change Phone Number Detection

To modify how phone numbers are detected, edit the regex pattern in `main.go`:

```go
var phoneRegex = regexp.MustCompile(`\+?1?\s*\(?(\d{3})\)?[-.\s]?(\d{3})[-.\s]?(\d{4})`)
```

### Add Authentication

For production use, consider adding authentication to the API endpoints:

1. Implement a simple API key system
2. Add proper JWT authentication
3. Set up OAuth2 for more secure access

## Troubleshooting

- If recordings aren't being processed, check the logs in `backend/mail_output.log` and `backend/mail_error.log`
- Make sure Mail.app is running and you have permissions set correctly
- Check that the backend server is running on port 8080
- Verify the launchd job is running with `launchctl list | grep com.urinal.mail-processor`
- If DuckDB issues occur, check file permissions on the data directory

## Security Considerations

- This implementation uses "*" for CORS, which is fine for development but should be restricted in production
- The AppleScript currently has no authentication mechanism for the webhook
- Consider encrypting sensitive data like phone numbers in a production environment
- Implement access controls if multiple users will access the system