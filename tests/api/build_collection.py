#!/usr/bin/env python3
"""Generates staypoint.postman_collection.json (Postman v2.1).

The collection is the artifact people open in Postman; this file is the
source. Edit here, run `python3 tests/api/build_collection.py`, commit both.
scripts/api-e2e.sh fails if the two drift.

Every request asserts the contract the API *should* honour (STA-338 edge-case
list), not whatever the daemon happens to return today. Tests that currently
fail because of a known daemon bug carry the bug's issue id in their name.
"""
import json
import os

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, "staypoint.postman_collection.json")

# Daemon bugs this suite exposed (filed from STA-346). A test named
# "[STA-nnn] ..." fails until that issue is fixed.
AUTHZ = "STA-355"     # governance bypass, cross-task interaction resolve
NOT_FOUND = "STA-356"  # unknown ids answered with 500/200/400
CONTRACT = "STA-357"  # validation gaps, null instead of []
AUDIT = "STA-358"     # audit omits comments and interactions


def bug(issue, name):
    return "[%s] %s" % (issue, name)


# --- assertion snippets ----------------------------------------------------

def status(code, name=None):
    return 'pm.test(%s, () => pm.response.to.have.status(%d));' % (
        json.dumps(name or "status is %d" % code), code)


def status_in(codes, name=None):
    return 'pm.test(%s, () => pm.expect(pm.response.code).to.be.oneOf(%s));' % (
        json.dumps(name or "status in %s" % codes), json.dumps(codes))


def client_error(name=None):
    return 'pm.test(%s, () => pm.expect(pm.response.code).to.be.within(400, 499));' % (
        json.dumps(name or "rejected with 4xx (not 5xx, not 2xx)"))


def not_found(name=None):
    return status(404, name or "unknown id -> 404")


def has_error_field():
    return ('pm.test("body has error message", () => '
            'pm.expect(pm.response.json().error).to.be.a("string").and.not.empty);')


def test(name, body):
    return "pm.test(%s, () => { %s });" % (json.dumps(name), body)


def save(var, expr):
    return 'pm.collectionVariables.set(%s, %s);' % (json.dumps(var), expr)


J = "const j = pm.response.json();"


# --- request builders ------------------------------------------------------

def req(name, method, path, body=None, tests=(), raw=None, headers=None,
        auth=True, description=None):
    url_raw = "{{baseUrl}}" + path
    path_part, _, query = path.partition("?")
    url = {
        "raw": url_raw,
        "host": ["{{baseUrl}}"],
        "path": [p for p in path_part.split("/") if p],
    }
    if query:
        url["query"] = [
            {"key": kv.partition("=")[0], "value": kv.partition("=")[2]}
            for kv in query.split("&")
        ]
    hdrs = [{"key": k, "value": v} for k, v in (headers or {}).items()]
    request = {"method": method, "header": hdrs, "url": url}
    if not auth:
        request["auth"] = {"type": "noauth"}
    if body is not None or raw is not None:
        request["header"].append({"key": "Content-Type", "value": "application/json"})
        request["body"] = {
            "mode": "raw",
            "raw": raw if raw is not None else json.dumps(body, indent=2),
            "options": {"raw": {"language": "json"}},
        }
    if description:
        request["description"] = description
    item = {"name": name, "request": request}
    if tests:
        item["event"] = [{
            "listen": "test",
            "script": {"type": "text/javascript", "exec": list(tests)},
        }]
    return item


def folder(name, items, description=None):
    f = {"name": name, "item": items}
    if description:
        f["description"] = description
    return f


def ask_payload(options=("yes", "no")):
    return json.dumps({"questions": [
        {"id": "q1", "question": "Ship it?", "options": list(options)}]})


# --- folders ---------------------------------------------------------------

ALL_ROUTES = [
    ("GET", "/api/health"), ("GET", "/api/events"), ("GET", "/api/sse"),
    ("GET", "/api/tasks"), ("POST", "/api/tasks"), ("GET", "/api/tasks/x"),
    ("GET", "/api/tasks/x/comments"), ("POST", "/api/tasks/x/comments"),
    ("POST", "/api/tasks/x/done"), ("POST", "/api/tasks/x/block"),
    ("POST", "/api/tasks/x/unblock"), ("GET", "/api/tasks/x/dependencies"),
    ("POST", "/api/tasks/x/blockers"), ("DELETE", "/api/tasks/x/blockers/y"),
    ("POST", "/api/tasks/x/stage"), ("GET", "/api/tasks/x/run-steps"),
    ("GET", "/api/tasks/x/interactions"), ("POST", "/api/tasks/x/interactions"),
    ("POST", "/api/tasks/x/interactions/1/resolve"),
    ("GET", "/api/threads"), ("POST", "/api/threads"), ("GET", "/api/threads/x"),
    ("GET", "/api/threads/x/messages"), ("POST", "/api/threads/x/messages"),
    ("GET", "/api/sessions"), ("POST", "/api/sessions"), ("GET", "/api/sessions/x"),
    ("POST", "/api/sessions/x/heartbeat"), ("POST", "/api/sessions/x/close"),
    ("GET", "/api/agents"),
    ("GET", "/api/tasks/x/governance"), ("POST", "/api/tasks/x/governance"),
    ("POST", "/api/tasks/x/reviewers"), ("DELETE", "/api/tasks/x/reviewers/y"),
    ("POST", "/api/tasks/x/approvers"), ("DELETE", "/api/tasks/x/approvers/y"),
    ("POST", "/api/tasks/x/watchdog"), ("POST", "/api/tasks/x/review"),
    ("POST", "/api/tasks/x/approve"), ("POST", "/api/tasks/x/transition"),
    ("GET", "/api/tasks/x/audit"),
    ("GET", "/api/telemetry"), ("GET", "/api/fleet/overview"),
    ("GET", "/api/fleet/tasks/x"), ("GET", "/api/fleet/tasks/x/comments"),
    ("POST", "/api/fleet/tasks/x/comments"), ("GET", "/api/report"),
    ("GET", "/api/checklist"), ("GET", "/api/checklist/sprints"),
    ("PATCH", "/api/checklist/x"), ("GET", "/api/checklist/x/history"),
    ("POST", "/api/checklist/seed"), ("POST", "/api/checklist/evaluate"),
    ("GET", "/"), ("GET", "/ui/"),
]


