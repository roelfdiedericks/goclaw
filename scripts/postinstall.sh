#!/bin/sh
# GoClaw .deb postinstall script
# Creates symlink to bundled whisper model in /etc/skel for new users

set -e

# Create skel directory for new users
mkdir -p /etc/skel/.goclaw/stt/whisper

# Symlink bundled model
if [ -f /usr/share/goclaw/stt/ggml-tiny.en.bin ]; then
    ln -sf /usr/share/goclaw/stt/ggml-tiny.en.bin /etc/skel/.goclaw/stt/whisper/
fi

# For existing users, print a note
echo ""
echo "GoClaw installed successfully!"
echo ""
echo "To use the bundled whisper model, run:"
echo "  mkdir -p ~/.goclaw/stt/whisper"
echo "  ln -sf /usr/share/goclaw/stt/ggml-tiny.en.bin ~/.goclaw/stt/whisper/"
echo ""
echo "Or run 'goclaw setup' to configure."
echo ""
