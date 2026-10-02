#!/usr/bin/env python3
"""Minimal in-memory Paperclip API stub for the StayPoint API e2e suite.

The daemon's /api/fleet/* routes proxy to Paperclip. The suite points them
here so no test request ever reaches the live control plane. Only the
endpoints the daemon actually calls are implemented.

Usage: paperclip_stub.py <port-file>
Binds 127.0.0.1 on an ephemeral port and writes the port to <port-file>.
"""
import json
import sys
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

COMPANY = {"id": "stub-company", "name": "Stub Company", "issuePrefix": "STB"}
ISSUE = {
    "id": "stub-issue-1",
    "identifier": "STB-1",
    "title": "Stub fleet issue",
    "description": "Served by tests/api/paperclip_stub.py",
    "status": "todo",
    "priority": "medium",
    "companyId": COMPANY["id"],
    "projectId": "",
    "issueNumber": 1,
    "labels": [],
    "createdAt": "2026-01-01T00:00:00Z",
}
ISSUES = {ISSUE["id"]: ISSUE, ISSUE["identifier"]: ISSUE}
COMMENTS = {ISSUE["id"]: []}


class Handler(BaseHTTPRequestHandler):
    def log_message(self, fmt, *args):  # keep newman output readable
        pass

    def _send(self, status, body):
        data = json.dumps(body).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    def _read_body(self):
        # The daemon proxies r.Body straight through, so it arrives chunked.
        if "chunked" in (self.headers.get("Transfer-Encoding") or "").lower():
            chunks = []
            while True:
                size = int(self.rfile.readline().strip().split(b";")[0], 16)
                if size == 0:
                    self.rfile.readline()
                    return b"".join(chunks)
                chunks.append(self.rfile.read(size))
                self.rfile.readline()
        length = int(self.headers.get("Content-Length") or 0)
        return self.rfile.read(length) if length else b""

    def _issue(self, parts):
        # parts: ["api", "issues", "<id>", ...]
        return ISSUES.get(parts[2]) if len(parts) >= 3 else None

    def do_GET(self):
        path = self.path.split("?", 1)[0]
        parts = [p for p in path.split("/") if p]
        if parts == ["api", "companies"]:
            return self._send(200, [COMPANY])
        if parts[:2] == ["api", "companies"] and len(parts) == 3:
            if parts[2] == COMPANY["id"]:
                return self._send(200, COMPANY)
            return self._send(404, {"error": "Company not found"})
        if parts[:2] == ["api", "companies"] and len(parts) == 4:
            if parts[3] == "issues":
                return self._send(200, [ISSUE] if parts[2] == COMPANY["id"] else [])
            return self._send(200, [])  # agents, projects
        if parts[:2] == ["api", "issues"]:
            issue = self._issue(parts)
            if issue is None:
                return self._send(404, {"error": "Issue not found"})
            if len(parts) == 3:
                return self._send(200, issue)
            if len(parts) == 4 and parts[3] == "comments":
                return self._send(200, COMMENTS[issue["id"]])
        self._send(404, {"error": "stub: no route for GET " + path})

    def do_POST(self):
        path = self.path.split("?", 1)[0]
        parts = [p for p in path.split("/") if p]
        raw = self._read_body()
        if parts[:2] == ["api", "issues"] and len(parts) == 4 and parts[3] == "comments":
            issue = self._issue(parts)
            if issue is None:
                return self._send(404, {"error": "Issue not found"})
            try:
                body = json.loads(raw or b"{}")
            except ValueError:
                return self._send(400, {"error": "invalid json"})
            if not isinstance(body.get("body"), str) or not body["body"].strip():
                return self._send(400, {"error": "Validation error", "details": [{"path": ["body"]}]})
            comment = {
                "id": "stub-comment-%d" % (len(COMMENTS[issue["id"]]) + 1),
                "issueId": issue["id"],
                "body": body["body"],
            }
            COMMENTS[issue["id"]].append(comment)
            return self._send(201, comment)
        self._send(404, {"error": "stub: no route for POST " + path})


def main():
    srv = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
    with open(sys.argv[1], "w") as f:
        f.write(str(srv.server_address[1]))
    srv.serve_forever()


if __name__ == "__main__":
    main()