def f_auth():
    items = [
        req("health with bearer token", "GET", "/api/health", tests=[
            status(200), J,
            test("status ok", 'pm.expect(j.status).to.eql("ok");'),
            test("reports git commit", 'pm.expect(j.git_commit).to.be.a("string");'),
        ]),
        req("X-StayPoint-Token header is accepted", "GET", "/api/health", auth=False,
            headers={"X-StayPoint-Token": "{{token}}"}, tests=[status(200)]),
        req("?token= query param is accepted", "GET", "/api/health?token={{token}}",
            auth=False, tests=[status(200)]),
        req("wrong token -> 401", "GET", "/api/health", auth=False,
            headers={"Authorization": "Bearer not-the-token"},
            tests=[status(401), has_error_field()]),
        req("cross-origin request -> 403", "GET", "/api/health",
            headers={"Origin": "https://evil.example"}, tests=[status(403)]),
        req("loopback origin is allowed and reflected", "GET", "/api/health",
            headers={"Origin": "{{baseUrl}}"}, tests=[
                status(200),
                test("ACAO reflects origin, never *",
                     'pm.expect(pm.response.headers.get("Access-Control-Allow-Origin"))'
                     '.to.eql(pm.variables.replaceIn("{{baseUrl}}"));'),
            ]),
        req("CORS preflight from loopback origin -> 204", "OPTIONS", "/api/tasks",
            auth=False, headers={"Origin": "{{baseUrl}}",
                                 "Access-Control-Request-Method": "POST"},
            tests=[status(204)]),
    ]
    unauth = [
        req("%s %s without token -> 401" % (m, p), m, p, auth=False,
            tests=[status(401)])
        for m, p in ALL_ROUTES
    ]
    return folder("00 Auth & health", items + [
        folder("Every route rejects unauthenticated calls", unauth)])


def f_tasks():
    return folder("01 Tasks", [
        req("create task A", "POST", "/api/tasks",
            {"name": "e2e task A", "repo_path": "/tmp/staypoint-e2e", "git_branch": "main"},
            tests=[status(201), J,
                   test("returns id and defaults",
                        'pm.expect(j.id).to.match(/^task-/); pm.expect(j.name).to.eql("e2e task A");'
                        'pm.expect(j.execution_stage).to.eql("todo"); pm.expect(j.is_blocked).to.eql(false);'),
                   save("taskA", "j.id")]),
        req("create task B", "POST", "/api/tasks",
            {"name": "e2e task B", "repo_path": "/tmp/staypoint-e2e"},
            tests=[status(201), J, save("taskB", "j.id")]),
        req("duplicate create (same name) makes a distinct task", "POST", "/api/tasks",
            {"name": "e2e task A", "repo_path": "/tmp/staypoint-e2e"},
            tests=[status(201), J,
                   test("new id, not the original",
                        'pm.expect(j.id).to.not.eql(pm.collectionVariables.get("taskA"));'),
                   save("taskDup", "j.id")]),
        req("empty name -> 400", "POST", "/api/tasks", {"name": "   "},
            tests=[status(400), has_error_field()]),
        req("missing fields -> 400", "POST", "/api/tasks", {},
            tests=[status(400), has_error_field()]),
        req("malformed JSON -> 400", "POST", "/api/tasks", raw="{not json",
            tests=[status(400), has_error_field()]),
        req("list tasks: newest first", "GET", "/api/tasks", tests=[
            status(200), J,
            test("pagination envelope",
                 'pm.expect(j).to.include.keys("tasks","total","limit","offset","has_more");'),
            test("B listed before A (created_at desc)",
                 'const ids = j.tasks.map(t => t.id);'
                 'const a = ids.indexOf(pm.collectionVariables.get("taskA"));'
                 'const b = ids.indexOf(pm.collectionVariables.get("taskB"));'
                 'pm.expect(a).to.be.above(-1); pm.expect(b).to.be.above(-1); pm.expect(b).to.be.below(a);'),
        ]),
        req("list tasks: limit/offset paginate", "GET", "/api/tasks?limit=1&offset=1", tests=[
            status(200), J,
            test("one row, has_more", 'pm.expect(j.tasks).to.have.length(1); pm.expect(j.limit).to.eql(1);'
                 'pm.expect(j.offset).to.eql(1); pm.expect(j.has_more).to.eql(true);'),
        ]),
        req("list tasks: bad limit/offset fall back to defaults", "GET",
            "/api/tasks?limit=-5&offset=abc", tests=[
                status(200), J,
                test("defaults", 'pm.expect(j.limit).to.eql(100); pm.expect(j.offset).to.eql(0);')]),
        req("list tasks: limit capped at 1000", "GET", "/api/tasks?limit=99999",
            tests=[status(200), J, test("capped", 'pm.expect(j.limit).to.eql(1000);')]),
        req(bug(CONTRACT, "list tasks: empty result is [] not null"), "GET",
            "/api/tasks?status=no-such-status", tests=[
                status(200), J,
                test("tasks is an empty array",
                     'pm.expect(j.tasks).to.be.an("array").that.is.empty; pm.expect(j.total).to.eql(0);')]),
        req("get task A", "GET", "/api/tasks/{{taskA}}", tests=[
            status(200), J,
            test("task, comments, dependencies",
                 'pm.expect(j.task.id).to.eql(pm.collectionVariables.get("taskA"));'
                 'pm.expect(j).to.include.keys("comments","dependencies");'),
        ]),
        req("get unknown task -> 404", "GET", "/api/tasks/task-doesnotexist",
            tests=[not_found(), has_error_field()]),
    ])


def f_comments():
    return folder("02 Comments", [
        req("add comment (message)", "POST", "/api/tasks/{{taskA}}/comments",
            {"author": "qa-bot", "message": "first comment"}, tests=[status(201)]),
        req("add comment (body alias, default author)", "POST",
            "/api/tasks/{{taskA}}/comments", {"body": "second comment"}, tests=[status(201)]),
        req("whitespace-only message -> 400", "POST", "/api/tasks/{{taskA}}/comments",
            {"message": "   "}, tests=[status(400), has_error_field()]),
        req("missing message -> 400", "POST", "/api/tasks/{{taskA}}/comments", {},
            tests=[status(400)]),
        req("malformed JSON -> 400", "POST", "/api/tasks/{{taskA}}/comments", raw="{",
            tests=[status(400)]),
        req("list comments: oldest first, author defaulted", "GET",
            "/api/tasks/{{taskA}}/comments", tests=[
                status(200), J,
                test("two comments in insertion order",
                     'pm.expect(j.comments).to.have.length(2);'
                     'pm.expect(j.comments[0].message).to.eql("first comment");'
                     'pm.expect(j.comments[0].author).to.eql("qa-bot");'
                     'pm.expect(j.comments[1].author).to.eql("user");'
                     'pm.expect(j.comments[0].id).to.be.below(j.comments[1].id);'),
            ]),
        req(bug(NOT_FOUND, "add comment to unknown task -> 404"), "POST",
            "/api/tasks/task-doesnotexist/comments", {"message": "x"}, tests=[not_found()]),
        req(bug(NOT_FOUND, "list comments of unknown task -> 404"), "GET",
            "/api/tasks/task-doesnotexist/comments", tests=[not_found()]),
    ])


