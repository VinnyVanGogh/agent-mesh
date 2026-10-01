#!/usr/bin/env bash
# Rebuild staypointd from source and restart the LaunchAgent.
# Run this after any git pull or code change.
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"
BINARY="$HOME/.local/bin/staypointd"
PLIST="$HOME/Library/LaunchAgents/com.staypoint.daemon.plist"
LABEL="com.staypoint.daemon"

COMMIT="$(git -C "$REPO" rev-parse --short HEAD 2>/dev/null || echo 'none')"
echo "→ Building staypointd from $REPO (commit: $COMMIT) ..."
go build -ldflags "-X main.GitCommit=$COMMIT -X main.commit=$COMMIT" -o "$BINARY" "$REPO/cmd/staypointd"
# macOS privacy grants (TCC, e.g. "access files in your Documents folder") are
# keyed to the binary's designated requirement. An ad-hoc signature's
# requirement is its cdhash, which changes on every rebuild, so each rebuild
# re-prompts. Signing with a real certificate makes the requirement
# "identifier + certificate", which survives rebuilds. Override with
# STAYPOINT_SIGN_IDENTITY; falls back to ad-hoc when no identity exists.
SIGN_IDENTITY="${STAYPOINT_SIGN_IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | awk 'NR==1 && $2 ~ /^[0-9A-F]{40}$/ {print $2}')}"
if [ -n "$SIGN_IDENTITY" ]; then
    codesign -s "$SIGN_IDENTITY" -f --timestamp=none -i com.staypoint.daemon "$BINARY"
else
    echo "  ! No codesigning identity found; ad-hoc signing. macOS will re-prompt for permissions after every rebuild."
    codesign -s - -f -i com.staypoint.daemon "$BINARY"
fi
echo "  Built: $BINARY ($(staypointd -version 2>/dev/null || echo 'ok'))"

cat <<EOF > "$PLIST"
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
    <key>Label</key>
    <string>com.staypoint.daemon</string>
    <key>ProgramArguments</key>
    <array>
        <string>$BINARY</string>
    </array>
    <key>WorkingDirectory</key>
    <string>$HOME</string>
    <key>EnvironmentVariables</key>
    <dict>
        <key>PATH</key>
        <string>/opt/homebrew/bin:/opt/homebrew/sbin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/go/bin</string>
        <key>HOME</key>
        <string>$HOME</string>
        <key>STAYPOINT_REPO_ROOT</key>
        <string>$REPO</string>
    </dict>
    <key>RunAtLoad</key>
    <true/>
    <key>KeepAlive</key>
    <true/>
    <key>StandardOutPath</key>
    <string>/tmp/staypointd.log</string>
    <key>StandardErrorPath</key>
    <string>/tmp/staypointd.err</string>
</dict>
</plist>
EOF

if launchctl list | grep -q "$LABEL"; then
    echo "→ Stopping $LABEL ..."
    launchctl unload "$PLIST" 2>/dev/null || true
fi

echo "→ Starting $LABEL ..."
launchctl load "$PLIST" 2>/dev/null || true
launchctl kickstart -k "gui/$(id -u)/$LABEL" 2>/dev/null || true

sleep 2
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
