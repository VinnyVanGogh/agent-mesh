#!/bin/sh
if command -v systemctl >/dev/null 2>&1; then
    systemctl --user daemon-reload 2>/dev/null || true
    echo "staypointd installed. Enable with: systemctl --user enable --now staypointd"
fi