def f_blockers():
    return folder("03 Blockers & block/unblock", [
        req("A blocked by B", "POST", "/api/tasks/{{taskA}}/blockers",
            {"blocker_id": "{{taskB}}", "rationale": "needs B first"}, tests=[status(200)]),
        req("dependencies show the edge", "GET", "/api/tasks/{{taskA}}/dependencies", tests=[
            status(200), J,
            test("A is blocked by B",
                 'pm.expect(j.task.is_blocked).to.eql(true);'
                 'pm.expect(j.blocked_by.map(b => b.id)).to.include(pm.collectionVariables.get("taskB"));'),
        ]),
        req("duplicate blocker add is idempotent", "POST", "/api/tasks/{{taskA}}/blockers",
            {"blocker_id": "{{taskB}}", "rationale": "needs B first"},
            tests=[status_in([200, 201, 409])]),
        req("still exactly one B edge", "GET", "/api/tasks/{{taskA}}/dependencies", tests=[
            status(200), J,
            test("no duplicate edge",
                 'pm.expect(j.blocked_by.filter(b => b.id === pm.collectionVariables.get("taskB"))).to.have.length(1);'),
        ]),
        req("B reverse view: blocks A", "GET", "/api/tasks/{{taskB}}/dependencies", tests=[
            status(200), J,
            test("B blocks A", 'pm.expect((j.blocks || []).map(b => b.id)).to.include(pm.collectionVariables.get("taskA"));'),
        ]),
        req(bug(CONTRACT, "cycle (B blocked by A) -> 4xx"), "POST", "/api/tasks/{{taskB}}/blockers",
            {"blocker_id": "{{taskA}}"}, tests=[client_error()]),
        req(bug(CONTRACT, "self-block -> 400"), "POST", "/api/tasks/{{taskA}}/blockers",
            {"blocker_id": "{{taskA}}"}, tests=[status(400)]),
        req("missing blocker_id -> 400", "POST", "/api/tasks/{{taskA}}/blockers", {},
            tests=[status(400), has_error_field()]),
        req(bug(NOT_FOUND, "unknown blocker id -> 404"), "POST", "/api/tasks/{{taskA}}/blockers",
            {"blocker_id": "task-doesnotexist"}, tests=[not_found()]),
        req(bug(NOT_FOUND, "blockers on unknown task -> 404"), "POST",
            "/api/tasks/task-doesnotexist/blockers", {"blocker_id": "{{taskB}}"},
            tests=[not_found()]),
        req("remove blocker B from A", "DELETE", "/api/tasks/{{taskA}}/blockers/{{taskB}}",
            tests=[status(200)]),
        req("cleanup: drop any reverse/self edges", "DELETE",
            "/api/tasks/{{taskB}}/blockers/{{taskA}}", tests=[status_in([200, 404])]),
        req("cleanup: drop self edge", "DELETE", "/api/tasks/{{taskA}}/blockers/{{taskA}}",
            tests=[status_in([200, 404])]),
        req("A unblocked after removal", "GET", "/api/tasks/{{taskA}}/dependencies", tests=[
            status(200), J, test("not blocked", 'pm.expect(j.task.is_blocked).to.eql(false);')]),
        req(bug(NOT_FOUND, "remove blocker on unknown task -> 404"), "DELETE",
            "/api/tasks/task-doesnotexist/blockers/{{taskB}}", tests=[not_found()]),
        req("block B with reason", "POST", "/api/tasks/{{taskB}}/block",
            {"reason": "waiting on vendor"}, tests=[status(200)]),
        req("B shows blocked + reason", "GET", "/api/tasks/{{taskB}}", tests=[
            status(200), J,
            test("blocked", 'pm.expect(j.task.is_blocked).to.eql(true); pm.expect(j.task.block_reason).to.eql("waiting on vendor");')]),
        req("unblock B", "POST", "/api/tasks/{{taskB}}/unblock", tests=[status(200)]),
        req("B no longer blocked", "GET", "/api/tasks/{{taskB}}", tests=[
            status(200), J, test("unblocked", 'pm.expect(j.task.is_blocked).to.eql(false);')]),
        req(bug(NOT_FOUND, "block unknown task -> 404"), "POST", "/api/tasks/task-doesnotexist/block",
            {"reason": "x"}, tests=[not_found()]),
        req(bug(NOT_FOUND, "unblock unknown task -> 404"), "POST",
            "/api/tasks/task-doesnotexist/unblock", tests=[not_found()]),
        req("dependencies of unknown task -> 404", "GET",
            "/api/tasks/task-doesnotexist/dependencies", tests=[not_found()]),
    ])


def f_stage_runsteps_done():
    return folder("04 Stage, run-steps, done", [
        req("set stage in_progress", "POST", "/api/tasks/{{taskB}}/stage",
            {"stage": "in_progress"}, tests=[
                status(200), J, test("echoes stage", 'pm.expect(j.stage).to.eql("in_progress");')]),
        req("stage persisted", "GET", "/api/tasks/{{taskB}}", tests=[
            status(200), J, test("in_progress", 'pm.expect(j.task.execution_stage).to.eql("in_progress");')]),
        req("invalid stage -> 400", "POST", "/api/tasks/{{taskB}}/stage", {"stage": "bogus"},
            tests=[status(400), has_error_field()]),
        req("missing stage -> 400", "POST", "/api/tasks/{{taskB}}/stage", {},
            tests=[status(400)]),
        req("malformed JSON -> 400", "POST", "/api/tasks/{{taskB}}/stage", raw="[",
            tests=[status(400)]),
        req(bug(NOT_FOUND, "stage on unknown task -> 404"), "POST", "/api/tasks/task-doesnotexist/stage",
            {"stage": "todo"}, tests=[not_found()]),
        req("run-steps: empty list for new task", "GET", "/api/tasks/{{taskB}}/run-steps",
            tests=[status(200), J, test("steps is []", 'pm.expect(j.steps).to.be.an("array").that.is.empty;')]),
        req(bug(NOT_FOUND, "run-steps of unknown task -> 404"), "GET",
            "/api/tasks/task-doesnotexist/run-steps", tests=[not_found()]),
        req(bug(NOT_FOUND, "done without a work product -> 409/422, not 500"), "POST",
            "/api/tasks/{{taskB}}/done", tests=[status_in([409, 422]), has_error_field()]),
        req(bug(NOT_FOUND, "done on unknown task -> 404"), "POST", "/api/tasks/task-doesnotexist/done",
            tests=[not_found()]),
    ])


