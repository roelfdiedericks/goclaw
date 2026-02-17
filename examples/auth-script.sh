#!/bin/bash
# auth-script.sh - Example authentication script for GoClaw user_auth tool
#
# This script demonstrates the interface for user authentication.
# It looks up users from a JSON file based on credentials provided via stdin.
#
# SETUP:
# 1. Copy this script to your desired location (e.g., ~/.goclaw/scripts/auth.sh)
# 2. Make it executable: chmod +x ~/.goclaw/scripts/auth.sh
# 3. Create a users file (see below)
# 4. Configure goclaw.json:
#    {
#      "auth": {
#        "enabled": true,
#        "script": "/home/user/.goclaw/scripts/auth.sh",
#        "credentialHints": [
#          {"key": "customer_id", "label": "Customer ID"},
#          {"key": "phone", "label": "phone number"},
#          {"key": "email", "label": "email address"}
#        ],
#        "allowedRoles": ["customer", "user"]
#      }
#    }
#
# USERS FILE:
# Create a JSON file at the path specified by GOCLAW_AUTH_USERS (default: ~/.goclaw/auth-users.json)
# Format:
# {
#   "CUS-12345": {
#     "name": "Alice Smith",
#     "username": "alice",
#     "role": "customer",
#     "id": "CUS-12345",
#     "context": "VIP customer since 2020. Has 3 pending orders."
#   },
#   "+1234567890": {
#     "name": "Bob Jones",
#     "username": "bob",
#     "role": "user",
#     "id": "bob@example.com",
#     "context": "Standard user. Prefers email communication."
#   }
# }
#
# TESTING:
# echo '{"id": "CUS-12345"}' | ./auth-script.sh
#
# INPUT (stdin):
# JSON object with credentials, e.g.: {"id": "CUS-12345", "phone": "+1234567890"}
#
# OUTPUT (stdout):
# Success: {"success": true, "user": {...}, "message": "..."}
# Failure: {"success": false, "message": "..."}

set -e

# Configuration
USERS_FILE="${GOCLAW_AUTH_USERS:-$HOME/.goclaw/auth-users.json}"

# Read credentials from stdin
CREDS=$(cat)

# Log for debugging (to stderr so it doesn't interfere with JSON output)
>&2 echo "auth-script: received credentials"

# Extract credential values - these should match your credentialHints config
# Example: "credentialHints": ["customer_id", "phone", "email"]
CUSTOMER_ID=$(echo "$CREDS" | jq -r '.customer_id // empty' 2>/dev/null)
PHONE=$(echo "$CREDS" | jq -r '.phone // empty' 2>/dev/null)
EMAIL=$(echo "$CREDS" | jq -r '.email // empty' 2>/dev/null)

# Use first non-empty credential as lookup key
LOOKUP_KEY="${CUSTOMER_ID:-${PHONE:-$EMAIL}}"

# No valid credential provided
if [ -z "$LOOKUP_KEY" ]; then
    echo '{"success": false, "message": "No valid credential provided. Ask for their customer_id, phone, or email."}'
    exit 0
fi

>&2 echo "auth-script: looking up key: $LOOKUP_KEY"

# Check if users file exists
if [ ! -f "$USERS_FILE" ]; then
    >&2 echo "auth-script: users file not found: $USERS_FILE"
    echo '{"success": false, "message": "Authentication system is not configured. Please contact support."}'
    exit 0
fi

# Look up user by key
USER=$(jq -r --arg key "$LOOKUP_KEY" '.[$key] // empty' "$USERS_FILE" 2>/dev/null)

# User not found
if [ -z "$USER" ] || [ "$USER" = "null" ]; then
    >&2 echo "auth-script: user not found for key: $LOOKUP_KEY"
    echo '{"success": false, "message": "No user found with that identifier. Ask them to double-check or try a different credential (phone, email, or customer ID)."}'
    exit 0
fi

# User found - format success response
>&2 echo "auth-script: user found"

echo "$USER" | jq '{
    success: true,
    user: {
        name: .name,
        username: .username,
        role: .role,
        id: .id
    },
    message: (.context // "User authenticated successfully.")
}'
