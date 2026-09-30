#!/usr/bin/env bash
# Rebuild staypointd from source and restart the LaunchAgent.
# Run this after any git pull or code change.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$HOME/.local/bin/staypointd"
PLIST="$HOME/Library/LaunchAgents/com.staypoint.daemon.plist"
LABEL="com.staypoint.daemon"

echo "→ Building staypointd from $REPO ..."
go build -o "$BINARY" "$REPO/cmd/staypointd"
codesign -s - -f "$BINARY"
echo "  Built: $BINARY ($(staypointd -version 2>/dev/null || echo 'ok'))"

if launchctl list | grep -q "$LABEL"; then
    echo "→ Stopping $LABEL ..."
    launchctl unload "$PLIST"
fi

echo "→ Starting $LABEL ..."
launchctl load "$PLIST"

sleep 1
if launchctl list | grep -q "$LABEL"; then
    echo "✓ $LABEL is running."
else
    echo "✗ $LABEL failed to start — check /tmp/staypointd.err"
    exit 1
fi

TOKEN=$(cat "$HOME/.staypoint/auth_token" 2>/dev/null || echo "")
if [ -n "$TOKEN" ]; then
    PORT=$(grep "HTTP and SSE server active" /tmp/staypointd.err 2>/dev/null | grep -oE '127\.0\.0\.1:[0-9]+' | tail -1 | cut -d: -f2 || echo "41421")
    echo ""
    echo "  Web UI: http://127.0.0.1:${PORT}/?token=${TOKEN}"
fi