def f_interactions():
    ask = ask_payload()
    return folder("05 Interactions", [
        req("list: empty is []", "GET", "/api/tasks/{{taskA}}/interactions", tests=[
            status(200), J, test("[]", 'pm.expect(j.interactions).to.be.an("array").that.is.empty;')]),
        req("create ask_user_questions", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions", "payload": ask, "idempotency_key": "e2e-ask-1"},
            tests=[status(201), J,
                   test("pending", 'pm.expect(j.status).to.eql("pending"); pm.expect(j.interaction_kind).to.eql("ask_user_questions");'),
                   save("ixAsk", "String(j.id)")]),
        req("duplicate idempotency key returns the same interaction", "POST",
            "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions", "payload": ask, "idempotency_key": "e2e-ask-1"},
            tests=[status_in([200, 201]), J,
                   test("same id", 'pm.expect(String(j.id)).to.eql(pm.collectionVariables.get("ixAsk"));')]),
        req("create request_confirmation (key from payload)", "POST",
            "/api/tasks/{{taskA}}/interactions",
            {"kind": "request_confirmation",
             "payload": json.dumps({"prompt": "Approve plan?", "idempotencyKey": "e2e-confirm-1"})},
            tests=[status(201), J,
                   test("payload idempotencyKey used", 'pm.expect(j.idempotency_key).to.eql("e2e-confirm-1");'),
                   save("ixConfirm", "String(j.id)")]),
        req("confirmation targeting a document with no revisions -> 4xx", "POST",
            "/api/tasks/{{taskA}}/interactions",
            {"kind": "request_confirmation",
             "payload": json.dumps({"prompt": "Approve plan?",
                                    "target": {"type": "issue_document", "key": "plan", "revisionId": 1}})},
            tests=[client_error(), has_error_field()]),
        req("confirmation with revisionId 0 -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "request_confirmation",
             "payload": json.dumps({"prompt": "x", "target": {"type": "issue_document", "key": "plan", "revisionId": 0}})},
            tests=[status(400)]),
        req("create suggest_tasks", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "suggest_tasks", "payload": json.dumps({"tasks": [{"title": "follow-up"}]})},
            tests=[status(201), J, save("ixSuggest", "String(j.id)")]),
        req("list: creation order", "GET", "/api/tasks/{{taskA}}/interactions", tests=[
            status(200), J,
            test("3 interactions ascending by id",
                 'pm.expect(j.interactions).to.have.length(3);'
                 'const ids = j.interactions.map(i => i.id);'
                 'pm.expect(ids).to.eql([...ids].sort((a, b) => a - b));'),
        ]),
        req("missing kind -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"payload": ask}, tests=[status(400), has_error_field()]),
        req("unknown kind -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "bogus", "payload": "{}"}, tests=[status(400)]),
        req("empty payload -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions"}, tests=[status(400)]),
        req("payload not JSON -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions", "payload": "{nope"}, tests=[status(400)]),
        req("question with one option -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions", "payload": ask_payload(("only",))}, tests=[status(400)]),
        req("suggest_tasks with empty list -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "suggest_tasks", "payload": json.dumps({"tasks": []})}, tests=[status(400)]),
        req("confirmation without prompt -> 400", "POST", "/api/tasks/{{taskA}}/interactions",
            {"kind": "request_confirmation", "payload": json.dumps({"prompt": ""})},
            tests=[status(400)]),
        req(bug(NOT_FOUND, "create on unknown task -> 404"), "POST",
            "/api/tasks/task-doesnotexist/interactions",
            {"kind": "ask_user_questions", "payload": ask}, tests=[not_found()]),
        req("list on unknown task -> 404", "GET", "/api/tasks/task-doesnotexist/interactions",
            tests=[not_found()]),
        req("create a fourth interaction for the cross-task check", "POST",
            "/api/tasks/{{taskA}}/interactions",
            {"kind": "ask_user_questions", "payload": ask, "idempotency_key": "e2e-cross"},
            tests=[status(201), J, save("ixCross", "String(j.id)")]),
        req(bug(AUTHZ, "resolve via a different task's URL -> 404"), "POST",
            "/api/tasks/{{taskB}}/interactions/{{ixCross}}/resolve",
            {"status": "accepted"}, tests=[not_found()]),
        req(bug(AUTHZ, "cross-task resolve left the interaction pending"), "GET",
            "/api/tasks/{{taskA}}/interactions", tests=[
                status(200), J,
                test("still pending", 'pm.expect(j.interactions.find(i => String(i.id) === pm.collectionVariables.get("ixCross")).status).to.eql("pending");')]),
        req("resolve: bad status -> 400", "POST",
            "/api/tasks/{{taskA}}/interactions/{{ixConfirm}}/resolve", {"status": "bogus"},
            tests=[status(400)]),
        req("resolve: missing status -> 400", "POST",
            "/api/tasks/{{taskA}}/interactions/{{ixConfirm}}/resolve", {}, tests=[status(400)]),
        req("resolve: non-integer id -> 400", "POST",
            "/api/tasks/{{taskA}}/interactions/abc/resolve", {"status": "accepted"},
            tests=[status(400)]),
        req(bug(NOT_FOUND, "resolve: unknown interaction id -> 404"), "POST",
            "/api/tasks/{{taskA}}/interactions/999999/resolve", {"status": "accepted"},
            tests=[not_found()]),
        req("resolve confirmation: accepted with response", "POST",
            "/api/tasks/{{taskA}}/interactions/{{ixConfirm}}/resolve",
            {"status": "accepted", "response": {"confirmed": True, "feedback": "ok"}}, tests=[
                status(200), J,
                test("accepted + resolved_at + response stored",
                     'pm.expect(j.status).to.eql("accepted"); pm.expect(j.resolved_at).to.be.a("string");'
                     'pm.expect(JSON.parse(j.response).confirmed).to.eql(true);'),
            ]),
        req("resolve twice -> 4xx (409 preferred)", "POST",
            "/api/tasks/{{taskA}}/interactions/{{ixConfirm}}/resolve", {"status": "rejected"},
            tests=[client_error()]),
        req("a new comment supersedes pending interactions", "POST",
            "/api/tasks/{{taskA}}/comments", {"message": "user replied in thread"},
            tests=[status(201)]),
        req("pending ones are now superseded; resolved one untouched", "GET",
            "/api/tasks/{{taskA}}/interactions", tests=[
                status(200), J,
                test("statuses",
                     'const by = Object.fromEntries(j.interactions.map(i => [String(i.id), i.status]));'
                     'pm.expect(by[pm.collectionVariables.get("ixAsk")]).to.eql("superseded");'
                     'pm.expect(by[pm.collectionVariables.get("ixSuggest")]).to.eql("superseded");'
                     'pm.expect(by[pm.collectionVariables.get("ixConfirm")]).to.eql("accepted");'),
            ]),
    ])


