# Cursor Agent CLI — Headless Mode Reference

> **Version tested**: Cursor Agent **2026.03.20-44cb435** **IDE version**: Cursor 2.6.19 **Date**: 2026-03-23 **Auth used for tests**: OAuth (login) **OS**: macOS arm64 **Source**: `cursor agent --help` + real test results (C1-C7)

---

## 1. Architecture

Cursor Agent est un **binaire standalone séparé de l'IDE**. Il n'est PAS un fork de Claude Code CLI.

- **IDE** : `/usr/local/bin/cursor` → shell wrapper qui route vers l'app Electron (éditeur) ou vers `cursor-agent` (sous-commande `agent`).
- **Agent** : `~/.local/bin/cursor-agent` → symlink vers `~/.local/share/cursor-agent/versions/<version>/cursor-agent`. Runtime Node.js standalone.
- **Installation** : auto-installé au premier `cursor agent`. Mise à jour via `cursor-agent update`.

Le wrapper `/usr/local/bin/cursor` détecte `$1 == "agent"` et `exec ~/.local/bin/cursor-agent "$@"`.

---

## 2. CLI Help (verbatim from 2026.03.20)

```
Usage: agent [options] [command] [prompt...]

Start the Cursor Agent

Arguments:
  prompt                       Initial prompt for the agent

Options:
  -v, --version                Output the version number
  --api-key <key>              API key for authentication (can also use CURSOR_API_KEY env var)
  -H, --header <header>        Add custom header to agent requests (format: 'Name: Value', can be used multiple times)
  -p, --print                  Print responses to console (for scripts or non-interactive use). Has access to all tools,
                               including write and shell. (default: false)
  --output-format <format>     Output format (only works with --print): text | json | stream-json (default: "text")
  --stream-partial-output      Stream partial output as individual text deltas (only works with --print and stream-json format)
  --mode <mode>                Start in the given execution mode. plan: read-only/planning. ask: Q&A style. (choices: "plan", "ask")
  --plan                       Start in plan mode (shorthand for --mode=plan). Ignored if --cloud is passed.
  --resume [chatId]            Select a session to resume
  --continue                   Continue previous session
  --model <model>              Model to use (e.g., gpt-5, sonnet-4, sonnet-4-thinking)
  --list-models                List available models and exit
  -f, --force                  Force allow commands unless explicitly denied
  --yolo                       Alias for --force (Run Everything)
  --sandbox <mode>             Explicitly enable or disable sandbox mode (choices: "enabled", "disabled")
  --approve-mcps               Automatically approve all MCP servers
  --trust                      Trust the current workspace without prompting (only works with --print/headless mode)
  --workspace <path>           Workspace directory to use (defaults to current working directory)
  -w, --worktree [name]        Start in an isolated git worktree at ~/.cursor/worktrees/<reponame>/<name>.
  --worktree-base <branch>     Branch or ref to base the new worktree on (default: current HEAD)
  -c, --cloud                  Start in cloud mode (open composer picker on launch)
  -h, --help                   Display help for command

Commands:
  login                        Authenticate with Cursor.
  logout                       Sign out and clear stored authentication
  status|whoami                View authentication status
  models                       List available models for this account
  about                        Display version, system, and account information
  update                       Update Cursor Agent to the latest version
  create-chat                  Create a new empty chat and return its ID
  generate-rule|rule           Generate a new Cursor rule with interactive prompts
  resume                       Resume the latest chat session
  ls                           Resume a chat session
  help [command]               Display help for command
```

### Key differences from Claude Code CLI

