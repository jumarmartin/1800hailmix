#!/bin/bash

# Script to set up the Mail.app to backend integration

echo "Setting up Mail.app to backend integration..."
echo "This script requires admin privileges to install the launchd job."

# Set variables
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
PLIST_FILE="com.urinal.mail-processor.plist"
PLIST_PATH="$SCRIPT_DIR/$PLIST_FILE"
SCRIPT_FILE="mail_to_backend.scpt"
SCRIPT_PATH="$SCRIPT_DIR/$SCRIPT_FILE"
LAUNCHD_DIR="$HOME/Library/LaunchAgents"

# Check if files exist
if [ ! -f "$PLIST_PATH" ]; then
    echo "Error: $PLIST_FILE not found in $SCRIPT_DIR"
    exit 1
fi

if [ ! -f "$SCRIPT_PATH" ]; then
    echo "Error: $SCRIPT_FILE not found in $SCRIPT_DIR"
    exit 1
fi

# Make sure the LaunchAgents directory exists
mkdir -p "$LAUNCHD_DIR"

# Copy the plist file to LaunchAgents directory
cp "$PLIST_PATH" "$LAUNCHD_DIR/"

# Load the launchd job
echo "Loading the launchd job..."
launchctl load "$LAUNCHD_DIR/$PLIST_FILE"

# Check if the job was loaded successfully
if launchctl list | grep com.urinal.mail-processor > /dev/null; then
    echo "Mail.app integration has been set up successfully!"
    echo "The script will run every 5 minutes to check for new emails."
    echo "You can view logs at:"
    echo "  - $SCRIPT_DIR/mail_output.log"
    echo "  - $SCRIPT_DIR/mail_error.log"
else
    echo "Error: Failed to load the launchd job."
    echo "Please try running: launchctl load $LAUNCHD_DIR/$PLIST_FILE"
fi

# Provide instructions for testing
echo ""
echo "To test the integration:"
echo "1. Make sure your backend server is running: go run main.go"
echo "2. Send yourself an email with [IMPORTANT] in the subject line"
echo "3. Open Mail.app and check that the email arrives"
echo "4. The script will process the email and send it to your backend"
echo "5. Verify at http://localhost:8080/api/emails that the email data was received"
echo ""
echo "To uninstall, run: launchctl unload $LAUNCHD_DIR/$PLIST_FILE"

# Make script executable
chmod +x "$SCRIPT_PATH"