-- Apple Mail to Backend Integration Script
-- This script processes emails from Mail.app and forwards them to our backend
-- with a special focus on MP3 attachments

on run
	processNewEmails()
end run

on processNewEmails()
	tell application "Mail"
		-- Get unread messages from the inbox
		set theAccount to account "Personal"
		set targetMailbox to mailbox "mp3" of theAccount
		
		-- Get only unread messages from the mailbox
		set inboxMessages to (get messages of targetMailbox whose read status is false)
		
		-- Limit to the first 10 unread emails
		set maxEmails to 10
		set processedCount to 0
		
		repeat with currentMessage in inboxMessages
			-- Only process up to maxEmails
			if processedCount � maxEmails then
				exit repeat
			end if
			
			-- Look for any email with MP3 attachments
			set hasMP3 to false
			set mp3Attachments to {}
			
			-- Check if message has attachments
			if (count of mail attachments of currentMessage) > 0 then
				repeat with theAttachment in mail attachments of currentMessage
					-- Check if it's an MP3 file
					set fileName to name of theAttachment
					if fileName ends with ".mp3" then
						set hasMP3 to true
						set end of mp3Attachments to theAttachment
					end if
				end repeat
			end if
			
			-- Process if it has MP3 attachments
			if hasMP3 then
				log "Processing message with MP3 attachment: " & subject of currentMessage
				
				-- Extract email data
				set emailSubject to subject of currentMessage
				set emailSender to sender of currentMessage
				set emailDate to date received of currentMessage
				
				-- Start building the JSON data
				set jsonData to "{"
				set jsonData to jsonData & "\"subject\": \"" & my escapeJSON(emailSubject) & "\", "
				set jsonData to jsonData & "\"sender\": \"" & my escapeJSON(emailSender) & "\", "
				set jsonData to jsonData & "\"receivedAt\": \"" & (emailDate as string) & "\", "
				set jsonData to jsonData & "\"attachments\": ["
				
				-- Add attachments to the JSON
				set firstAttachment to true
				repeat with theAttachment in mp3Attachments
					-- Get attachment details
					set fileName to name of theAttachment
					
					-- Save attachment to Desktop temporarily (with a unique name to avoid conflicts)
					set uniqueID to do shell script "date +%s"
					set tmpPath to (path to desktop as string) & "temp_mp3_" & uniqueID & "_" & fileName
					save theAttachment in file tmpPath
					
					-- Convert attachment to base64 (using the file on desktop which has proper permissions)
					set base64Content to do shell script "base64 -i " & quoted form of POSIX path of tmpPath
					
					-- Add to JSON
					if not firstAttachment then
						set jsonData to jsonData & ", "
					end if
					set jsonData to jsonData & "{"
					set jsonData to jsonData & "\"fileName\": \"" & my escapeJSON(fileName) & "\", "
					set jsonData to jsonData & "\"content\": \"" & base64Content & "\", "
					set jsonData to jsonData & "\"mimeType\": \"audio/mpeg\""
					set jsonData to jsonData & "}"
					
					set firstAttachment to false
					
					-- Clean up temporary file
					do shell script "rm " & quoted form of POSIX path of tmpPath
				end repeat
				
				-- Close the attachments array
				set jsonData to jsonData & "]"
				
				-- Close the JSON object
				set jsonData to jsonData & "}"
				
				-- Send to backend
				my sendToBackend(jsonData)
				
				-- Mark as read
				set read status of currentMessage to true
				
				-- Increment processed count
				set processedCount to processedCount + 1
			end if
		end repeat
		
		log "Processed " & processedCount & " emails with MP3 attachments (limited to first " & maxEmails & " unread emails)"
	end tell
end processNewEmails

on escapeJSON(theString)
	-- Basic JSON string escaping
	set theString to my replaceText(theString, "\"", "\\\"")
	set theString to my replaceText(theString, return, "\\n")
	set theString to my replaceText(theString, tab, "\\t")
	return theString
end escapeJSON

on replaceText(theText, oldString, newString)
	set AppleScript's text item delimiters to oldString
	set theTextItems to text items of theText
	set AppleScript's text item delimiters to newString
	set theText to theTextItems as text
	set AppleScript's text item delimiters to ""
	return theText
end replaceText

on sendToBackend(jsonData)
	-- Save JSON to Desktop temporarily to avoid permission issues
	set uniqueID to do shell script "date +%s"
	set tmpJSONPath to POSIX path of (path to desktop as string) & "temp_json_" & uniqueID & "_email_data.json"
	do shell script "echo '" & jsonData & "' > " & quoted form of tmpJSONPath
	
	-- Use curl to send the JSON data from file
	do shell script "curl -X POST -H 'Content-Type: application/json' --data-binary @" & quoted form of tmpJSONPath & " http://localhost:8080/api/webhook"
	
	-- Clean up temporary file
	do shell script "rm " & quoted form of tmpJSONPath
end sendToBackend