|Aspect|Claude Code|Cursor Agent|
|---|---|---|
|Binary|`claude` (single binary)|`cursor-agent` (separate from IDE)|
|Headless flag|`-p` (print)|`-p, --print` (same semantics)|
|Output format|`--output-format text\|json\|stream-json`|Same ✅|
|`--verbose` required for stream-json|Yes|**No** (works without it)|
|Working directory|No flag (cmd.Dir only)|`--workspace <path>` ✅|
|Auto-approve|`--permission-mode acceptEdits`|`--yolo` / `--force`|
|Trust workspace|N/A|`--trust` (required in headless)|
|Max turns|`--max-turns N`|**Not available**|
|Allowed tools|`--allowedTools "Bash,Read,Write,Edit"`|**Not available**|
|Resume|`--resume <session-id>`|`--resume [chatId]` ✅|
|Continue|`--continue`|`--continue` ✅|
|Model|`--model sonnet\|opus\|haiku`|`--model <model-id>` (different naming)|
|Context file|CLAUDE.md|`.cursor/rules/*.mdc`|
|Built-in worktree|No|`-w, --worktree [name]`|
|Cost reporting|`total_cost_usd` in result|**Not available**|
|Turns reporting|`num_turns` in result|**Not available**|

---

## 3. Output Format: JSON (final)

Single JSON object on stdout. **Tested** (C1).

### Complete field reference

```jsonc
{
  "type": "result",
  "subtype": "success",                // "success" or "error" (not observed)
  "is_error": false,                   // boolean
  "duration_ms": 5328,                 // wall clock time
  "duration_api_ms": 5328,             // API time (same as duration_ms in tests)
  "result": "Le fichier hello.txt...", // final text response
  "session_id": "a9f4a1ce-971c-4a24-9ee9-3f8342293c93",  // UUID for resume
  "request_id": "7b7cbe41-48aa-4ed6-9e1d-9c02d353d8bc",  // request trace ID
  "usage": {
    "inputTokens": 4,                  // NOTE: camelCase (NOT snake_case)
    "outputTokens": 117,
    "cacheReadTokens": 28154,
    "cacheWriteTokens": 1197
  }
}
```

### Key observations

- **No `total_cost_usd`** — only token counts. Cost must be calculated externally.
- **No `num_turns`** — not available in the JSON output.
- **No `modelUsage`** — no per-model breakdown.
- **Usage fields are camelCase** — `inputTokens` not `input_tokens` (differs from Claude Code).
- **`request_id`** — new field not present in Claude Code.
- **Exit code** is 0 even on impossible tasks (same as Claude Code).
- **`is_error: false`** even on impossible tasks (same as Claude Code).

---

## 4. Output Format: stream-json (NDJSON)

```bash
cursor-agent -p --output-format stream-json --yolo --trust --workspace <dir> "prompt"
```

**No `--verbose` required** (unlike Claude Code). One JSON object per line on stdout.

### 6 event types observed (C2)

#### Type 1: `system/init` (always first line)

```jsonc
{
  "type": "system",
  "subtype": "init",
  "apiKeySource": "login",              // "login" (OAuth) or "api_key"
  "cwd": "/private/tmp/gump-cursor-tests",
  "session_id": "644c7cde-a660-4224-a26b-0b0a7e279293",
  "model": "Sonnet 4.6 1M",            // display name, NOT model ID
  "permissionMode": "default"           // "default" with --yolo
}
```

#### Type 2: `user` (prompt echo)

```jsonc
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [{"type": "text", "text": "Create a file..."}]
  },
  "session_id": "644c7cde-..."
}
```

#### Type 3: `assistant` (text response)

```jsonc
{
  "type": "assistant",
  "message": {
    "role": "assistant",
    "content": [{"type": "text", "text": "Je vais créer ce fichier."}]
  },
  "session_id": "644c7cde-...",
  "model_call_id": "53df2d2f-...-m2qz",   // links to tool_calls from same LLM turn
  "timestamp_ms": 1774272397614
}
```

#### Type 4: `tool_call/started`

```jsonc
{
  "type": "tool_call",
  "subtype": "started",
  "call_id": "toolu_bdrk_01EKXzaxy6vAqm6a3wL93cNY",
  "tool_call": {
    "editToolCall": {                       // tool type is the key name
      "args": {
        "path": "/tmp/gump-cursor-tests/stream-test.txt",
        "streamContent": "cursor stream works"
      }
    }
  },
  "model_call_id": "53df2d2f-...-m2qz",
  "session_id": "644c7cde-...",
  "timestamp_ms": 1774272397615
}
```

#### Type 5: `tool_call/completed`

