#!/bin/sh
# Reload systemd user daemon units after install (best-effort; skip in containers).
if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload 2>/dev/null || true
    echo "staypointd installed. Enable with: systemctl --user enable --now staypointd"
fi
