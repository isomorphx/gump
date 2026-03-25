# Claude Code CLI — Headless Mode Reference

> **Version tested**: Claude Code **v2.1.59** **Date**: 2026-02-26 **Auth used for tests**: OAuth (Max subscription) **OS**: macOS, Node v24.12.0 **Source**: Official doc (code.claude.com/docs/en/headless) + `claude -h` output + real test results

---

## 1. CLI Help Output (verbatim from v2.1.59)

```
Usage: claude [options] [command] [prompt]

Arguments:
  prompt                           Your prompt

Options:
  -p, --print                      Print response and exit (non-interactive)
  --output-format <format>         "text" (default), "json", or "stream-json" (only with --print)
  --input-format <format>          "text" (default), or "stream-json" (only with --print)
  --include-partial-messages       Include partial message chunks (only with --print + stream-json)
  --verbose                        Override verbose mode setting from config
  -d, --debug [filter]             Debug mode with optional category filter (e.g., "api,hooks")
  --model <name>                   Model: sonnet, opus, haiku, or full name
  --fallback-model <name>          Fallback model when default overloaded (print mode)
  --permission-mode <mode>         Permission mode: plan, acceptEdits, etc.
  --dangerously-skip-permissions   Skip all permission prompts
  --allowedTools <tools>           Tools that execute without prompting
  --disallowedTools <tools>        Tools removed from context
  --tools <tools>                  Restrict built-in tools (use "" to disable all)
  --append-system-prompt <text>    Append to default system prompt
  --append-system-prompt-file <p>  Append file contents to system prompt
  --system-prompt <text>           Replace entire default system prompt
  --system-prompt-file <path>      Replace with file contents (print mode only)
  --max-turns <n>                  Limit conversation turns (default 10 in headless)
  --continue                       Continue most recent conversation
  --resume <session-id>            Resume specific session by ID
  --fork-session                   Create new session ID instead of reusing original
  --session-id <uuid>              Use specific session ID (must be valid UUID)
  --add-dir <paths>                Add additional working directories
  --json-schema <schema>           JSON Schema for structured output (with --output-format json)
  --remote <task>                  Create web session on claude.ai
  --teleport                       Resume web session in local terminal
  --from-pr <pr>                   Resume session linked to GitHub PR
```

---

## 2. Invocation Patterns

### Basic headless call

```bash
claude -p "<prompt>" --output-format json
```

### Full production call

```bash
claude -p "<prompt>" \
  --output-format json \
  --allowedTools "Bash,Read,Write,Edit" \
  --permission-mode acceptEdits \
  --max-turns 15 \
  --model sonnet \
  --append-system-prompt "<additional instructions>"
```

### Streaming with token-level granularity

```bash
claude -p "<prompt>" \
  --output-format stream-json \
  --verbose \
  --include-partial-messages
```

### Pipeline chaining (agent to agent)

```bash
claude -p "analyze code" --output-format stream-json --verbose | \
  claude -p "process results" --input-format stream-json --output-format stream-json --verbose | \
  claude -p "generate report" --input-format stream-json
```

### Working directory

**No --cwd flag exists.** Controlled via the shell's cwd:

```go
// In Go adapter:
cmd := exec.Command("claude", "-p", prompt, "--output-format", "json")
cmd.Dir = "/path/to/worktree"
```

**Tested**: Claude Code respects the process cwd (test 5).

### Resume a session

```bash
# Resume specific session
claude -p "<follow-up>" --resume <session-id> --output-format json

# Continue most recent session
claude -p "<follow-up>" --continue --output-format json
```

**Tested**: Session-id remains identical across resume calls (test 3).

### System prompt control

```bash
# Append to default system prompt (keeps Claude Code defaults)
--append-system-prompt "You must write tests for every function."

# Replace entire system prompt (loses Claude Code defaults)
--system-prompt "You are a code reviewer. Only review, never edit."

# From file
--append-system-prompt-file ./instructions.md
--system-prompt-file ./custom-prompt.md
```

### CLAUDE.md

Claude Code reads `CLAUDE.md` from the working directory at startup automatically. No flag needed. This is the primary vector for injecting project context, coding conventions, and file modification policies.

Important distinction (from official doc):