```jsonc
{
  "type": "tool_call",
  "subtype": "completed",
  "call_id": "toolu_bdrk_01EKXzaxy6vAqm6a3wL93cNY",
  "tool_call": {
    "editToolCall": {
      "args": {
        "path": "/tmp/gump-cursor-tests/stream-test.txt",
        "streamContent": "cursor stream works"
      },
      "result": {
        "success": {
          "path": "/tmp/gump-cursor-tests/stream-test.txt",
          "linesAdded": 1,
          "linesRemoved": 1,
          "diffString": "-\n+cursor stream works",
          "afterFullFileContent": "cursor stream works",
          "message": "Wrote contents to /tmp/gump-cursor-tests/stream-test.txt"
        }
      }
    }
  },
  "model_call_id": "53df2d2f-...-m2qz",
  "session_id": "644c7cde-...",
  "timestamp_ms": 1774272397722
}
```

#### Type 6: `result` (final, always last line)

Same schema as the JSON final output (section 3).

### Stream sequence for a typical file-creation task

```
Line 1: system/init          → session metadata
Line 2: user                 → prompt echo
Line 3: assistant            → text (thinking/plan)
Line 4: tool_call/started    → editToolCall with path + content
Line 5: tool_call/completed  → result with diff, linesAdded
Line 6: assistant            → text (confirmation)
Line 7: result               → final aggregated result
```

### Key differences from Claude Code stream

|Aspect|Claude Code|Cursor Agent|
|---|---|---|
|Tool use|`type: "assistant"` → `content[].type: "tool_use"`|`type: "tool_call"` → `subtype: "started"\|"completed"`|
|Tool result|`type: "user"` → `content[].type: "tool_result"`|Integrated in `tool_call/completed` → `result.success`|
|Tool name|Explicit: `Write`, `Read`, `Bash`, `Edit`|Implicit: key name in `tool_call` object (`editToolCall`, etc.)|
|File path|`input.file_path`|`tool_call.editToolCall.args.path`|
|Per-turn usage|`message.usage` on each `assistant` event|**Not available in stream**|
|Rate limit events|`rate_limit_event` between turns|**Not present**|
|`model_call_id`|Not present|Present (links assistant + tool_call events)|
|`timestamp_ms`|Not present on events|Present on assistant + tool_call events|

### Tool type extraction

The tool type is the key name inside `tool_call`:

- `editToolCall` → Write/Edit (file creation or modification)
- Other tool types not observed in tests — may include `shellToolCall`, `readToolCall`, etc.

To extract the tool type programmatically: `Object.keys(event.tool_call)[0]`.

To extract the file path: `event.tool_call[toolType].args.path`.

---

## 5. Resume Behavior

```bash
# Step 1: capture session_id
SESSION_ID=$(... | python3 -c "import json,sys; print(json.load(sys.stdin)['session_id'])")

# Step 2: resume by ID
cursor-agent -p --output-format json --yolo --trust --resume "$SESSION_ID" "follow-up prompt"
```

**Confirmed behaviors (C3):**

- Session-id from step 1: `87109ddc-c0f9-43bd-a009-8fda0585ec0a`
- Session-id from step 2 (--resume): `87109ddc-c0f9-43bd-a009-8fda0585ec0a` ← **identical**
- Full conversation context is preserved
- Both step1.txt and step2.txt created successfully
- `--continue` also available (resumes most recent session)

---

## 6. Error Detection

Exit code is **always 0**. `is_error` is **always false** even on impossible tasks.

|Layer|Check|Detects|
|---|---|---|
|1|`is_error == true`|API/infra errors (not observed in tests)|
|2|`duration_api_ms == 0`|Request never reached API|
|3|Parse `result` text|Agent refusal or inability|
|4|Check expected file existence|Agent claimed success but didn't deliver|

**Test C4**: Impossible task → exit 0, `is_error: false`, `subtype: "success"`. The agent created `result.txt` anyway with an explanation. Same behavior as Claude Code.

---

## 7. Context File

Cursor Agent reads `.cursor/rules/*.mdc` files from the workspace. **Tested (C5)**: `.cursor/rules/canary.mdc` with `alwaysApply: true` was correctly read and the canary string appeared in the response.

### MDC format

```markdown
---
description: Rule description
alwaysApply: true
---
Rule content here.
```