def f_governance():
    return folder("06 Governance, reviewers, approvers, transitions", [
        req("create task G", "POST", "/api/tasks", {"name": "e2e governance task"},
            tests=[status(201), J, save("taskG", "j.id")]),
        req(bug(CONTRACT, "governance of a fresh task uses [] not null"), "GET",
            "/api/tasks/{{taskG}}/governance", tests=[
                status(200), J,
                test("empty arrays", 'for (const k of ["reviewers","approvers","reviews","votes"]) pm.expect(j[k], k).to.be.an("array").that.is.empty;')]),
        req("governance of unknown task -> 404", "GET",
            "/api/tasks/task-doesnotexist/governance", tests=[not_found()]),
        req("set governance: threshold 1, review required", "POST",
            "/api/tasks/{{taskG}}/governance",
            {"approval_threshold": 1, "require_review": True, "actor_id": "qa-bot"}, tests=[
                status(200), J,
                test("config stored", 'pm.expect(j.approval_threshold).to.eql(1); pm.expect(j.require_review).to.eql(true);')]),
        req("set governance: malformed JSON -> 400", "POST", "/api/tasks/{{taskG}}/governance",
            raw="nope", tests=[status(400)]),
        req("set governance on unknown task -> 404", "POST",
            "/api/tasks/task-doesnotexist/governance", {"approval_threshold": 1},
            tests=[not_found()]),
        req("assign reviewer", "POST", "/api/tasks/{{taskG}}/reviewers",
            {"reviewer_id": "rev-1", "reviewer_type": "agent", "actor_id": "qa-bot"},
            tests=[status(201)]),
        req("assign same reviewer again (duplicate)", "POST", "/api/tasks/{{taskG}}/reviewers",
            {"reviewer_id": "rev-1", "reviewer_type": "agent"}, tests=[status_in([200, 201, 409])]),
        req("reviewer missing id -> 400", "POST", "/api/tasks/{{taskG}}/reviewers", {},
            tests=[status(400), has_error_field()]),
        req("reviewer on unknown task -> 404", "POST", "/api/tasks/task-doesnotexist/reviewers",
            {"reviewer_id": "rev-1"}, tests=[not_found()]),
        req("assign approver", "POST", "/api/tasks/{{taskG}}/approvers",
            {"approver_id": "app-1", "approver_type": "user"}, tests=[status(201)]),
        req("assign same approver again (duplicate)", "POST", "/api/tasks/{{taskG}}/approvers",
            {"approver_id": "app-1", "approver_type": "user"}, tests=[status_in([200, 201, 409])]),
        req("approver missing id -> 400", "POST", "/api/tasks/{{taskG}}/approvers", {},
            tests=[status(400)]),
        req("approver on unknown task -> 404", "POST", "/api/tasks/task-doesnotexist/approvers",
            {"approver_id": "app-1"}, tests=[not_found()]),
        req("set watchdog keeps threshold/review", "POST", "/api/tasks/{{taskG}}/watchdog",
            {"watchdog_agent_id": "wd-1", "watchdog_prompt": "watch scope creep"}, tests=[
                status(200), J,
                test("merged config", 'pm.expect(j.watchdog_agent_id).to.eql("wd-1"); pm.expect(j.require_review).to.eql(true); pm.expect(j.approval_threshold).to.eql(1);')]),
        req("watchdog on unknown task -> 404", "POST", "/api/tasks/task-doesnotexist/watchdog",
            {"watchdog_agent_id": "wd-1"}, tests=[not_found()]),
        req("snapshot lists each assignee once", "GET", "/api/tasks/{{taskG}}/governance", tests=[
            status(200), J,
            test("one reviewer, one approver",
                 'pm.expect(j.reviewers.filter(r => r.reviewer_id === "rev-1")).to.have.length(1);'
                 'pm.expect(j.approvers.filter(a => a.approver_id === "app-1")).to.have.length(1);'
                 'pm.expect(j.config.watchdog_agent_id).to.eql("wd-1");'),
        ]),
        req("transition: missing to -> 400", "POST", "/api/tasks/{{taskG}}/transition", {},
            tests=[status(400)]),
        req("transition: todo -> done skips stages -> 422", "POST",
            "/api/tasks/{{taskG}}/transition", {"to": "done"}, tests=[status(422), has_error_field()]),
        req("transition: unknown stage -> 4xx", "POST", "/api/tasks/{{taskG}}/transition",
            {"to": "bogus"}, tests=[client_error()]),
        req("transition on unknown task -> 404", "POST",
            "/api/tasks/task-doesnotexist/transition", {"to": "in_progress"}, tests=[not_found()]),
        req("transition: todo -> in_progress", "POST", "/api/tasks/{{taskG}}/transition",
            {"to": "in_progress", "actor_id": "qa-bot"}, tests=[
                status(200), J, test("stage", 'pm.expect(j.execution_stage).to.eql("in_progress");')]),
        req("transition: in_progress -> in_review", "POST", "/api/tasks/{{taskG}}/transition",
            {"to": "in_review", "actor_id": "qa-bot"}, tests=[status(200)]),
        req("in_review -> done before any review -> 422", "POST",
            "/api/tasks/{{taskG}}/transition", {"to": "done"}, tests=[status(422)]),
        req("review: missing reviewer_id -> 400", "POST", "/api/tasks/{{taskG}}/review",
            {"decision": "approved"}, tests=[status(400)]),
        req("review: missing decision -> 400", "POST", "/api/tasks/{{taskG}}/review",
            {"reviewer_id": "rev-1"}, tests=[status(400)]),
        req("review: invalid decision -> 400", "POST", "/api/tasks/{{taskG}}/review",
            {"reviewer_id": "rev-1", "decision": "bogus"}, tests=[status(400)]),
        req(bug(AUTHZ, "review from an unassigned reviewer -> 403"), "POST",
            "/api/tasks/{{taskG}}/review", {"reviewer_id": "stranger", "decision": "approved"},
            tests=[status(403)]),
        req("review on unknown task -> 404", "POST", "/api/tasks/task-doesnotexist/review",
            {"reviewer_id": "rev-1", "decision": "approved"}, tests=[not_found()]),
        req("approve: missing vote -> 400", "POST", "/api/tasks/{{taskG}}/approve",
            {"approver_id": "app-1"}, tests=[status(400)]),
        req("approve: invalid vote -> 400", "POST", "/api/tasks/{{taskG}}/approve",
            {"approver_id": "app-1", "vote": "bogus"}, tests=[status(400)]),
        req(bug(AUTHZ, "vote from an unassigned approver -> 403"), "POST",
            "/api/tasks/{{taskG}}/approve", {"approver_id": "stranger", "vote": "approved"},
            tests=[status(403)]),
        req(bug(AUTHZ, "stranger review + vote must not open the in_review -> done gate"), "POST",
            "/api/tasks/{{taskG}}/transition", {"to": "done"}, tests=[status(422)]),
        req("approve on unknown task -> 404", "POST", "/api/tasks/task-doesnotexist/approve",
            {"approver_id": "app-1", "vote": "approved"}, tests=[not_found()]),
        req("remove approver", "DELETE", "/api/tasks/{{taskG}}/approvers/app-1",
            tests=[status(200)]),
        req("remove reviewer", "DELETE", "/api/tasks/{{taskG}}/reviewers/rev-1",
            tests=[status(200)]),
        req("removed assignees gone from snapshot", "GET", "/api/tasks/{{taskG}}/governance",
            tests=[status(200), J,
                   test("none left", 'pm.expect((j.reviewers || []).length).to.eql(0); pm.expect((j.approvers || []).length).to.eql(0);')]),
        req("remove reviewer on unknown task -> 404", "DELETE",
            "/api/tasks/task-doesnotexist/reviewers/rev-1", tests=[not_found()]),
        req("remove approver on unknown task -> 404", "DELETE",
            "/api/tasks/task-doesnotexist/approvers/app-1", tests=[not_found()]),
        req("audit on unknown task -> 404", "GET", "/api/tasks/task-doesnotexist/audit",
            tests=[not_found()]),
        req("audit: newest first", "GET", "/api/tasks/{{taskG}}/audit", tests=[
            status(200), J,
            test("descending created_at",
                 'const ts = j.audit_log.map(e => e.created_at);'
                 'pm.expect(ts).to.eql([...ts].sort().reverse());'),
        ]),
    ])


