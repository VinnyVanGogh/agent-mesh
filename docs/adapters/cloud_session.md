# cloud_session Adapter

The `cloud_session` adapter launches an Anthropic-managed cloud session via
`claude --cloud`, burning the **$250 cloud session credit** instead of local
or API quota pools.

## When to use

Use `adapterConfig.provider = "cloud_session"` for long-horizon tasks that
would exhaust local quota (Opus, large codebases, overnight runs) or when you
want to offload execution to Anthropic-managed infrastructure entirely.

**Do not** assign `cloud_session` to high-frequency heartbeat or triage agents;
the credit is finite and expires. As of this writing the credit expires
**2026-11-04**.

## Async contract

Cloud sessions are **async by design**. The CLI exits after launching the
session; it does not stream output to stdout. The adapter:

1. Wraps the user prompt with the **repo-output contract** (see below).
2. Runs `claude --cloud --dangerously-skip-permissions --output-format json <prompt>`.
3. Parses `session_id` and `url` from the JSON response.
4. Emits a **synthetic stream-json result event** to the caller:

```json
{
  "type": "result",
  "subtype": "cloud_session_started",
  "session_id": "session_01...",
  "result": "Cloud session started.\nURL: https://claude.ai/code/session_01...\nResult will appear in branch: cloud/session_01..."
}
```

The actual task result arrives later as a branch + PR in the repo.

## Repo-output contract

The prompt is automatically extended with instructions that tell the cloud
session to:

1. Write its full output to a file (`docs/cloud-output/<session-id>.md` by
   default, or `CLAUDE_CLOUD_OUTPUT_FILE` if set).
2. Commit the result to a new branch `cloud/<session-id>`.
3. Open a PR titled `Cloud session result: <task-slug>`.

## Environment variables

| Variable | Default | Purpose |
|---|---|---|
| `CLAUDE_CLOUD_OUTPUT_FILE` | `docs/cloud-output/<session-id>.md` | Override the output file path written inside the session |
| `CLAUDE_CLOUD_REPO_DIR` | `req.Dir` (harness working dir) | Override the working directory passed to `--cloud` |

## Provider chain

`cloud_session` is **opt-in** and **never appears in failover chains**. It
returns a single-element chain with no fallback. If the credit is exhausted or
the binary is missing, the run fails rather than falling back to a quota pool.

```
provider=cloud_session  →  [cloud_session]   (no failover)
provider=claude         →  [work-claude, personal-claude, gemini]  (unchanged)
provider=gemini         →  [gemini, work-claude, personal-claude]  (unchanged)
```

## Output format note

`--cloud` is incompatible with `--output-format stream-json`. The adapter
always passes `--output-format json`, regardless of what the caller requested.
The synthetic result event is stream-json-compatible so existing consumers
handle it without changes.

## Integration with STA-412

[STA-412](/STA/issues/STA-412) adds a Claude Cloud slot to the routing chain
behind a **disabled flag**. Once this adapter passes a real test run against
the $250 credit, that flag can be enabled to allow Paperclip routing to
opt agents into cloud sessions automatically.