### Does it read CLAUDE.md?

Not tested. The `.cursor/rules/` mechanism is the primary context injection path. For Gump, the safest approach is to generate a `.cursor/rules/gump.mdc` file with the agent instructions (same content as CLAUDE.md but in MDC format).

---

## 8. Model Mapping

Default model: `claude-4.6-opus-high-thinking`.

### Observed models (from --list-models)

|Category|Model IDs|
|---|---|
|Claude Opus 4.6|`claude-4.6-opus-high`, `claude-4.6-opus-high-thinking`, `claude-4.6-opus-max`, `claude-4.6-opus-max-thinking`|
|Claude Sonnet 4.6|`claude-4.6-sonnet-medium`, `claude-4.6-sonnet-medium-thinking`|
|Claude Opus 4.5|`claude-4.5-opus-high`, `claude-4.5-opus-high-thinking`|
|Claude Sonnet 4.5|`claude-4.5-sonnet`, `claude-4.5-sonnet-thinking`|
|Claude Sonnet 4|`claude-4-sonnet`, `claude-4-sonnet-1m`, `claude-4-sonnet-thinking`, `claude-4-sonnet-1m-thinking`|
|GPT-5.4|`gpt-5.4-low`, `gpt-5.4-medium`, `gpt-5.4-high`, `gpt-5.4-xhigh` + `-fast` variants|
|GPT-5.4 Mini/Nano|`gpt-5.4-mini-*`, `gpt-5.4-nano-*`|
|GPT-5.3 Codex|`gpt-5.3-codex`, `gpt-5.3-codex-low`, `gpt-5.3-codex-high`, `gpt-5.3-codex-xhigh` + `-fast` variants|
|GPT-5.2|`gpt-5.2`, `gpt-5.2-low`, `gpt-5.2-high`, `gpt-5.2-xhigh` + `-fast` and `-codex` variants|
|GPT-5.1|`gpt-5.1`, `gpt-5.1-low`, `gpt-5.1-high`, `gpt-5.1-codex-*`|
|Gemini|`gemini-3.1-pro`, `gemini-3-pro`, `gemini-3-flash`|
|Grok|`grok-4-20`, `grok-4-20-thinking`|
|Kimi|`kimi-k2.5`|
|Composer|`composer-2-fast`, `composer-2`, `composer-1.5`|
|Auto|`auto`|

**Model tested**: `claude-4.6-sonnet-medium` (C1-C5, C7), `gpt-5.4-medium` (C6). Both work.

---

## 9. Mapping RunResult for Gump

```go
// Extraction from the JSON result object
func parseCursorResult(resultObj map[string]interface{}) *RunResult {
    usage := getMap(resultObj, "usage")
    return &RunResult{
        SessionID:           getString(resultObj, "session_id"),
        IsError:             getBool(resultObj, "is_error"),
        DurationMs:          getInt(resultObj, "duration_ms"),
        DurationAPI:         getInt(resultObj, "duration_api_ms"),
        NumTurns:            0,  // NOT available in Cursor output
        Result:              getString(resultObj, "result"),
        InputTokens:         getInt(usage, "inputTokens"),      // camelCase!
        OutputTokens:        getInt(usage, "outputTokens"),     // camelCase!
        CacheReadTokens:     getInt(usage, "cacheReadTokens"),  // camelCase!
        CacheCreationTokens: getInt(usage, "cacheWriteTokens"), // "cacheWrite" maps to cache_creation
        CostUSD:             0,  // NOT available in Cursor output
        ModelUsage:          map[string]ModelMetrics{},         // NOT available
    }
}
```

**Critical**: usage field names are **camelCase** (`inputTokens`), not snake_case (`input_tokens`). This is different from Claude Code.

---

## 10. Streaming Event Mapping for TurnTracker

|Cursor Event|TurnTracker mapping|Display|
|---|---|---|
|`system/init`|Ignore|Not displayed|
|`user`|Ignore|Not displayed|
|`assistant` (text)|Start new turn OR continue current|Label from action mix|
|`tool_call/started`|Add action to current turn|`Write <path>` or `Bash <cmd>`|
|`tool_call/completed`|Update action result|Not displayed separately (merged with started)|
|`result`|End session|Summary line|