def f_workflow():
    return folder("07 Full workflow chain", [
        req("1 create task", "POST", "/api/tasks",
            {"name": "e2e workflow task", "repo_path": "/tmp/staypoint-e2e"},
            tests=[status(201), J, save("wf", "j.id")]),
        req("2 comment", "POST", "/api/tasks/{{wf}}/comments",
            {"author": "planner", "message": "plan: do the thing"}, tests=[status(201)]),
        req("3a require review + 1 approval", "POST", "/api/tasks/{{wf}}/governance",
            {"approval_threshold": 1, "require_review": True, "actor_id": "board"},
            tests=[status(200)]),
        req("3b assign reviewer", "POST", "/api/tasks/{{wf}}/reviewers",
            {"reviewer_id": "reviewer-agent", "reviewer_type": "agent", "actor_id": "board"},
            tests=[status(201)]),
        req("3c assign approver", "POST", "/api/tasks/{{wf}}/approvers",
            {"approver_id": "board-user", "approver_type": "user", "actor_id": "board"},
            tests=[status(201)]),
        req("4 transition todo -> in_progress", "POST", "/api/tasks/{{wf}}/transition",
            {"to": "in_progress", "actor_id": "coder"}, tests=[status(200)]),
        req("5a create confirmation", "POST", "/api/tasks/{{wf}}/interactions",
            {"kind": "request_confirmation", "payload": json.dumps({"prompt": "Merge?"}),
             "idempotency_key": "wf-confirm"},
            tests=[status(201), J, save("wfIx", "String(j.id)")]),
        req("5b resolve confirmation", "POST", "/api/tasks/{{wf}}/interactions/{{wfIx}}/resolve",
            {"status": "accepted", "response": {"confirmed": True}}, tests=[status(200)]),
        req("6 transition in_progress -> in_review", "POST", "/api/tasks/{{wf}}/transition",
            {"to": "in_review", "actor_id": "coder"}, tests=[status(200)]),
        req("7a reviewer approves", "POST", "/api/tasks/{{wf}}/review",
            {"reviewer_id": "reviewer-agent", "decision": "approved", "notes": "lgtm"},
            tests=[status(200)]),
        req("7b approver votes", "POST", "/api/tasks/{{wf}}/approve",
            {"approver_id": "board-user", "vote": "approved"}, tests=[status(200)]),
        req("8 transition in_review -> done", "POST", "/api/tasks/{{wf}}/transition",
            {"to": "done", "actor_id": "board"}, tests=[
                status(200), J, test("done", 'pm.expect(j.execution_stage).to.eql("done");')]),
        req("done is terminal: done -> in_progress -> 422", "POST",
            "/api/tasks/{{wf}}/transition", {"to": "in_progress"}, tests=[status(422)]),
        req("9a task view reflects the run", "GET", "/api/tasks/{{wf}}", tests=[
            status(200), J,
            test("stage done, comment kept",
                 'pm.expect(j.task.execution_stage).to.eql("done");'
                 'pm.expect(j.comments.map(c => c.message)).to.include("plan: do the thing");'),
        ]),
        req("9b interaction resolved", "GET", "/api/tasks/{{wf}}/interactions", tests=[
            status(200), J,
            test("accepted", 'pm.expect(j.interactions.find(i => String(i.id) === pm.collectionVariables.get("wfIx")).status).to.eql("accepted");')]),
        req("9c audit has governance history", "GET", "/api/tasks/{{wf}}/audit", tests=[
            status(200), J,
            test("assignments, review, vote and every transition are logged",
                 'const ev = j.audit_log;'
                 'const types = ev.map(e => e.event_type);'
                 'const hops = ev.filter(e => e.event_type === "state_transition").map(e => e.from_state + ">" + e.to_state);'
                 'pm.expect(hops).to.include.members(["todo>in_progress","in_progress>in_review","in_review>done"]);'
                 'pm.expect(types).to.include.members(["approval_vote"]);'
                 'pm.expect(types.some(t => /review/.test(t))).to.eql(true);'
                 'pm.expect(types.some(t => /reviewer/.test(t))).to.eql(true);'
                 'pm.expect(types.some(t => /approver/.test(t))).to.eql(true);'),
        ]),
        req(bug(AUDIT, "9d audit also shows the comment and the interaction"), "GET",
            "/api/tasks/{{wf}}/audit", tests=[
                status(200), J,
                test("comment + interaction events present",
                     'const types = j.audit_log.map(e => e.event_type).join(" ");'
                     'pm.expect(types).to.match(/comment/);'
                     'pm.expect(types).to.match(/interaction/);'),
            ]),
    ])


