#!/bin/sh
if command -v systemctl >/dev/null 2>&1; then
    systemctl --user stop staypointd 2>/dev/null || true
    systemctl --user disable staypointd 2>/dev/null || true
    systemctl --user daemon-reload 2>/dev/null || true
fi