- `CLAUDE.md` is added as a **user message** following the default system prompt
- `--append-system-prompt` **appends to** the system prompt itself
- `--system-prompt` **replaces** the entire default system prompt (output styles also do this)

### Timeout

No internal timeout flag. Use external `timeout` command:

```bash
timeout 120 claude -p "..." --output-format json
```

---

## 3. Output Format: JSON (final)

Single JSON object on stdout. **Exit code is always 0** even on errors.

### Complete field reference (from test 1)

```jsonc
{
  "type": "result",
  "subtype": "success",           // "success" | "error"
  "is_error": false,              // true only on API/infra errors
  "duration_ms": 6052,            // wall clock time
  "duration_api_ms": 5995,        // time spent in API calls
  "num_turns": 2,                 // LLM turns (includes tool use rounds)
  "result": "Created `hello.txt` with \"hello world\".",  // final text
  "stop_reason": null,            // observed: null (see delta section)
  "session_id": "f9356e8d-87ad-4a05-9d46-80c1f1591878",  // UUID
  "total_cost_usd": 0.032543,    // total across all models
  "usage": {
    "input_tokens": 4,
    "cache_creation_input_tokens": 1694,
    "cache_read_input_tokens": 38821,
    "output_tokens": 101,
    "server_tool_use": {
      "web_search_requests": 0,
      "web_fetch_requests": 0
    },
    "service_tier": "standard",
    "cache_creation": {
      "ephemeral_1h_input_tokens": 1694,
      "ephemeral_5m_input_tokens": 0
    },
    "inference_geo": "",
    "iterations": [],
    "speed": "standard"
  },
  "modelUsage": {                 // per-model breakdown
    "claude-opus-4-6": {
      "inputTokens": 4,
      "outputTokens": 101,
      "cacheReadInputTokens": 38821,
      "cacheCreationInputTokens": 1694,
      "webSearchRequests": 0,
      "costUSD": 0.032543,
      "contextWindow": 200000,
      "maxOutputTokens": 32000
    }
    // Can include multiple models (see section 8)
  },
  "permission_denials": [],       // denied permission requests
  "uuid": "522bd2f6-..."         // unique execution identifier
}
```

### Structured output (--json-schema)

```bash
claude -p "extract function names" \
  --output-format json \
  --json-schema '{"type":"object","properties":{"functions":{"type":"array","items":{"type":"string"}}}}'
```

Response includes metadata with structured output in the `structured_output` field. (Not tested — from official doc.)

---

## 4. Output Format: stream-json (NDJSON)

**Requires**: `--verbose` flag mandatory with `-p` mode. Without it: exit 1 with error.

```bash
claude -p "<prompt>" --output-format stream-json --verbose
```

Optional: `--include-partial-messages` for token-level streaming chunks.

One JSON object per line on stdout. **6 message types observed:**

### Type 1: `system/init` (always first line)

```jsonc
{
  "type": "system",
  "subtype": "init",
  "cwd": "/private/tmp/gump-cli-tests",
  "session_id": "8fc99cf0-...",
  "tools": ["Task", "TaskOutput", "Bash", "Glob", "Grep", "ExitPlanMode",
            "Read", "Edit", "Write", "NotebookEdit", "WebFetch", "TodoWrite",
            "WebSearch", "TaskStop", "AskUserQuestion", "Skill",
            "EnterPlanMode", "EnterWorktree", "ToolSearch"],
  "mcp_servers": [
    {"name": "claude.ai Google Calendar", "status": "needs-auth"},
    {"name": "claude.ai Gmail", "status": "needs-auth"}
  ],
  "model": "claude-opus-4-6",
  "permissionMode": "acceptEdits",
  "claude_code_version": "2.1.59",
  "output_style": "default",
  "agents": ["general-purpose", "statusline-setup", "Explore", "Plan"],
  "skills": ["keybindings-help", "debug", "claude-developer-platform"],
  "plugins": [],
  "fast_mode_state": "off",
  "apiKeySource": "none",        // or "environment" for API key auth
  "slash_commands": ["keybindings-help", "debug", ...],
  "uuid": "328a985a-..."
}
```

### Type 2: `assistant` (LLM response — tool use)