def f_sessions():
    return folder("08 Sessions", [
        req("register session", "POST", "/api/sessions",
            {"id": "e2e-sess-1", "agent_type": "claude", "repo_path": "/tmp/staypoint-e2e",
             "git_branch": "main", "pid": 4242, "hostname": "e2e-host"},
            tests=[status(201), J, test("echoes id", 'pm.expect(j.id).to.eql("e2e-sess-1");')]),
        req("re-register same id is an upsert", "POST", "/api/sessions",
            {"id": "e2e-sess-1", "agent_type": "claude", "repo_path": "/tmp/staypoint-e2e"},
            tests=[status_in([200, 201])]),
        req("missing required fields -> 400", "POST", "/api/sessions", {"id": "e2e-sess-2"},
            tests=[status(400), has_error_field()]),
        req("malformed JSON -> 400", "POST", "/api/sessions", raw="{", tests=[status(400)]),
        req("get session", "GET", "/api/sessions/e2e-sess-1", tests=[
            status(200), J, test("active", 'pm.expect(j.status).to.eql("active"); pm.expect(j.agent_type).to.eql("claude");')]),
        req("get unknown session -> 404", "GET", "/api/sessions/nope", tests=[not_found()]),
        req("heartbeat", "POST", "/api/sessions/e2e-sess-1/heartbeat", tests=[status(200)]),
        req("heartbeat unknown -> 404", "POST", "/api/sessions/nope/heartbeat",
            tests=[not_found()]),
        req("close session", "POST", "/api/sessions/e2e-sess-1/close", tests=[status(200)]),
        req("closed session shows closed", "GET", "/api/sessions/e2e-sess-1", tests=[
            status(200), J, test("closed", 'pm.expect(j.status).to.eql("closed");')]),
        req(bug(NOT_FOUND, "close unknown session -> 404"), "POST", "/api/sessions/nope/close",
            tests=[not_found()]),
        req("list sessions includes it", "GET", "/api/sessions", tests=[
            status(200), J,
            test("present", 'pm.expect(j.sessions.map(s => s.id)).to.include("e2e-sess-1");')]),
        req("list agents returns array", "GET", "/api/agents", tests=[
            status(200), J, test("agents array", 'pm.expect(j.agents).to.be.an("array");')]),
    ])


def f_threads():
    return folder("09 Threads", [
        req(bug(CONTRACT, "list threads: empty is [] not null"), "GET", "/api/threads", tests=[
            status(200), J, test("threads array", 'pm.expect(j.threads).to.be.an("array");')]),
        req("create thread", "POST", "/api/threads",
            {"title": "e2e thread", "repo_path": "/tmp/staypoint-e2e", "system_prompt": "be brief"},
            tests=[status(201), J, save("thread", "j.id")]),
        req("malformed JSON -> 400", "POST", "/api/threads", raw="x", tests=[status(400)]),
        req("get thread", "GET", "/api/threads/{{thread}}", tests=[
            status(200), J, test("title", 'pm.expect(j.title).to.eql("e2e thread");')]),
        req("get unknown thread -> 404", "GET", "/api/threads/nope", tests=[not_found()]),
        req("append user message", "POST", "/api/threads/{{thread}}/messages",
            {"role": "user", "content": "hello", "token_count": 2}, tests=[
                status(201), J, test("seq 0", 'pm.expect(j.sequence_num).to.eql(0);')]),
        req("append assistant message", "POST", "/api/threads/{{thread}}/messages",
            {"role": "assistant", "content": "hi"}, tests=[
                status(201), J, test("seq 1", 'pm.expect(j.sequence_num).to.eql(1);')]),
        req(bug(CONTRACT, "empty message -> 400"), "POST", "/api/threads/{{thread}}/messages", {},
            tests=[status(400)]),
        req(bug(CONTRACT, "invalid role -> 400, not 500"), "POST", "/api/threads/{{thread}}/messages",
            {"role": "bogus", "content": "x"}, tests=[status(400)]),
        req(bug(NOT_FOUND, "append to unknown thread -> 404"), "POST", "/api/threads/nope/messages",
            {"role": "user", "content": "x"}, tests=[not_found()]),
        req("messages in sequence order", "GET", "/api/threads/{{thread}}/messages", tests=[
            status(200), J,
            test("ordered by sequence_num",
                 'const s = j.messages.map(m => m.sequence_num);'
                 'pm.expect(s).to.eql([...s].sort((a, b) => a - b));'
                 'pm.expect(j.messages[0].content).to.eql("hello");'),
        ]),
        req("messages paginate", "GET", "/api/threads/{{thread}}/messages?limit=1&offset=1", tests=[
            status(200), J,
            test("second message, has_more reflects total",
                 'pm.expect(j.messages).to.have.length(1); pm.expect(j.messages[0].content).to.eql("hi");'
                 'pm.expect(j.has_more).to.eql(j.total > 2);'),
        ]),
        req(bug(NOT_FOUND, "messages of unknown thread -> 404"), "GET", "/api/threads/nope/messages",
            tests=[not_found()]),
        req("list threads includes it with message_count", "GET", "/api/threads", tests=[
            status(200), J,
            test("count", 'const t = j.threads.find(t => t.id === pm.collectionVariables.get("thread"));'
                 'pm.expect(t).to.exist; pm.expect(t.message_count).to.be.at.least(2);')]),
    ])


def f_checklist():
    return folder("10 Checklist", [
        req("seed sprint", "POST", "/api/checklist/seed", {"sprint": "E2E-SPRINT"}, tests=[
            status(200), J, test("seeded > 0", 'pm.expect(j.seeded).to.be.above(0); pm.collectionVariables.set("seeded", j.seeded);')]),
        req("seed again is a no-op (duplicate create)", "POST", "/api/checklist/seed",
            {"sprint": "E2E-SPRINT"}, tests=[
                status(200), J,
                test("all skipped", 'pm.expect(j.seeded).to.eql(0); pm.expect(j.skipped).to.eql(Number(pm.collectionVariables.get("seeded")));')]),
        req("list items for sprint", "GET", "/api/checklist?sprint=E2E-SPRINT", tests=[
            status(200), J,
            test("items present", 'pm.expect(j.items).to.be.an("array").that.is.not.empty;'),
            save("checkItem", "j.items[0].id")]),
        req("sprints include seeded sprint", "GET", "/api/checklist/sprints", tests=[
            status(200), J, test("present", 'pm.expect(j.sprints).to.include("E2E-SPRINT");')]),
        req("update item: partial + notes", "PATCH", "/api/checklist/{{checkItem}}",
            {"status": "partial", "notes": "half done"}, tests=[
                status(200), J,
                test("updated, version bumped", 'pm.expect(j.status).to.eql("partial"); pm.expect(j.notes).to.eql("half done"); pm.expect(j.version).to.be.above(0);')]),
        req("update item: invalid status -> 400", "PATCH", "/api/checklist/{{checkItem}}",
            {"status": "bogus"}, tests=[status(400)]),
        req("update item: malformed JSON -> 400", "PATCH", "/api/checklist/{{checkItem}}",
            raw="x", tests=[status(400)]),
        req("update unknown item -> 404", "PATCH", "/api/checklist/nope",
            {"status": "partial"}, tests=[not_found()]),
        req("history records the change", "GET", "/api/checklist/{{checkItem}}/history", tests=[
            status(200), J,
            test("one partial entry", 'pm.expect(j.history.map(h => h.status)).to.include("partial");')]),
        req(bug(NOT_FOUND, "history of unknown item -> 404"), "GET", "/api/checklist/nope/history",
            tests=[not_found()]),
        req("evaluate (no downgrade, isolated repo root)", "POST",
            "/api/checklist/evaluate?sprint=E2E-SPRINT&downgrade=false&repo_root=/nonexistent",
            tests=[status(200), J,
                   test("summary", 'pm.expect(j.sprint).to.eql("E2E-SPRINT"); pm.expect(j.total).to.be.above(0);')]),
    ])


