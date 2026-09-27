#!/usr/bin/env python3
"""Paperclip External Review Bridge.

Watches ~/.claude/reviews/pending-gemini/ and ~/.claude/reviews/pending/
and forwards new external peer reviews into Paperclip as issues
assigned to Chief of Staff for intelligent domain triage.
"""

import glob
import json
import os
import sys
import urllib.request

PAPERCLIP_URL = os.environ.get("PAPERCLIP_URL", "http://127.0.0.1:3100")
COMPANY_ID = os.environ.get("PAPERCLIP_COMPANY_ID", "a9cb311a-f22d-45fe-80ab-51e23a5369b4")
CHIEF_OF_STAFF_ID = "61ba286b-8417-4a18-9289-82c03b4baa79"
PROJECT_ID = "d3154ad6-7675-46c3-bd82-f6942b17f865"
STATE_FILE = os.path.expanduser("~/.claude/reviews/.paperclip-ingested.json")


def load_ingested():
    if os.path.exists(STATE_FILE):
        try:
            with open(STATE_FILE, "r") as f:
                return set(json.load(f))
        except Exception:
            return set()
    return set()


def save_ingested(ingested):
    os.makedirs(os.path.dirname(STATE_FILE), exist_ok=True)
    with open(STATE_FILE, "w") as f:
        json.dump(list(ingested), f, indent=2)


def post_issue(title, description, priority="medium"):
    url = f"{PAPERCLIP_URL}/api/companies/{COMPANY_ID}/issues"
    payload = {
        "title": title,
        "description": description,
        "assigneeAgentId": CHIEF_OF_STAFF_ID,
        "priority": priority,
        "projectId": PROJECT_ID,
    }
    req = urllib.request.Request(
        url,
        data=json.dumps(payload).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as resp:
        return json.loads(resp.read().decode("utf-8"))


def wake_chief(issue_id):
    url = f"{PAPERCLIP_URL}/api/agents/{CHIEF_OF_STAFF_ID}/wakeup"
    req = urllib.request.Request(
        url,
        data=json.dumps({"issueId": issue_id}).encode("utf-8"),
        headers={"Content-Type": "application/json"},
    )
    try:
        with urllib.request.urlopen(req, timeout=10) as resp:
            return json.loads(resp.read().decode("utf-8"))
    except Exception as e:
        print(f"Wakeup error: {e}", file=sys.stderr)
        return None


def sync_reviews():
    ingested = load_ingested()
    pattern1 = os.path.expanduser("~/.claude/reviews/pending-gemini/*.json")
    pattern2 = os.path.expanduser("~/.claude/reviews/pending/*.json")
    review_files = sorted(glob.glob(pattern1) + glob.glob(pattern2))

    new_count = 0
    for rpath in review_files:
        fname = os.path.basename(rpath)
        sha = os.path.splitext(fname)[0]
        if sha in ingested:
            continue

        try:
            with open(rpath, "r") as f:
                data = json.load(f)
        except Exception:
            continue

        verdict = str(data.get("verdict", "")).upper()
        repo = data.get("repo", "unknown")
        reviewer = data.get("reviewer", "External Reviewer")
        review_text = data.get("review", "")

        # We triage WARN and FAIL reviews (PASS is logged without ticket spam)
        if verdict not in ("WARN", "FAIL"):
            ingested.add(sha)
            continue

        title = f"[External Review] {repo} ({sha[:7]}): {verdict} from {reviewer}"
        desc = f"""### 🔬 Incoming External AI Peer Review
- **Repository**: `{repo}`
- **Commit SHA**: `{sha}`
- **Reviewer**: `{reviewer}`
- **Verdict**: `{verdict}`

#### Critique Content:
```
{review_text}
```

---
### 📋 Chief of Staff Directive:
1. **Triage**: Map finding to the responsible domain agent (Remediation Dev for code/secrets, Docs Writer for documentation, DevOps for CI, Visual QA for UI).
2. **Assign Dual-Action Task**: Author must **either defend the code** (technical justification comment) or **resolve the issue** (push fix to worktree).
3. **Approval Gate**: Once addressed, assign to **Senior PR Reviewer / Staff Auditor** (Claude Opus) for final verification.
"""
        priority = "high" if verdict == "FAIL" else "medium"
        try:
            issue = post_issue(title, desc, priority)
            print(f"Ingested {sha} -> {issue.get('identifier', 'Issue')}")
            wake_chief(issue.get("id"))
            ingested.add(sha)
            new_count += 1
        except Exception as e:
            print(f"Failed to post {sha}: {e}", file=sys.stderr)

    save_ingested(ingested)
    print(f"Sync complete. {new_count} new external reviews ingested to Chief of Staff.")


if __name__ == "__main__":
    sync_reviews()