```jsonc
{
  "type": "assistant",
  "message": {
    "model": "claude-opus-4-6",
    "id": "msg_01Jpnvv...",
    "role": "assistant",
    "content": [
      {
        "type": "tool_use",
        "id": "toolu_01Jfbi...",
        "name": "Write",
        "input": {
          "file_path": "/private/tmp/gump-cli-tests/stream-test.txt",
          "content": "stream works\n"
        },
        "caller": {"type": "direct"}
      }
    ],
    "usage": {                    // per-turn usage
      "input_tokens": 3,
      "cache_creation_input_tokens": 1649,
      "cache_read_input_tokens": 18624,
      "output_tokens": 26,
      "service_tier": "standard",
      "inference_geo": "not_available"
    },
    "stop_reason": null,
    "context_management": null
  },
  "parent_tool_use_id": null,
  "session_id": "8fc99cf0-...",
  "uuid": "cef7a33f-..."
}
```

### Type 3: `assistant` (LLM response — text, final answer)

```jsonc
{
  "type": "assistant",
  "message": {
    "content": [
      {
        "type": "text",
        "text": "Created `stream-test.txt` with the content \"stream works\"."
      }
    ],
    "usage": { /* per-turn */ }
  }
}
```

### Type 4: `user` (tool results)

```jsonc
{
  "type": "user",
  "message": {
    "role": "user",
    "content": [
      {
        "tool_use_id": "toolu_01Jfbi...",
        "type": "tool_result",
        "content": "File created successfully at: /private/tmp/gump-cli-tests/stream-test.txt"
      }
    ]
  },
  "tool_use_result": {           // structured diff info
    "type": "create",            // "create" | "edit" | ...
    "filePath": "/private/tmp/gump-cli-tests/stream-test.txt",
    "content": "stream works\n",
    "structuredPatch": [],
    "originalFile": null
  },
  "session_id": "8fc99cf0-...",
  "uuid": "e64c3b7c-..."
}
```

### Type 5: `rate_limit_event`

```jsonc
{
  "type": "rate_limit_event",
  "rate_limit_info": {
    "status": "allowed",
    "resetsAt": 1772139600,      // unix timestamp
    "rateLimitType": "five_hour",
    "overageStatus": "rejected",
    "isUsingOverage": false
  },
  "session_id": "8fc99cf0-...",
  "uuid": "7ab62505-..."
}
```

### Type 6: `result` (final, same as JSON mode)

Same schema as the JSON final output (section 3). Always the **last line**.

### Typical sequence for a file-creation task

```
Line 1: system/init        -> session metadata
Line 2: assistant           -> tool_use (Write file)
Line 3: user                -> tool_result (success)
Line 4: rate_limit_event    -> rate limit check
Line 5: assistant           -> text (summary message)
Line 6: result              -> final aggregated result
```

### Token-level streaming (--include-partial-messages)

Not tested. Per official doc, adds `stream_event` messages with `delta.type == "text_delta"` for real-time token display:

```bash
claude -p "Write a poem" --output-format stream-json --verbose --include-partial-messages | \
  jq -rj 'select(.type == "stream_event" and .event.delta.type? == "text_delta") | .event.delta.text'
```

---

## 5. Resume Behavior

```bash
SESSION=$(claude -p "create step1.txt" --output-format json | jq -r '.session_id')
claude -p "now create step2.txt" --resume "$SESSION" --output-format json
```

**Tested (test 3):**

- Session-id is **identical** in both calls (`9f2e5187-...`)
- Full conversation context is preserved
- Files created in first session are visible in resumed session
- Works with both `json` and `stream-json` output formats
- `--continue` resumes most recent session (no session-id needed)
- `--fork-session` creates new session-id instead of reusing original (not tested)

---

## 6. Error Detection

Exit code is **always 0**. Error detection must be multi-layered:

|Layer|Check|Detects|
|---|---|---|
|1|`is_error == true`|API errors, auth failures, infra issues|
|2|`duration_api_ms == 0`|Request never reached API|
|3|`num_turns == 0` or very low|Agent didn't engage|
|4|Parse `result` text|Agent refusal or inability|
|5|Check expected file existence|Agent claimed success but didn't deliver|

**Tested (test 4):** An impossible task returns `is_error: false`, `subtype: "success"`, exit 0. The agent simply responds with text explaining it can't do it. No structured error mechanism for agent-level failures.

---

## 7. Permission Modes