### Turn detection

New turn when an `assistant` event arrives after a `tool_call/completed` or after the `user` event (first turn). The `model_call_id` field links assistant + tool_call events from the same LLM turn.

### Tool type to Action.Type mapping

|`tool_call` key|Action.Type|Target extraction|
|---|---|---|
|`editToolCall`|`Write`|`.args.path`|
|(other tool types TBD)|TBD|TBD|

Only `editToolCall` was observed in testing. Other tool types (shell, read, etc.) will need discovery via more complex tasks.

### Per-turn tokens

**Not available** in the stream. Cursor stream events don't carry per-turn usage. Only the final `result` has aggregated usage. For the TurnTracker, tokens per turn will be 0 for Cursor (same limitation as Gemini).

---

## 11. Known Issues / Gotchas

1. **No `--max-turns`**: Cursor Agent has no flag to limit turns. The guard `max_turns` in Gump must rely on TurnTracker counting, not on a CLI flag.
    
2. **No `--allowedTools`**: No way to restrict which tools the agent uses. `--yolo` approves everything. For `no_write` guard enforcement, Gump must parse `tool_call` events and kill the process.
    
3. **No cost reporting**: `total_cost_usd` is absent. Cost must be estimated from tokens externally.
    
4. **No num_turns**: Must be counted by Gump from stream events.
    
5. **camelCase usage**: Token fields use camelCase (`inputTokens`) not snake_case. The adapter must handle this.
    
6. **TUI blocks without `-p`**: `cursor agent` without `-p` launches an interactive TUI that blocks without a TTY. Always use `-p` for headless.
    
7. **`--trust` required in headless**: Without `--trust`, the agent may prompt for workspace trust and block.
    
8. **Tool type in stream is implicit**: The tool type is the key name inside `tool_call` (e.g., `editToolCall`), not an explicit `name` field.
    
9. **No `--verbose` needed**: Unlike Claude Code, stream-json works without `--verbose`.
    
10. **Model display name in init**: The `model` field in `system/init` is the display name ("Sonnet 4.6 1M"), not the model ID ("claude-4.6-sonnet-medium").
    
11. **Auto-install on first `cursor agent`**: The wrapper script auto-downloads `cursor-agent` if not present. In CI, pre-install with `curl -sS https://cursor.com/install | bash`.
    

---

## 12. Raw Test Data

### C1 — JSON output (success, file creation)

```json
{"type":"result","subtype":"success","is_error":false,"duration_ms":5328,"duration_api_ms":5328,"result":"Le fichier `hello.txt` a été créé dans `/tmp/gump-cursor-tests/` avec le contenu `hello world`.","session_id":"a9f4a1ce-971c-4a24-9ee9-3f8342293c93","request_id":"7b7cbe41-48aa-4ed6-9e1d-9c02d353d8bc","usage":{"inputTokens":4,"outputTokens":117,"cacheReadTokens":28154,"cacheWriteTokens":1197}}
```

Exit: 0. hello.txt created. Model: claude-4.6-sonnet-medium.

### C2 — stream-json (7 lines NDJSON)

