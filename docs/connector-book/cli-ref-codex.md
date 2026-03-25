# Codex CLI — Headless Mode Reference

> **Version tested**: Codex CLI **v0.101.0** **Date**: 2026-02-28 **Auth used for tests**: ChatGPT OAuth **OS**: macOS **Source**: Official doc (developers.openai.com/codex/cli + /codex/noninteractive) + real test results

---

## 1. CLI Help / Invocation

### Basic headless call

```bash
codex exec "<prompt>"
```

### Full production call

```bash
codex exec "<prompt>" \
  --json \                           # JSONL stream on stdout (default: text on stdout, progress on stderr)
  --full-auto \                      # --ask-for-approval on-request + --sandbox workspace-write
  -C /path/to/worktree \             # working directory (--cd)
  -m gpt-5.3-codex \                 # model override
  -o result.txt                      # write final message to file
```

### Working directory

**`--cd` / `-C` flag exists** (unlike Claude Code):

```bash
codex exec -C /path/to/worktree "<prompt>"
```

**Tested**: Codex respects `-C` — file created in target directory (test 5).

### Resume a session

```bash
# Resume most recent session
codex exec resume --last "<follow-up prompt>"

# Resume by thread ID
codex exec resume <thread-id> "<follow-up prompt>"

# Both support --json
codex exec resume --last --json --full-auto "<prompt>"
```

- Thread-id is **identical** across resume calls (**tested**: test 3)
- Full conversation context is preserved
- Files created in first session are visible in resumed session

### Permissions / Sandbox

```bash
--full-auto                          # on-request approval + workspace-write sandbox (recommended)
--sandbox read-only                  # default for codex exec
--sandbox workspace-write            # allow file edits in workdir + /tmp
--sandbox danger-full-access         # unrestricted (use in containers only)
--ask-for-approval never             # skip all approvals
--dangerously-bypass-approvals-and-sandbox  # alias: --yolo
```

No granular `--allowedTools` like Claude Code. Sandbox policy controls what the agent can do.

### Context file