```bash
--permission-mode plan              # Requires approval for actions
--permission-mode acceptEdits       # Auto-approve file edits, prompt for bash
--dangerously-skip-permissions      # Skip ALL prompts (use in isolated envs only)
```

Tool permissions:

```bash
--allowedTools "Bash,Read,Write,Edit"           # Allow specific tools
--allowedTools "Bash(git log:*)" "Bash(git status:*)"  # Prefix matching
--disallowedTools "Bash(rm:*)" "Bash(sudo:*)"   # Block dangerous ops
--tools "Bash,Read,Write,Edit"                   # Restrict to ONLY these tools
```

**Note**: Trailing `*` enables prefix matching. `Bash(git diff *)` allows any command starting with `git diff`. The space before `*` matters.

---

## 8. Models and Gump Aliases

### Available models (--model flag)

|Model ID|Description|
|---|---|
|`opus`|Claude Opus 4.6 (défaut Max/Team/Enterprise, 1M context)|
|`sonnet`|Claude Sonnet 4.6 (1M context)|
|`haiku`|Claude Haiku 4.5|
|`claude-opus-4-6`|Claude Opus 4.6 (nom complet)|
|`claude-sonnet-4-6`|Claude Sonnet 4.6 (nom complet)|
|`claude-haiku-4-5-20251001`|Claude Haiku 4.5 (nom complet daté)|
|`opusplan`|Hybrid : Opus pour le planning, Sonnet pour l'exécution|

Claude Code accepte aussi `ANTHROPIC_MODEL` env var et les modèles Bedrock/Vertex ARN via `modelOverrides` dans `settings.json`. Les effort levels (low/medium/high) sont contrôlés via `/effort` ou `CLAUDE_CODE_EFFORT`.

### Gump alias → model flag

|Alias Gump (dans le workflow)|`--model` flag|Description|
|---|---|---|
|`claude`|(omis — défaut provider)|Opus 4.6 (défaut courant)|
|`claude-opus`|`opus`|Opus 4.6|
|`claude-sonnet`|`sonnet`|Sonnet 4.6|
|`claude-haiku`|`haiku`|Haiku 4.5|
|`claude-opusplan`|`opusplan`|Hybrid Opus/Sonnet|
|`claude-opus-4-6`|`claude-opus-4-6`|Opus 4.6 (nom complet)|
|`claude-sonnet-4-6`|`claude-sonnet-4-6`|Sonnet 4.6 (nom complet)|
|`claude-haiku-4-5`|`claude-haiku-4-5-20251001`|Haiku 4.5 (nom complet)|

Fallback : si l'alias ne matche pas la table, extraire la partie après `claude-` et la passer tel quel à `--model`.

---

## 9. Multi-Model Behavior

Claude Code can use **multiple models** in a single session. Observed in test 5:

```jsonc
"modelUsage": {
  "claude-opus-4-6": {
    "inputTokens": 5,
    "outputTokens": 199,
    "costUSD": 0.04589875,
    "contextWindow": 200000,
    "maxOutputTokens": 32000
  },
  "claude-haiku-4-5-20251001": {
    "inputTokens": 319,
    "outputTokens": 32,
    "costUSD": 0.000479,
    "contextWindow": 200000,
    "maxOutputTokens": 32000
  }
}
```

Haiku is used internally for lightweight sub-tasks (routing, classification). This is automatic and not user-controllable.

`--fallback-model` is a separate feature: used when the primary model is overloaded.

---

## 10. Available Tools (from init message)

Full list observed in stream-json init (v2.1.59):

```
Task, TaskOutput, Bash, Glob, Grep, ExitPlanMode, Read, Edit, Write,
NotebookEdit, WebFetch, TodoWrite, WebSearch, TaskStop, AskUserQuestion,
Skill, EnterPlanMode, EnterWorktree, ToolSearch
```

---

## 11. Delta: Official Doc vs Observed Reality