```json
{"type":"system","subtype":"init","apiKeySource":"login","cwd":"/private/tmp/gump-cursor-tests","session_id":"644c7cde-a660-4224-a26b-0b0a7e279293","model":"Sonnet 4.6 1M","permissionMode":"default"}
{"type":"user","message":{"role":"user","content":[{"type":"text","text":"Create a file called stream-test.txt containing 'cursor stream works'. Then confirm."}]},"session_id":"644c7cde-a660-4224-a26b-0b0a7e279293"}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Je vais créer ce fichier maintenant."}]},"session_id":"644c7cde-a660-4224-a26b-0b0a7e279293","model_call_id":"53df2d2f-4fe4-4b7d-93d1-6203b9188087-0-m2qz","timestamp_ms":1774272397614}
{"type":"tool_call","subtype":"started","call_id":"toolu_bdrk_01EKXzaxy6vAqm6a3wL93cNY","tool_call":{"editToolCall":{"args":{"path":"/tmp/gump-cursor-tests/stream-test.txt","streamContent":"cursor stream works"}}},"model_call_id":"53df2d2f-4fe4-4b7d-93d1-6203b9188087-0-m2qz","session_id":"644c7cde-a660-4224-a26b-0b0a7e279293","timestamp_ms":1774272397615}
{"type":"tool_call","subtype":"completed","call_id":"toolu_bdrk_01EKXzaxy6vAqm6a3wL93cNY","tool_call":{"editToolCall":{"args":{"path":"/tmp/gump-cursor-tests/stream-test.txt","streamContent":"cursor stream works"},"result":{"success":{"path":"/tmp/gump-cursor-tests/stream-test.txt","linesAdded":1,"linesRemoved":1,"diffString":"-\n+cursor stream works","afterFullFileContent":"cursor stream works","message":"Wrote contents to /tmp/gump-cursor-tests/stream-test.txt"}}}},"model_call_id":"53df2d2f-4fe4-4b7d-93d1-6203b9188087-0-m2qz","session_id":"644c7cde-a660-4224-a26b-0b0a7e279293","timestamp_ms":1774272397722}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"Le fichier `stream-test.txt` a été créé avec le contenu `cursor stream works`."}]},"session_id":"644c7cde-a660-4224-a26b-0b0a7e279293"}
{"type":"result","subtype":"success","duration_ms":5544,"duration_api_ms":5544,"is_error":false,"result":"Je vais créer ce fichier maintenant.Le fichier `stream-test.txt` a été créé avec le contenu `cursor stream works`.","session_id":"644c7cde-a660-4224-a26b-0b0a7e279293","request_id":"53df2d2f-4fe4-4b7d-93d1-6203b9188087","usage":{"inputTokens":4,"outputTokens":122,"cacheReadTokens":13535,"cacheWriteTokens":15844}}
```

### C3 — Resume (session-id preserved)

Step 1 session_id: `87109ddc-c0f9-43bd-a009-8fda0585ec0a` Step 2 session_id: `87109ddc-c0f9-43bd-a009-8fda0585ec0a` ← **identical** Both step1.txt and step2.txt created successfully.

### C4 — Error handling (impossible task)

Exit: 0. `is_error: false`. `subtype: "success"`. Agent created result.txt with explanation.

### C5 — Context file (.cursor/rules)

`.cursor/rules/canary.mdc` with `alwaysApply: true` → CURSOR-CANARY-42 appeared in response. ✅

### C6 — Model override (GPT-5.4)

`--model gpt-5.4-medium` → Exit 0, model-test.txt created. `inputTokens: 11543` (higher than Claude, likely larger system prompt).

### C7 — Mode plan (read-only)

`--mode plan` → Exit 0, detailed analysis of repository structure. No files modified.

---

## 13. Delta: Cursor Agent vs Claude Code

|Aspect|Claude Code|Cursor Agent|Impact for Gump|
|---|---|---|---|
|Binary|`claude`|`cursor-agent` (via `cursor agent`)|Different binary name in adapter|
|`--verbose` for stream-json|Required|Not required|Simpler command construction|
|`--workspace` flag|Not available|Available ✅|Use instead of cmd.Dir (belt+suspenders)|
|`--trust`|Not available|Required in headless|Must always add `--trust`|
|`--max-turns`|Available|Not available|Guard must count turns itself|
|`--allowedTools`|Available|Not available|Guard must monitor tool_call events|
|`total_cost_usd`|Available|Not available|CostUSD = 0, estimate externally|
|`num_turns`|Available|Not available|Count from stream events|
|Usage field names|snake_case|camelCase|Adapter must handle both|
|Tool events|`assistant` + `user` pair|`tool_call/started` + `tool_call/completed`|Different stream parsing|
|Tool name|Explicit (`Write`, `Read`, `Bash`)|Implicit (key name: `editToolCall`)|Different tool type extraction|
|Context file|CLAUDE.md|`.cursor/rules/*.mdc`|Generate MDC file, preserve existing rules|
|`modelUsage` breakdown|Available|Not available|No per-model metrics|
|`permission_denials`|Available|Not available|Cannot track denied permissions|