def f_fleet():
    return folder("11 Telemetry, fleet, report", [
        req("telemetry", "GET", "/api/telemetry", tests=[status(200), J,
            test("has fleet block", 'pm.expect(j).to.have.property("fleet");')]),
        req("fleet overview lists the stub company", "GET", "/api/fleet/overview", tests=[
            status(200), J,
            test("stub org present", 'pm.expect(j.organizations.map(o => o.issue_prefix)).to.include("STB");')]),
        req("fleet task (proxied)", "GET", "/api/fleet/tasks/STB-1", tests=[
            status(200), J, test("identifier", 'pm.expect(j.identifier).to.eql("STB-1");')]),
        req("fleet task unknown -> 404", "GET", "/api/fleet/tasks/STB-404", tests=[not_found()]),
        req("post fleet comment (proxied)", "POST", "/api/fleet/tasks/STB-1/comments",
            {"body": "from the e2e suite"}, tests=[status(201)]),
        req("fleet comment missing body -> 400", "POST", "/api/fleet/tasks/STB-1/comments",
            {"message": "wrong field"}, tests=[status(400)]),
        req("fleet comments include the new one", "GET", "/api/fleet/tasks/STB-1/comments",
            tests=[status(200), J,
                   test("wrapped array", 'pm.expect(j.comments.map(c => c.body)).to.include("from the e2e suite");')]),
        req("fleet comment on unknown issue -> 404", "POST",
            "/api/fleet/tasks/STB-404/comments", {"body": "x"}, tests=[not_found()]),
        req(bug(NOT_FOUND, "fleet comments of unknown issue -> 404"), "GET",
            "/api/fleet/tasks/STB-404/comments", tests=[not_found()]),
        req("report: unknown type -> 400", "GET", "/api/report?type=bogus",
            tests=[status(400), has_error_field()]),
        req("report: work as HTML", "GET", "/api/report?type=work&format=html", tests=[
            status(200),
            test("html", 'pm.expect(pm.response.headers.get("Content-Type")).to.include("text/html");'
                 'pm.expect(pm.response.text()).to.include("<html");')]),
    ])


def f_ui():
    return folder("12 Web UI shell", [
        req("GET / serves dashboard with token injected", "GET", "/", tests=[
            status(200),
            test("html", 'pm.expect(pm.response.headers.get("Content-Type")).to.include("text/html");')]),
        req("GET /ui/ serves static assets", "GET", "/ui/", tests=[status(200)]),
    ])


def f_sse():
    return folder("13 SSE (open in Postman; api-e2e.sh checks it with curl)", [
        req("GET /api/events stream", "GET", "/api/events?cursor=0",
            headers={"Accept": "text/event-stream"}),
        req("GET /api/sse stream (alias)", "GET", "/api/sse?cursor=0",
            headers={"Accept": "text/event-stream"}),
    ], description="Streaming responses never end, so newman skips this folder. "
                   "scripts/api-e2e.sh asserts the stream with curl instead.")


def build():
    items = [f_auth(), f_tasks(), f_comments(), f_blockers(), f_stage_runsteps_done(),
             f_interactions(), f_governance(), f_workflow(), f_sessions(), f_threads(),
             f_checklist(), f_fleet(), f_ui(), f_sse()]
    return {
        "info": {
            "name": "StayPoint daemon API",
            "_postman_id": "6f1c1f43-5d2b-4c8e-9f0e-5a7e2b0c3460",
            "description": (
                "End-to-end suite for the StayPoint daemon HTTP API (STA-346).\n\n"
                "Run headless: scripts/api-e2e.sh. In Postman, import this file and "
                "staypoint.postman_environment.json, set `baseUrl` and `token`, and run "
                "folders in order: later folders reuse ids saved by earlier ones.\n\n"
                "Generated by tests/api/build_collection.py; edit that, not this file.\n\n"
                "Test names prefixed [STA-nnn] fail today because of that open daemon bug."),
            "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json",
        },
        "auth": {"type": "bearer", "bearer": [
            {"key": "token", "value": "{{token}}", "type": "string"}]},
        "item": items,
        "variable": [{"key": k, "value": ""} for k in (
            "taskA", "taskB", "taskDup", "taskG", "wf", "wfIx", "ixAsk", "ixConfirm", "ixCross",
            "ixSuggest", "thread", "checkItem", "seeded")],
    }


def check_routes(repo_root):
    """Fail if any route registered in internal/server lacks an authenticated
    request in the collection. Returns the list of uncovered routes."""
    import glob
    import re
    registered = set()
    for path in glob.glob(os.path.join(repo_root, "internal", "server", "*.go")):
        if path.endswith("_test.go"):
            continue
        with open(path) as f:
            registered.update(re.findall(r'"((?:GET|POST|PUT|PATCH|DELETE) /[^"]*)"', f.read()))

    requests = []

    def walk(items, top):
        for it in items:
            if "item" in it:
                walk(it["item"], top or it["name"])
            elif it["request"].get("auth", {}).get("type") != "noauth":
                url = it["request"]["url"]
                requests.append((it["request"]["method"], "/" + "/".join(url["path"])))

    walk(build()["item"], None)
    missing = []
    for route in sorted(registered):
        method, pattern = route.split(" ", 1)
        rx = "^" + re.sub(r"\\{[^/]+\\}", "[^/]+", re.escape(pattern.rstrip("/"))) + "/?$"
        if not any(m == method and re.match(rx, p) for m, p in requests):
            missing.append(route)
    return missing


if __name__ == "__main__":
    import sys
    if len(sys.argv) > 2 and sys.argv[1] == "--check-routes":
        missing = check_routes(sys.argv[2])
        for r in missing:
            print("uncovered route:", r)
        sys.exit(1 if missing else 0)
    if len(sys.argv) > 1:
        OUT = sys.argv[1]
    with open(OUT, "w") as f:
        json.dump(build(), f, indent=2)
        f.write("\n")
    print("wrote", os.path.relpath(OUT))