|Aspect|Official Doc|Observed (v2.1.59)|Impact|
|---|---|---|---|
|`stop_reason`|Implied "stop_sequence"|`null` in all tests|Don't rely on this field|
|stream-json + `-p`|Not explicitly stated|**Requires `--verbose`**, exit 1 without it|Must always add `--verbose`|
|Exit code on error|Not documented|Always 0, even with `is_error: true`|Must parse JSON for errors|
|`--cwd` flag|Not mentioned|Does not exist|Use `cmd.Dir` in Go|
|`rate_limit_event` in stream|Not documented|Appears between turns|Parse or ignore|
|`tool_use_result` in user msg|Not documented|Contains structured diff (type, filePath, content, structuredPatch)|Rich file change tracking|
|`modelUsage` multi-model|Not documented|Haiku used for internal sub-tasks alongside primary model|Cost tracking must iterate all models|
|`apiKeySource` in init|Not documented|`"none"` (OAuth) or `"environment"` (API key)|Useful for debugging auth|
|`fast_mode_state` in init|Not documented|`"off"` by default|Fast mode = 6x pricing|
|`agents` in init|Not documented|`["general-purpose", "statusline-setup", "Explore", "Plan"]`|Agent routing system|
|`--include-partial-messages`|Documented|Not tested|Token-level streaming|
|`--input-format stream-json`|Documented|Not tested|Pipeline chaining|
|`--json-schema`|Documented|Not tested|Structured output|
|`--fork-session`|Documented|Not tested|New session-id on resume|

---

## 12. Known Issues / Gotchas

1. **API key auth broken** (v2.1.42-v2.1.59): `ANTHROPIC_API_KEY` env var causes `ByteString` error with smart quote at index 108. Workaround: use OAuth login, `unset ANTHROPIC_API_KEY`.
    
2. **stream-json requires --verbose**: Undocumented requirement for `-p` mode. Without `--verbose`, exit 1 with error: `"Error: When using --print, --output-format=stream-json requires --verbose"`.
    
3. **stop_reason is null**: Doc implies "stop_sequence" but real output shows `null`. Don't rely on this field.
    
4. **Exit code always 0**: Even on `is_error: true`. Must parse JSON for error detection.
    
5. **No --cwd flag**: Must control via process working directory.
    
6. **Timeout**: No internal timeout flag. Use external `timeout` command.
    
7. **Workspace trust dialog**: Skipped in `-p` mode (headless). But `echo ... | claude` (without `-p`) triggers it.
    
8. **Slash commands unavailable**: User-invoked skills like `/commit` are only available in interactive mode. In `-p` mode, describe the task directly.
    

---

## 13. Raw Test Data

### Test 1 — JSON final (success)

```json
{"type":"result","subtype":"success","is_error":false,"duration_ms":6052,"duration_api_ms":5995,"num_turns":2,"result":"Created `hello.txt` with \"hello world\".","stop_reason":null,"session_id":"f9356e8d-87ad-4a05-9d46-80c1f1591878","total_cost_usd":0.032542999999999996,"usage":{"input_tokens":4,"cache_creation_input_tokens":1694,"cache_read_input_tokens":38821,"output_tokens":101,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":1694,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard"},"modelUsage":{"claude-opus-4-6":{"inputTokens":4,"outputTokens":101,"cacheReadInputTokens":38821,"cacheCreationInputTokens":1694,"webSearchRequests":0,"costUSD":0.032542999999999996,"contextWindow":200000,"maxOutputTokens":32000}},"permission_denials":[],"uuid":"522bd2f6-3230-474e-a1c7-bb8b3924b968"}
```

### Test 2 — stream-json (6 lines NDJSON)

