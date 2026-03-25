# Gemini CLI — Headless Mode Reference

> **Version tested**: Gemini CLI **v0.31.0** **Date**: 2026-02-28 **Auth used for tests**: Google account (cached credentials) **OS**: macOS **Source**: Official doc (geminicli.com/docs/cli/headless + google-gemini.github.io) + real test results

---

## 1. CLI Help / Invocation

### Basic headless call

```bash
gemini -p "<prompt>"
```

### Full production call

```bash
gemini -p "<prompt>" \
  --output-format json \             # or stream-json or text (default)
  --yolo \                           # auto-approve all actions (alias: -y)
  -m gemini-3-flash-preview \        # model override
  --all-files                        # include all files in context
```

### Working directory

**No `--cd` flag.** Controlled via the shell's cwd (same as Claude Code):

```go
// In Go adapter:
cmd := exec.Command("gemini", "-p", prompt, "--output-format", "json", "--yolo")
cmd.Dir = "/path/to/worktree"
```

**Tested**: Gemini respects the process cwd (test 5).

### Resume a session

```bash
# Resume most recent session (interactive mode)
gemini --resume

# Resume by index or UUID (interactive mode)
gemini --resume 5
gemini --resume <session-uuid>

# Resume in headless mode (confirmed working!)
gemini --resume -p "<follow-up prompt>" --output-format json --yolo
```

- Session-id is **identical** across resume calls (**tested**: test 3)
- Full conversation context is preserved ("I remember resume-step1.txt")
- `--resume -p` combines resume with headless mode

### Permissions / Approval

```bash
--yolo / -y                          # auto-approve everything (recommended for headless)
--approval-mode auto_edit            # auto-approve file edits, prompt for shell commands
```

No granular `--allowedTools` like Claude Code. No sandbox policy system like Codex.

### Context file

