#!/bin/sh
set -e

CONFIG_DIR="${HOME}/.goclaw"
CONFIG_FILE="${CONFIG_DIR}/goclaw.json"
USERS_FILE="${CONFIG_DIR}/users.json"

# Ensure config directory exists
mkdir -p "$CONFIG_DIR"

# Check if config exists
if [ ! -f "$CONFIG_FILE" ]; then
    echo ""
    echo "=== GoClaw First Run ==="
    echo ""
    echo "No configuration found."
    echo ""
    echo "Option 1: Interactive Setup Wizard (recommended)"
    echo "  Run this in another terminal:"
    echo "    docker exec -it <container_name> goclaw setup"
    echo ""
    echo "Option 2: Quick Start with Defaults"
    echo "  Set GOCLAW_QUICK_START=1 to generate default configs"
    echo "  You'll need to edit goclaw.json with your API key afterwards"
    echo ""
    
    if [ "$GOCLAW_QUICK_START" = "1" ]; then
        echo "Quick start mode enabled, generating defaults..."
        echo ""
        
        # Generate goclaw.json
        goclaw setup generate > "$CONFIG_FILE"
        echo "Created: $CONFIG_FILE"
        
        # Generate users.json with password
        goclaw setup generate --users --with-password > "$USERS_FILE"
        echo "Created: $USERS_FILE"
        
        echo ""
        echo "=== Next Steps ==="
        echo "1. Edit $CONFIG_FILE and add your LLM API key"
        echo "2. Note the generated password above for web UI login (username: owner)"
        echo "3. Restart the container"
        echo ""
    fi
    exit 1
fi

# Check if users.json exists
if [ ! -f "$USERS_FILE" ]; then
    echo ""
    echo "=== Generating users.json ==="
    goclaw setup generate --users --with-password > "$USERS_FILE"
    echo "Created: $USERS_FILE"
    echo ""
    echo "Note the generated password above for web UI login (username: owner)"
    echo "Restart the container to apply."
    echo ""
    exit 1
fi

# Start gateway
exec goclaw gateway "$@"