Codex reads `AGENTS.md` from the working directory at startup (equivalent to Claude Code's `CLAUDE.md`).

### Timeout

No internal timeout flag. Use external `timeout` command:

```bash
timeout 120 codex exec --full-auto "<prompt>"
```

### Auth in CI

```bash
CODEX_API_KEY=<key> codex exec --json "<prompt>"
```

`CODEX_API_KEY` is only supported in `codex exec` mode.

---

## 2. Output Format: Text (default)

By default, `codex exec` streams progress to **stderr** and prints only the **final agent message** to **stdout**.

```bash
codex exec --full-auto "create hello.txt with hello world"
```

**stdout**:

```
Created `hello.txt` at `/private/tmp/gump-codex-tests` with content:

`hello world`

Verified by listing the file and reading it back.
```

**stderr** (progress, not machine-readable):

```
OpenAI Codex v0.101.0 (research preview)
--------
workdir: /private/tmp/gump-codex-tests
model: gpt-5.3-codex
provider: openai
approval: never
sandbox: workspace-write [workdir, /tmp, $TMPDIR]
reasoning effort: none
reasoning summaries: auto
session id: 019ca529-2265-7c80-a4ab-cca366060f4a
--------
user
Create a file called hello.txt containing 'hello world'. Then confirm what you did.
...
tokens used
1 382
```

### Key observations

- Exit code is **always 0** — even on impossible tasks (test 4)
- Stderr contains the `session id` (useful for debugging but not for parsing)
- Final message on stdout is plain text, not structured
- Use `--json` for machine-readable output

---

## 3. Output Format: JSONL (--json)

```bash
codex exec --json --full-auto "<prompt>"
```

One JSON object per line on stdout. **8 event types observed**:

### Event Type 1: `thread.started` (always first line)

```jsonc
{
  "type": "thread.started",
  "thread_id": "019ca529-3d5a-7a53-9b2a-db26bbaee746"   // UUID — this is the session ID for resume
}
```

### Event Type 2: `turn.started`

```jsonc
{
  "type": "turn.started"
}
```

### Event Type 3: `item.completed` — reasoning

```jsonc
{
  "type": "item.completed",
  "item": {
    "id": "item_0",
    "type": "reasoning",
    "text": "**Implementing file creation with commentary**"
  }
}
```

### Event Type 4: `item.completed` — agent_message (intermediate)

```jsonc
{
  "type": "item.completed",
  "item": {
    "id": "item_1",
    "type": "agent_message",
    "text": "Creating `stream-test.txt` in the workspace with the exact requested content, then I'll verify it was written correctly."
  }
}
```

### Event Type 5: `item.started` — command_execution

```jsonc
{
  "type": "item.started",
  "item": {
    "id": "item_2",
    "type": "command_execution",
    "command": "/bin/zsh -lc \"printf 'codex stream works' > stream-test.txt && cat stream-test.txt\"",
    "aggregated_output": "",
    "exit_code": null,
    "status": "in_progress"
  }
}
```

### Event Type 6: `item.completed` — command_execution

```jsonc
{
  "type": "item.completed",
  "item": {
    "id": "item_2",
    "type": "command_execution",
    "command": "/bin/zsh -lc \"printf 'codex stream works' > stream-test.txt && cat stream-test.txt\"",
    "aggregated_output": "codex stream works",
    "exit_code": 0,
    "status": "completed"
  }
}
```

### Event Type 7: `item.completed` — agent_message (final answer)

```jsonc
{
  "type": "item.completed",
  "item": {
    "id": "item_3",
    "type": "agent_message",
    "text": "Created `stream-test.txt` with the content:\n\n`codex stream works`"
  }
}
```

### Event Type 8: `turn.completed` (usage/tokens)

```jsonc
{
  "type": "turn.completed",
  "usage": {
    "input_tokens": 15931,
    "cached_input_tokens": 14080,
    "output_tokens": 175
  }
}
```

### Additional item types (from documentation, not observed in tests)

- `file_change` — file modifications (item.completed only)
- `web_search` — web search queries
- `todo_list` — plan/task list (supports item.updated)
- `mcp_tool_call` — MCP tool invocations
- `error` — item-level errors

### Stream sequence for a typical file-creation task

```
Line 1: thread.started          → thread_id (session ID)
Line 2: turn.started            → turn begins
Line 3: item.completed          → reasoning summary
Line 4: item.completed          → agent_message (intermediate)
Line 5: item.started            → command_execution (in_progress)
Line 6: item.completed          → command_execution (completed, exit_code, output)
Line 7: item.completed          → agent_message (final answer)
Line 8: turn.completed          → usage (input_tokens, cached_input_tokens, output_tokens)
```

---

## 4. Resume Behavior

```bash
# Step 1: capture thread_id
THREAD_ID=$(codex exec --json --full-auto "create step1.txt" | \
  jq -r 'select(.type=="thread.started") | .thread_id')

# Step 2: resume by ID
codex exec resume "$THREAD_ID" --json --full-auto "now create step2.txt"

# Or resume most recent
codex exec resume --last --json --full-auto "now create step2.txt"
```

**Confirmed behaviors (test 3):**

- Thread-id is **identical** across all calls: `019ca529-5319-78f2-bcdc-240240a190e5`
- Both `resume --last` and `resume <ID>` work in headless mode
- Full conversation context is preserved (agent remembers previous files)
- step1.txt, step2.txt, and step3.txt all created successfully

---

## 5. Error Detection Strategy

Exit code is **always 0**. No `is_error` field. Error detection must be inferred:

|Layer|Check|Detects|
|---|---|---|
|1|`turn.failed` event|API/infra errors, turn-level failures|
|2|`type: "error"` event|Stream-level fatal errors|
|3|`item.completed` with `item.type: "error"`|Item-level errors|
|4|Last `agent_message` text|Agent refusal or inability ("that is impossible")|
|5|Check expected file existence|Agent claimed success but didn't deliver|

**Test 4 result**: Impossible task → exit 0, no `turn.failed`, final `agent_message` explains why it's impossible. Same behavior as Claude Code — no structured error for agent-level failures.

---

## 6. Models and Gump Aliases

### Available models (-m flag)

|Model ID|Description|
|---|---|
|`gpt-5.4`|GPT-5.4 — flagship frontier, coding + reasoning + tool use (recommandé)|
|`gpt-5.4-mini`|GPT-5.4 Mini — fast, efficient, pour subagents (30% du coût de 5.4)|
|`gpt-5.3-codex`|GPT-5.3 Codex — industry-leading coding (défaut actuel en exec)|
|`gpt-5.3-codex-spark`|GPT-5.3 Codex Spark — near-instant real-time (Pro only)|
|`gpt-5.2-codex`|GPT-5.2 Codex — long-horizon, compaction native|
|`gpt-5.2`|GPT-5.2 — general purpose|
|`gpt-5.1-codex-max`|GPT-5.1 Codex Max — long-running multi-context|
|`gpt-5`|GPT-5 — base frontier|
|`gpt-5-mini`|GPT-5 Mini — lightweight|
|`o3-codex`|O3 Codex — reasoning-focused|

Codex accepte aussi n'importe quel model ID compatible Chat Completions / Responses API via `-m`.

### Gump alias → model flag

|Alias Gump|`-m` flag|Description|
|---|---|---|
|`codex`|(omis — défaut gpt-5.3-codex)|GPT-5.3 Codex (défaut courant)|
|`codex-gpt54`|`gpt-5.4`|GPT-5.4 flagship|
|`codex-gpt54-mini`|`gpt-5.4-mini`|GPT-5.4 Mini|
|`codex-gpt53`|`gpt-5.3-codex`|GPT-5.3 Codex|
|`codex-gpt53-spark`|`gpt-5.3-codex-spark`|GPT-5.3 Codex Spark|
|`codex-gpt52`|`gpt-5.2-codex`|GPT-5.2 Codex|
|`codex-gpt51-max`|`gpt-5.1-codex-max`|GPT-5.1 Codex Max|
|`codex-o3`|`o3-codex`|O3 Codex|

Fallback : si l'alias ne matche pas la table, extraire la partie après `codex-` et la passer tel quel à `-m`.

---

## 7. Multi-Model Behavior

Stderr shows the active model (`gpt-5.3-codex`). The JSONL stream does NOT break down per-model usage — `turn.completed` provides aggregated tokens only.

From stderr (test 1):

```
model: gpt-5.3-codex
```

**No `modelUsage` equivalent** in the JSONL output. If Codex uses sub-models internally, it's not visible in the stream.

**No `cost_usd` field** — only token counts. Cost must be calculated externally.

---

## 8. AGENTS.md Integration

Codex reads `AGENTS.md` from the working directory at startup. This is the primary vector for injecting:

- Project context
- Coding conventions
- File modification policies
- Step-specific instructions

No flag needed — just place the file in the cwd. Can also use:

```bash
--add-dir <path>    # grant additional directories write access
```

---

## 9. Known Issues / Gotchas

1. **Exit code always 0**: Even on agent-level failures. Must parse JSONL events for error detection.
    
2. **No cost_usd**: Only token counts in `turn.completed`. Calculate cost externally based on model pricing.
    
3. **No granular tool control**: Unlike Claude Code's `--allowedTools`, Codex uses sandbox policies (`read-only`, `workspace-write`, `danger-full-access`).
    
4. **Sandbox required**: By default, `codex exec` runs in read-only sandbox. Use `--full-auto` or `--sandbox workspace-write` for file creation tasks.
    
5. **Git repo required**: Codex requires a Git repository. Override with `--skip-git-repo-check` if needed.
    
6. **Timeout**: No internal timeout flag. Use external `timeout` command.
    
7. **CODEX_API_KEY only in exec**: The `CODEX_API_KEY` env var is only supported in `codex exec` mode.
    
8. **Stderr contains session info**: The `session id` in stderr is the same as `thread_id` in the JSONL stream.
    

---

## 10. Delta: Official Doc vs Observed Reality

|Aspect|Official Doc|Observed (v0.101.0)|Impact|
|---|---|---|---|
|Exit code on error|Not documented|Always 0 (even impossible tasks)|Must parse JSONL for errors|
|`turn.failed`|Documented as event type|Not observed in impossible task test|Agent-level failures are NOT turn failures|
|Model in JSONL|Not explicitly documented|Only in stderr, not in JSONL events|Cannot extract model from stream|
|`cached_input_tokens`|Not documented separately|Present in `turn.completed.usage`|Good for cost tracking|
|Default sandbox|Documented as read-only|Confirmed read-only|Must use `--full-auto` for file tasks|
|`-C` flag|Documented as `--cd`|Works correctly|Unlike Claude Code, has native cwd flag|
|Resume in exec|Documented|Works with both `--last` and `<ID>`|Thread-id preserved across resumes|

---

## 11. Raw Test Data

### Test 1 — Text output (success, file creation)

Exit code: 0. hello.txt created. Model: gpt-5.3-codex. 1,382 tokens.

### Test 2 — JSONL stream (8 lines)

```json
{"type":"thread.started","thread_id":"019ca529-3d5a-7a53-9b2a-db26bbaee746"}
{"type":"turn.started"}
{"type":"item.completed","item":{"id":"item_0","type":"reasoning","text":"**Implementing file creation with commentary**"}}
{"type":"item.completed","item":{"id":"item_1","type":"agent_message","text":"Creating `stream-test.txt` in the workspace with the exact requested content, then I'll verify it was written correctly."}}
{"type":"item.started","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc \"printf 'codex stream works' > stream-test.txt && cat stream-test.txt\"","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_2","type":"command_execution","command":"/bin/zsh -lc \"printf 'codex stream works' > stream-test.txt && cat stream-test.txt\"","aggregated_output":"codex stream works","exit_code":0,"status":"completed"}}
{"type":"item.completed","item":{"id":"item_3","type":"agent_message","text":"Created `stream-test.txt` with the content:\n\n`codex stream works`"}}
{"type":"turn.completed","usage":{"input_tokens":15931,"cached_input_tokens":14080,"output_tokens":175}}
```

### Test 3 — Resume (thread-id preserved)

Step 1 thread_id: `019ca529-5319-78f2-bcdc-240240a190e5` Step 2 thread_id (--last): `019ca529-5319-78f2-bcdc-240240a190e5` ← **identical** Step 3 thread_id (by ID): `019ca529-5319-78f2-bcdc-240240a190e5` ← **identical** All 3 step files created successfully.

### Test 4 — Error handling (impossible task)

Exit code: 0. No `turn.failed` event. Agent responded with mathematical explanation. No structured error for agent-level failures.

### Test 5 — Working directory (-C flag)

`-C /tmp/gump-codex-cwd-test` correctly set working directory. cwd-proof.txt created in target dir.