Gemini reads `GEMINI.md` from the working directory at startup (equivalent to Claude Code's `CLAUDE.md`). **Tested** (test 6): GEMINI.md rules were respected — file prefixed with "gump-", avoided forbidden word.

Can also override system prompt via environment:

```bash
GEMINI_SYSTEM_MD=/path/to/custom.md gemini -p "..."
```

### Timeout

No internal timeout flag. Use external `timeout` command:

```bash
timeout 120 gemini -p "..." --output-format json --yolo
```

⚠️ macOS note: `timeout` is not native. Use `gtimeout` from coreutils or a background process with kill.

### Auth

```bash
# Google account (default, uses cached credentials)
gemini -p "..."

# API key
GEMINI_API_KEY=<key> gemini -p "..."
```

---

## 2. Output Format: Text (default)

```bash
gemini -p "Create hello.txt with hello world" --yolo
```

**stdout**:

```
I will create a file named `hello.txt` with the content "hello world".

I've created `hello.txt` with the content "hello world".
```

**stderr**:

```
YOLO mode is enabled. All tool calls will be automatically approved.
Loaded cached credentials.
YOLO mode is enabled. All tool calls will be automatically approved.
```

### Key observations

- Exit code is **0** on success
- Stderr contains auth and mode info, not structured
- Final response on stdout is plain text

---

## 3. Output Format: JSON (--output-format json)

Single JSON object on stdout. **Tested** (test 2).

### Complete field reference (from real output)

```jsonc
{
  "session_id": "f8aecb35-957d-4671-8f75-4c784f8d9918",   // UUID — for resume
  "response": "Created `json-test.txt` with the content \"json works\".",  // final text
  "stats": {
    "models": {                       // per-model breakdown (multi-model visible!)
      "gemini-2.5-flash-lite": {      // utility router model
        "api": {
          "totalRequests": 1,
          "totalErrors": 0,
          "totalLatencyMs": 1361
        },
        "tokens": {
          "input": 839,               // raw input tokens
          "prompt": 839,              // prompt tokens (may differ from input with caching)
          "candidates": 49,           // output/response tokens
          "total": 1028,              // total tokens including thoughts
          "cached": 0,                // cached tokens
          "thoughts": 140,            // thinking/reasoning tokens
          "tool": 0                   // tokens for tool descriptions
        },
        "roles": {
          "utility_router": {         // role played by this model
            "totalRequests": 1,
            "totalErrors": 0,
            "totalLatencyMs": 1361,
            "tokens": { /* same structure */ }
          }
        }
      },
      "gemini-3-flash-preview": {     // main model
        "api": {
          "totalRequests": 2,
          "totalErrors": 0,
          "totalLatencyMs": 2712
        },
        "tokens": {
          "input": 5610,
          "prompt": 11554,
          "candidates": 55,
          "total": 11609,
          "cached": 5944,
          "thoughts": 0,
          "tool": 0
        },
        "roles": {
          "main": { /* same structure */ }
        }
      }
    },
    "tools": {                        // tool execution stats
      "totalCalls": 1,
      "totalSuccess": 1,
      "totalFail": 0,
      "totalDurationMs": 9,
      "totalDecisions": {
        "accept": 1,
        "reject": 0,
        "modify": 0,
        "auto_accept": 0
      },
      "byName": {
        "write_file": {               // per-tool breakdown
          "count": 1,
          "success": 1,
          "fail": 0,
          "durationMs": 9,
          "decisions": { /* same structure */ }
        }
      }
    },
    "files": {                        // file modification stats
      "totalLinesAdded": 1,
      "totalLinesRemoved": 0
    }
  },
  "error": null                       // present only on error; structure: {type, message, code}
}
```

### Error case (from documentation)

```jsonc
{
  "response": null,
  "stats": { /* ... */ },
  "error": {
    "type": "ApiError",              // or "AuthError", etc.
    "message": "Human-readable error description",
    "code": 401                      // optional error code
  }
}
```

### Key observations

- **`session_id` IS present** in JSON output (issue #14435 was resolved)
- **No `cost_usd` field** — only token counts. Cost must be calculated externally.
- **No `is_error` boolean** — check `error` field for null vs object
- **No `duration_ms` at top level** — latency is per-model in `stats.models.*.api.totalLatencyMs`
- **Multi-model visible**: `gemini-2.5-flash-lite` (utility_router) + `gemini-3-flash-preview` (main)
- **Rich tool stats**: per-tool breakdown with decisions (accept/reject/modify/auto_accept)
- Exit code: **0** on success, **0** on impossible tasks (test 4)

---

## 4. Output Format: stream-json (NDJSON)

```bash
gemini -p "<prompt>" --output-format stream-json --yolo
```

One JSON object per line on stdout. **7 event types observed** (test 2b):

### Event Type 1: `init` (always first line)

```jsonc
{
  "type": "init",
  "timestamp": "2026-02-28T17:01:59.810Z",
  "session_id": "50519d94-866a-4a72-a775-43861035d274",   // UUID for resume
  "model": "auto-gemini-3"
}
```

### Event Type 2: `message` (role: user)

```jsonc
{
  "type": "message",
  "timestamp": "2026-02-28T17:01:59.812Z",
  "role": "user",
  "content": "Create a file called stream-test.txt containing 'stream works'"
}
```

### Event Type 3: `message` (role: assistant, delta)

```jsonc
{
  "type": "message",
  "timestamp": "2026-02-28T17:02:01.911Z",
  "role": "assistant",
  "content": "I will create a new file named `stream-test.txt` with the specified",
  "delta": true                      // indicates streaming chunk, not final
}
```

### Event Type 4: `tool_use`

```jsonc
{
  "type": "tool_use",
  "timestamp": "2026-02-28T17:02:02.105Z",
  "tool_name": "write_file",
  "tool_id": "write_file_1772298122105_0",
  "parameters": {
    "content": "stream works\n",
    "file_path": "stream-test.txt"
  }
}
```

### Event Type 5: `tool_result`

```jsonc
{
  "type": "tool_result",
  "timestamp": "2026-02-28T17:02:02.122Z",
  "tool_id": "write_file_1772298122105_0",
  "status": "success"
}
```

### Event Type 6: `message` (role: assistant, final)

```jsonc
{
  "type": "message",
  "timestamp": "2026-02-28T17:02:03.272Z",
  "role": "assistant",
  "content": "Created `stream-test.txt` with the content 'stream works'.",
  "delta": true
}
```

### Event Type 7: `result` (final, always last line)

```jsonc
{
  "type": "result",
  "timestamp": "2026-02-28T17:02:03.275Z",
  "status": "success",
  "stats": {
    "total_tokens": 12659,
    "input_tokens": 12479,
    "output_tokens": 89,
    "cached": 5952,
    "input": 6527,
    "duration_ms": 3468,
    "tool_calls": 1
  }
}
```

### Stream sequence for a typical file-creation task

```
Line 1: init              → session_id, model
Line 2: message (user)    → original prompt
Line 3: message (asst)    → thinking/plan (delta)
Line 4: message (asst)    → continued thinking (delta)
Line 5: tool_use          → write_file with parameters
Line 6: tool_result       → success
Line 7: message (asst)    → summary (delta)
Line 8: result            → final stats (tokens, duration, tool_calls)
```

### Key differences from JSON mode

- `result` event has **flat stats** (total_tokens, input_tokens, output_tokens, cached, duration_ms, tool_calls) — NOT the detailed per-model breakdown of JSON mode
- `session_id` is in the `init` event, not in `result`
- Assistant messages are streamed as deltas (`"delta": true`)

---

## 5. Resume Behavior

```bash
# Step 1: capture session_id from JSON output
SESSION_ID=$(gemini -p "create step1.txt" --output-format json --yolo | jq -r '.session_id')

# Step 2: resume in headless mode
gemini --resume -p "create step2.txt" --output-format json --yolo
```

**Confirmed behaviors (test 3):**

- Session-id from step 1: `69b47bf6-75c2-4106-bdaa-14a8b81c20da`
- Session-id from step 2 (--resume -p): `69b47bf6-75c2-4106-bdaa-14a8b81c20da` ← **identical**
- `--resume -p` combines headless mode with session resume
- Full conversation context is preserved: "I remember resume-step1.txt"
- Both resume-step1.txt and resume-step2.txt created successfully

**⚠️ Limitations:**

- `--resume` without arguments resumes the **most recent** session (no way to specify UUID in headless mode via flag — only in interactive mode by index or UUID)
- Sessions are **project-specific** (stored per working directory hash in `~/.gemini/tmp/<project_hash>/chats/`)

---

## 6. Error Detection Strategy

|Layer|Check|Detects|
|---|---|---|
|1|`error` field non-null (JSON mode)|API errors, auth failures, infra issues|
|2|`error.type`|Error category: "ApiError", "AuthError", etc.|
|3|`result.status` (stream-json)|"success" or "error"|
|4|`response` text|Agent refusal or inability|
|5|Check expected file existence|Agent claimed success but didn't deliver|

**Test 4 result**: Impossible task → exit 0, `error: null`, `response` present (agent explains why it can't delete the file). Same behavior as Claude Code and Codex — no structured error for agent-level failures.

---

## 7. Models and Gump Aliases

### Available models (-m flag)

|Model ID|Description|
|---|---|
|`auto-gemini-3`|Auto routing : Gemini 3.1 Pro pour complex, Flash pour simple (défaut)|
|`gemini-3.1-pro-preview`|Gemini 3.1 Pro — le plus avancé, reasoning amélioré (ARC-AGI-2: 77.1%)|
|`gemini-3-flash`|Gemini 3 Flash — Pro-level intelligence, prix Flash|
|`gemini-3.1-flash-lite`|Gemini 3.1 Flash Lite — high-volume, cost-efficient workhorse|
|`gemini-2.5-pro-preview`|Gemini 2.5 Pro — deep reasoning (legacy)|
|`gemini-2.5-flash`|Gemini 2.5 Flash — fast, stable (legacy)|

Gemini CLI supporte aussi le routing via `/model` : `Auto` (défaut), `Pro`, `Flash`. Gemini 3 Pro Preview (`gemini-3-pro-preview`) est deprecated depuis le 9 mars 2026 — migrer vers `gemini-3.1-pro-preview`.

### Gump alias → model flag

|Alias Gump|`-m` flag|Description|
|---|---|---|
|`gemini`|(omis — défaut auto-gemini-3)|Auto routing Gemini 3|
|`gemini-pro`|`gemini-3.1-pro-preview`|Gemini 3.1 Pro|
|`gemini-flash`|`gemini-3-flash`|Gemini 3 Flash|
|`gemini-flash-lite`|`gemini-3.1-flash-lite`|Gemini 3.1 Flash Lite|
|`gemini-25-pro`|`gemini-2.5-pro-preview`|Gemini 2.5 Pro (legacy)|
|`gemini-25-flash`|`gemini-2.5-flash`|Gemini 2.5 Flash (legacy)|

Fallback : si l'alias ne matche pas la table, extraire la partie après `gemini-` et la passer tel quel à `-m`.

---

## 8. Multi-Model Behavior

Gemini CLI uses **multiple models** visibly in a single session. Observed in all tests:

```jsonc
"models": {
  "gemini-2.5-flash-lite": {
    "roles": { "utility_router": { ... } },   // routing/classification
    "tokens": { "input": 839, "candidates": 49, "thoughts": 140 }
  },
  "gemini-3-flash-preview": {
    "roles": { "main": { ... } },             // primary reasoning
    "tokens": { "input": 5610, "candidates": 55, "thoughts": 0 }
  }
}
```

Key differences from Claude Code:

- **Roles are explicit**: `utility_router`, `main` (Claude Code doesn't expose roles)
- **Thoughts tokens tracked separately**: `thoughts` field per model
- **No cost_usd**: Must calculate cost from token counts + model pricing
- **Per-role token breakdown**: Each model can have multiple roles with separate stats

---

## 9. GEMINI.md Integration

Gemini reads `GEMINI.md` from the working directory at startup. **Tested** (test 6):

```markdown
# Project Rules
- Always prefix created files with "gump-"
- Never use the word "hello" in any file content
```

Result: Gemini created `gump-greeting.txt` (correct prefix) and avoided the word "hello". Rules were respected.

Can also override the system prompt entirely:

```bash
GEMINI_SYSTEM_MD=/path/to/custom.md gemini -p "..."
```

---

## 10. Known Issues / Gotchas

1. **No --cd flag**: Unlike Codex (`-C`), Gemini has no native working directory flag. Must control via process cwd.
    
2. **No cost_usd**: Only token counts. Calculate cost externally.
    
3. **No granular tool control**: No `--allowedTools` equivalent. Only `--yolo` (approve all) or `--approval-mode`.
    
4. **Long-running headless tasks may hang**: Test 4 with a complex math prompt hung indefinitely — Gemini has no built-in timeout. Always wrap with external timeout.
    
5. **macOS: no native `timeout` command**: Use `gtimeout` from coreutils.
    
6. **Resume in headless**: `--resume -p` works but only resumes the most recent session. Cannot specify UUID in headless mode via flag — session files are project-specific.
    
7. **stream-json result has flat stats**: Unlike JSON mode's rich per-model breakdown, the `result` event in stream-json only has aggregated numbers.
    
8. **`delta: true` on all assistant messages**: All streamed assistant messages have `delta: true`. There's no `delta: false` final message — the `result` event marks completion.
    
9. **Multiple stderr YOLO warnings**: "YOLO mode is enabled" is printed twice on stderr.
    

---

## 11. Delta: Official Doc vs Observed Reality

|Aspect|Official Doc|Observed (v0.31.0)|Impact|
|---|---|---|---|
|`session_id` in JSON|Issue #14435 requested it|✅ Present at top level|Can extract for resume|
|`--resume -p` (headless resume)|Not documented as supported|✅ Works, context preserved|Critical for Gump|
|`GEMINI.md` auto-read|Documented|✅ Confirmed, rules respected|Context file works|
|Exit code on impossible task|Not documented|0 (no structured error)|Must parse response text|
|Multi-model in JSON|Example shows single model|Always multi-model (flash-lite + flash)|Must iterate all models for metrics|
|`thoughts` tokens|Not explicitly documented|Present per-model, significant counts|Affects cost calculation|
|`roles` field|Not documented|Present per-model (utility_router, main)|Useful for debugging|
|stream-json event types|Documented (init, message, etc.)|Confirmed: init, message, tool_use, tool_result, result|Matches doc|
|`delta: true` on messages|Documented|All assistant messages are deltas|No non-delta messages observed|
|Tool names|Not fully documented|`write_file`, `run_shell_command` observed|Different from Claude's `Write`, `Bash`|

---

## 12. Raw Test Data

### Test 2 — JSON output (success, file creation)

```json
{"session_id":"f8aecb35-957d-4671-8f75-4c784f8d9918","response":"Created `json-test.txt` with the content \"json works\".","stats":{"models":{"gemini-2.5-flash-lite":{"api":{"totalRequests":1,"totalErrors":0,"totalLatencyMs":1361},"tokens":{"input":839,"prompt":839,"candidates":49,"total":1028,"cached":0,"thoughts":140,"tool":0},"roles":{"utility_router":{"totalRequests":1,"totalErrors":0,"totalLatencyMs":1361,"tokens":{"input":839,"prompt":839,"candidates":49,"total":1028,"cached":0,"thoughts":140,"tool":0}}}},"gemini-3-flash-preview":{"api":{"totalRequests":2,"totalErrors":0,"totalLatencyMs":2712},"tokens":{"input":5610,"prompt":11554,"candidates":55,"total":11609,"cached":5944,"thoughts":0,"tool":0},"roles":{"main":{"totalRequests":2,"totalErrors":0,"totalLatencyMs":2712,"tokens":{"input":5610,"prompt":11554,"candidates":55,"total":11609,"cached":5944,"thoughts":0,"tool":0}}}}},"tools":{"totalCalls":1,"totalSuccess":1,"totalFail":0,"totalDurationMs":9,"totalDecisions":{"accept":1,"reject":0,"modify":0,"auto_accept":0},"byName":{"write_file":{"count":1,"success":1,"fail":0,"durationMs":9,"decisions":{"accept":1,"reject":0,"modify":0,"auto_accept":0}}}},"files":{"totalLinesAdded":1,"totalLinesRemoved":0}}}
```

### Test 2b — stream-json (8 lines NDJSON)

```json
{"type":"init","timestamp":"2026-02-28T17:01:59.810Z","session_id":"50519d94-866a-4a72-a775-43861035d274","model":"auto-gemini-3"}
{"type":"message","timestamp":"2026-02-28T17:01:59.812Z","role":"user","content":"Create a file called stream-test.txt containing 'stream works'"}
{"type":"message","timestamp":"2026-02-28T17:02:01.911Z","role":"assistant","content":"I will create a new file named `stream-test.txt` with the specified","delta":true}
{"type":"message","timestamp":"2026-02-28T17:02:01.924Z","role":"assistant","content":" content.\n","delta":true}
{"type":"tool_use","timestamp":"2026-02-28T17:02:02.105Z","tool_name":"write_file","tool_id":"write_file_1772298122105_0","parameters":{"content":"stream works\n","file_path":"stream-test.txt"}}
{"type":"tool_result","timestamp":"2026-02-28T17:02:02.122Z","tool_id":"write_file_1772298122105_0","status":"success"}
{"type":"message","timestamp":"2026-02-28T17:02:03.272Z","role":"assistant","content":"Created `stream-test.txt` with the content 'stream works'.","delta":true}
{"type":"result","timestamp":"2026-02-28T17:02:03.275Z","status":"success","stats":{"total_tokens":12659,"input_tokens":12479,"output_tokens":89,"cached":5952,"input":6527,"duration_ms":3468,"tool_calls":1}}
```

### Test 3 — Resume (session-id preserved)

Step 1 session_id: `69b47bf6-75c2-4106-bdaa-14a8b81c20da` Step 2 session_id (--resume -p): `69b47bf6-75c2-4106-bdaa-14a8b81c20da` ← **identical** Context preserved: "I remember resume-step1.txt"

### Test 4 — Error handling (impossible task)

Exit code: 0. error: null. response: "The file `/nonexistent/impossible/path.txt` does not exist and therefore could not be deleted." No structured error for agent-level failures.

### Test 5 — cwd

Working directory correctly set via process cwd. cwd-proof.txt created in `/private/tmp/gump-gemini-cwd-test`. Multi-model: gemini-2.5-flash-lite + gemini-3-flash-preview.

### Test 6 — GEMINI.md

Rules respected: file created as `gump-greeting.txt` (correct prefix), avoided "hello".