```json
{"type":"system","subtype":"init","cwd":"/private/tmp/gump-cli-tests","session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3","tools":["Task","TaskOutput","Bash","Glob","Grep","ExitPlanMode","Read","Edit","Write","NotebookEdit","WebFetch","TodoWrite","WebSearch","TaskStop","AskUserQuestion","Skill","EnterPlanMode","EnterWorktree","ToolSearch"],"mcp_servers":[{"name":"claude.ai Google Calendar","status":"needs-auth"},{"name":"claude.ai Gmail","status":"needs-auth"}],"model":"claude-opus-4-6","permissionMode":"acceptEdits","slash_commands":["keybindings-help","debug","claude-developer-platform","compact","context","cost","init","pr-comments","release-notes","review","security-review","extra-usage","insights"],"apiKeySource":"none","claude_code_version":"2.1.59","output_style":"default","agents":["general-purpose","statusline-setup","Explore","Plan"],"skills":["keybindings-help","debug","claude-developer-platform"],"plugins":[],"uuid":"328a985a-4953-40d0-8748-b6320def2189","fast_mode_state":"off"}
{"type":"assistant","message":{"model":"claude-opus-4-6","id":"msg_01JpnvvB9GKY4JiE4JFhDWMa","type":"message","role":"assistant","content":[{"type":"tool_use","id":"toolu_01Jfbi5Nh713TYS9eYwVZ3zo","name":"Write","input":{"file_path":"/private/tmp/gump-cli-tests/stream-test.txt","content":"stream works\n"},"caller":{"type":"direct"}}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":3,"cache_creation_input_tokens":1649,"cache_read_input_tokens":18624,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":1649},"output_tokens":26,"service_tier":"standard","inference_geo":"not_available"},"context_management":null},"parent_tool_use_id":null,"session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3","uuid":"cef7a33f-88a5-4bf9-ba7a-b92a81d1d0a1"}
{"type":"user","message":{"role":"user","content":[{"tool_use_id":"toolu_01Jfbi5Nh713TYS9eYwVZ3zo","type":"tool_result","content":"File created successfully at: /private/tmp/gump-cli-tests/stream-test.txt"}]},"parent_tool_use_id":null,"session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3","uuid":"e64c3b7c-39c9-4508-b324-2e503b62580b","tool_use_result":{"type":"create","filePath":"/private/tmp/gump-cli-tests/stream-test.txt","content":"stream works\n","structuredPatch":[],"originalFile":null}}
{"type":"rate_limit_event","rate_limit_info":{"status":"allowed","resetsAt":1772139600,"rateLimitType":"five_hour","overageStatus":"rejected","overageDisabledReason":"org_level_disabled","isUsingOverage":false},"uuid":"7ab62505-ca36-45b2-9662-16987eeea053","session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3"}
{"type":"assistant","message":{"model":"claude-opus-4-6","id":"msg_01Ltydzk4uhfAppn5i3ymukj","type":"message","role":"assistant","content":[{"type":"text","text":"Created `stream-test.txt` with the content \"stream works\"."}],"stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":1,"cache_creation_input_tokens":125,"cache_read_input_tokens":20273,"cache_creation":{"ephemeral_5m_input_tokens":0,"ephemeral_1h_input_tokens":125},"output_tokens":1,"service_tier":"standard","inference_geo":"not_available"},"context_management":null},"parent_tool_use_id":null,"session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3","uuid":"669466f5-9a27-4124-8008-f533bddc45f3"}
{"type":"result","subtype":"success","is_error":false,"duration_ms":5469,"duration_api_ms":5423,"num_turns":2,"result":"Created `stream-test.txt` with the content \"stream works\".","stop_reason":null,"session_id":"8fc99cf0-8cae-4821-aa70-c0e9a24667c3","total_cost_usd":0.033231000000000004,"usage":{"input_tokens":4,"cache_creation_input_tokens":1774,"cache_read_input_tokens":38897,"output_tokens":107,"server_tool_use":{"web_search_requests":0,"web_fetch_requests":0},"service_tier":"standard","cache_creation":{"ephemeral_1h_input_tokens":1774,"ephemeral_5m_input_tokens":0},"inference_geo":"","iterations":[],"speed":"standard"},"modelUsage":{"claude-opus-4-6":{"inputTokens":4,"outputTokens":107,"cacheReadInputTokens":38897,"cacheCreationInputTokens":1774,"webSearchRequests":0,"costUSD":0.033231000000000004,"contextWindow":200000,"maxOutputTokens":32000}},"permission_denials":[],"uuid":"00a097be-a921-4f31-82fe-b78d9d70f28b"}
```

### Test 3 — Resume (session-id preserved)

- Step 1 session_id: `9f2e5187-7129-4379-b856-7edaf4577f3f`
- Step 2 session_id: `9f2e5187-7129-4379-b856-7edaf4577f3f` (identical)
- Both step1.txt and step2.txt created successfully

### Test 4 — Error handling

- Exit code: 0
- `is_error`: false
- `subtype`: "success"
- Agent responded: "You're right — that is impossible."
- No structured error for agent-level failures

### Test 5 — cwd + multi-model

- Working directory: `/private/tmp/gump-cwd-test2` (correct)
- Multi-model: opus (main) + haiku (sub-task)
- `num_turns`: 3 (extra turn from haiku sub-task)