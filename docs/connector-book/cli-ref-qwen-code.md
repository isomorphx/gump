# Qwen Code CLI — Headless Mode Reference

> **Version tested**: Qwen Code **v0.11.0** **Date**: 2026-03-02 **Auth used for tests**: API Key (Dashscope OpenAI-compatible) **OS**: macOS **Source**: Doc officielle + `qwen --help` v0.11.0 + tests reels **Parente**: Fork de Gemini CLI, adapte pour Qwen3-Coder

---

## 1. CLI Help Output (verbatim from v0.11.0)

```
Usage: qwen [options] [command]

Qwen Code - Launch an interactive CLI, use -p/--prompt for non-interactive mode

Commands:
  qwen [query..]             Launch Qwen Code CLI  [default]
  qwen mcp                   Manage MCP servers
  qwen extensions <command>  Manage Qwen Code extensions.

Positionals:
  query  Positional prompt. Defaults to one-shot; use -i/--prompt-interactive for interactive.

Options:
  -d, --debug                     Run in debug mode?  [boolean] [default: false]
  -m, --model                     Model  [string]
  -p, --prompt                    Prompt (deprecated: use positional instead)  [string]
  -i, --prompt-interactive        Execute prompt and continue interactive  [string]
  -s, --sandbox                   Run in sandbox?  [boolean]
  -y, --yolo                      Auto-accept all actions  [boolean] [default: false]
      --approval-mode             plan, default, auto-edit, yolo  [string]
      --chat-recording            Enable chat recording (needed for --continue/--resume)  [boolean]
      --session-id                Specify a session ID for this run  [string]
      --max-session-turns         Maximum number of session turns  [number]
      --allowed-tools             Tools to allow, will bypass confirmation  [array]
      --exclude-tools             Tools to exclude  [array]
      --core-tools                Core tool paths  [array]
      --include-directories       Additional directories  [array]
      --input-format              text, stream-json  [string] [default: "text"]
  -o, --output-format             text, json, stream-json  [string]
      --include-partial-messages  Include partial messages (with stream-json)  [boolean] [default: false]
  -c, --continue                  Resume most recent session  [boolean] [default: false]
  -r, --resume                    Resume specific session by ID  [string]
      --auth-type                 openai, anthropic, qwen-oauth, gemini, vertex-ai  [string]
      --channel                   VSCode, ACP, SDK, CI  [string]
      --acp                       Starts agent in ACP mode  [boolean]
      --experimental-lsp          Enable LSP  [boolean] [default: false]
  -e, --extensions                Extensions to use  [array]
  -l, --list-extensions           List extensions and exit  [boolean]
      --openai-logging            Enable OpenAI API logging  [boolean]
      --openai-logging-dir        Custom dir for API logs  [string]
      --openai-api-key            OpenAI API key  [string]
      --openai-base-url           OpenAI base URL  [string]
      --screen-reader             Enable screen reader mode  [boolean]
      --checkpointing             Enable checkpointing of file edits  [boolean] [default: false]
  -v, --version                   Show version number  [boolean]
  -h, --help                      Show help  [boolean]
```

---

## 2. Invocation Patterns

### Installation

```bash
npm install -g @qwen-code/qwen-code   # Node.js >= 20
```

### Auth (headless/CI)

```bash
export OPENAI_API_KEY="sk-xxxxx"
export OPENAI_BASE_URL="https://dashscope-intl.aliyuncs.com/compatible-mode/v1"
export OPENAI_MODEL="qwen3-coder-plus"
```

Ou via `--openai-api-key` et `--openai-base-url` flags directement. Ou via `~/.qwen/settings.json` (voir section 8).

### Basic headless call

```bash
qwen -p "Create hello.txt with hello world" --output-format json --yolo
```

### Full production call (Gump)

```bash
qwen -p "<prompt>" \
  --output-format stream-json \
  --yolo \
  --model <model> \
  --max-session-turns 25 \
  --allowed-tools "list_directory" "read_file" "grep_search" "glob" "edit" "write_file" "run_shell_command"
```

### Working directory

Pas de flag `--cwd`. Controle via le cwd du process :

```go
cmd := exec.Command("qwen", "-p", prompt, "--output-format", "stream-json", "--yolo")
cmd.Dir = "/path/to/worktree"
```

**Teste (test Q5)** : le cwd est respecte. Le champ `cwd` dans system/init confirme le directory.

### Resume

```bash
qwen --resume <session-id> -p "<follow-up>" --output-format json --yolo
qwen --continue -p "<follow-up>" --output-format json --yolo
```

**Teste (test Q3)** : session-id est preserve apres resume. `--resume` fonctionne en headless.

### Fichier de contexte

Qwen Code lit `QWEN.md` depuis le working directory au demarrage (confirme par test Q6).

### Timeout

Pas de flag timeout interne. Utiliser `context.WithTimeout` cote Go (SIGTERM + SIGKILL).

---

## 3. Output Format: JSON (buffered array)

**Difference vs Claude Code** : retourne un **JSON array** (pas un objet unique). Bufferise et emis en fin de session.

### Structure reelle complete (from test Q1)

```jsonc
[
  {
    "type": "system",
    "subtype": "init",                    // NOTE: "init" (pas "session_start" comme la doc dit)
    "uuid": "6548daf5-...",
    "session_id": "6548daf5-...",         // MEME que uuid dans system/init
    "cwd": "/path/to/workdir",
    "tools": ["task", "skill", "list_directory", "read_file", "grep_search",
              "glob", "edit", "write_file", "run_shell_command", "save_memory",
              "todo_write", "exit_plan_mode", "web_fetch", "web_search"],
    "mcp_servers": [],
    "model": "coder-model",              // NOTE: nom interne, pas le model ID reel
    "permission_mode": "yolo",
    "slash_commands": ["bug", "compress", "init", "summary"],
    "qwen_code_version": "0.11.0",
    "agents": ["general-purpose"]
  },
  {
    "type": "assistant",                  // thinking block
    "uuid": "cf5683f6-...",
    "session_id": "6548daf5-...",
    "parent_tool_use_id": null,
    "message": {
      "id": "cf5683f6-...",
      "type": "message",
      "role": "assistant",
      "model": "coder-model",
      "content": [
        {
          "type": "thinking",
          "thinking": "The user wants me to create a simple file...",
          "signature": ""
        }
      ],
      "stop_reason": null,
      "usage": {"input_tokens": 0, "output_tokens": 0}
    }
  },
  {
    "type": "assistant",                  // tool_use
    "uuid": "b1ffc112-...",
    "session_id": "6548daf5-...",
    "parent_tool_use_id": null,
    "message": {
      "id": "b1ffc112-...",
      "type": "message",
      "role": "assistant",
      "model": "coder-model",
      "content": [
        {
          "type": "tool_use",
          "id": "call_3488858d...",
          "name": "write_file",
          "input": {
            "file_path": "/path/to/hello.txt",
            "content": "hello world"
          }
        }
      ],
      "stop_reason": "tool_use",
      "usage": {
        "input_tokens": 13214,
        "output_tokens": 110,
        "cache_read_input_tokens": 13004,
        "total_tokens": 13324
      }
    }
  },
  {
    "type": "user",                       // tool_result
    "uuid": "31ab032e-...",
    "session_id": "6548daf5-...",
    "parent_tool_use_id": null,
    "message": {
      "role": "user",
      "content": [
        {
          "type": "tool_result",
          "tool_use_id": "call_3488858d...",
          "is_error": false,
          "content": "Successfully created and wrote to new file: /path/to/hello.txt."
        }
      ]
    }
  },
  {
    "type": "assistant",                  // text final
    "message": {
      "content": [{"type": "text", "text": "Done."}],
      "usage": {"input_tokens": 13384, "output_tokens": 30, "cache_read_input_tokens": 13208, "total_tokens": 13414}
    }
  },
  {
    "type": "result",
    "subtype": "success",
    "uuid": "0cb349fd-...",
    "session_id": "6548daf5-...",
    "is_error": false,
    "duration_ms": 7331,
    "duration_api_ms": 7274,
    "num_turns": 2,
    "result": "Done.",
    "usage": {
      "input_tokens": 26598,
      "output_tokens": 140,
      "cache_read_input_tokens": 26212,
      "total_tokens": 26738
    },
    "permission_denials": [],
    "stats": {
      "models": {
        "coder-model": {
          "api": {"totalRequests": 2, "totalErrors": 0, "totalLatencyMs": 7170},
          "tokens": {
            "prompt": 26598,
            "candidates": 140,
            "total": 26738,
            "cached": 26212,
            "thoughts": 54,
            "tool": 0
          }
        }
      },
      "tools": {
        "totalCalls": 1,
        "totalSuccess": 1,
        "totalFail": 0,
        "totalDurationMs": 2,
        "totalDecisions": {"accept": 0, "reject": 0, "modify": 0, "auto_accept": 1},
        "byName": {
          "write_file": {"count": 1, "success": 1, "fail": 0, "durationMs": 2}
        }
      },
      "files": {"totalLinesAdded": 1, "totalLinesRemoved": 0}
    }
  }
]
```

---

## 4. Output Format: stream-json (NDJSON)

**Pas besoin de `--verbose`** (contrairement a Claude Code). Fonctionne directement (teste Q2).

Memes types d'evenements que le mode JSON, mais un objet par ligne, emis en temps reel.

Sequence typique (test Q2, 7 lignes) :

```
Line 1: system/init        -> session metadata
Line 2: assistant           -> thinking
Line 3: assistant           -> tool_use (write_file)
Line 4: user                -> tool_result (success)
Line 5: assistant           -> thinking
Line 6: assistant           -> text (summary)
Line 7: result              -> final aggregated result
```

Le `result` final en stream-json ne contient PAS le champ `stats` (contrairement au mode JSON).

---

## 5. Resume Behavior

**Teste (test Q3)** :

- Step 1 session_id: `6548daf5-1bff-4e8e-b1cb-4a0561cac525`
- Step 2 session_id: `6548daf5-1bff-4e8e-b1cb-4a0561cac525` (IDENTIQUE)
- hello.txt et step2.txt crees avec succes
- Le `stats` dans le result accumule les metriques des deux steps (totalRequests: 4)

---

## 6. Error Detection

**Teste (test Q4)** :

- Exit code: **0** (meme comportement que Claude Code)
- `is_error`: **false**
- `subtype`: **"success"**
- L'agent repond en texte expliquant pourquoi c'est impossible
- `num_turns`: 1 (pas de tool use)

Conclusion : meme strategie que Claude Code. Ne JAMAIS utiliser l'exit code. Parser le JSON result.

---

## 7. Models and Gump Aliases

### Available models (-m flag)

|Model ID|Description|
|---|---|
|`qwen3-coder-plus`|Qwen3 Coder Plus (défaut)|
|`qwen3-coder`|Qwen3 Coder — base|

Qwen Code est un fork de Gemini CLI. La liste de modèles est limitée aux modèles Qwen exposés via la Dashscope API (OpenAI-compatible). Le flag `--auth-type` permet de router vers d'autres backends (anthropic, gemini, vertex-ai).

Le champ `model` dans `system/init` affiche toujours `"coder-model"` (nom interne) — pas le model ID réel passé via `-m`.

### Gump alias → model flag

|Alias Gump|`-m` flag|Description|
|---|---|---|
|`qwen`|(omis — défaut qwen3-coder-plus)|Qwen3 Coder Plus|
|`qwen-coder`|`qwen3-coder`|Qwen3 Coder base|
|`qwen-coder-plus`|`qwen3-coder-plus`|Qwen3 Coder Plus|

Fallback : si l'alias ne matche pas la table, extraire la partie après `qwen-` et la passer tel quel à `-m`.

---

## 8. Mapping RunResult pour Gump

```go
// Extraction depuis le dernier element type="result" du JSON array
func parseQwenResult(resultObj map[string]interface{}) *RunResult {
    return &RunResult{
        SessionID:           getString(resultObj, "session_id"),
        IsError:             getBool(resultObj, "is_error"),
        DurationMs:          getInt(resultObj, "duration_ms"),
        DurationAPI:         getInt(resultObj, "duration_api_ms"),
        NumTurns:            getInt(resultObj, "num_turns"),
        Result:              getString(resultObj, "result"),
        // Tokens depuis usage (agrege)
        InputTokens:         getNestedInt(resultObj, "usage", "input_tokens"),
        OutputTokens:        getNestedInt(resultObj, "usage", "output_tokens"),
        CacheReadTokens:     getNestedInt(resultObj, "usage", "cache_read_input_tokens"),
        CacheCreationTokens: 0,  // pas de champ cache_creation chez Qwen
        // Cost : PAS de champ cost dans le JSON Qwen. Calculer cote Gump.
        CostUSD:             0,
    }
}
```

**Differences cles vs Claude Code** :

- Pas de `total_cost_usd` dans l'output (calculer cote Gump via pricing API)
- Pas de `modelUsage` per-model breakdown (un seul modele "coder-model")
- Pas de `cache_creation_input_tokens` (seulement `cache_read_input_tokens`)
- Le champ model est toujours `"coder-model"` (nom interne, pas le model ID reel)
- `stats` present seulement en mode JSON, pas en stream-json

---

## 9. Flags confirmes par test

|Flag|Confirme|Notes|
|---|---|---|
|`-p` / `--prompt`|OUI|Mode headless. Deprecated, utiliser le positional|
|`--output-format json`|OUI|JSON array buffered|
|`--output-format stream-json`|OUI|NDJSON temps reel, PAS besoin de --verbose|
|`-m` / `--model`|OUI|Fonctionne (test Q7). Init montre "coder-model" comme nom interne|
|`--yolo`|OUI|Auto-approve tout|
|`--resume <id>`|OUI|Resume par session_id|
|`-c` / `--continue`|OUI (doc)|Non teste explicitement|
|`--max-session-turns`|OUI (help)|Equivalent de --max-turns|
|`--allowed-tools`|OUI (help)|Array de noms d'outils|
|`--exclude-tools`|OUI (help)|Array de noms d'outils|
|`--session-id`|OUI (help)|Specifier un session ID pour le run|
|`--sandbox`|OUI (help)|Docker sandbox|
|`--include-directories`|OUI (help)|Workspace additionnel|
|`--auth-type`|OUI (help)|openai, anthropic, qwen-oauth, gemini, vertex-ai|
|`--channel CI`|OUI (help)|Pour identifier les runs CI|

---

## 10. Outils disponibles (from system/init)

```
task, skill, list_directory, read_file, grep_search, glob, edit,
write_file, run_shell_command, save_memory, todo_write,
exit_plan_mode, web_fetch, web_search
```

Note : noms differents de Claude Code (ex: `write_file` vs `Write`, `run_shell_command` vs `Bash`).

---

## 11. Context File (QWEN.md)

**Teste (test Q6)** : QWEN.md est lu automatiquement. L'agent a repondu "PUDDING-CANARY-42" sans lire le fichier via un tool.

---

## 12. Delta: Documentation vs Reality

|Aspect|Documentation|Observe (v0.11.0)|Impact Gump|
|---|---|---|---|
|system subtype|"session_start"|"init"|Parser les deux|
|model dans init|model ID reel|"coder-model" (interne)|Pas fiable pour metriques|
|stream-json + verbose|Non mentionne|PAS besoin de --verbose|Plus simple que Claude|
|Exit code on error|Non documente|0 (meme que Claude)|Parser JSON obligatoire|
|cost/pricing|Non mentionne|ABSENT du JSON|Calculer cote Gump|
|cache_creation_tokens|Non documente|Absent (seulement cache_read)|Adapter le parsing|
|stats|Non documente|Present en JSON, absent en stream-json|Utiliser mode JSON pour stats|
|thinking blocks|Non documente|Present (type: "thinking")|Ignorer cote parsing|
|tool_use id format|Non documente|"call_xxx" (pas "toolu_xxx")|Format Qwen natif|
|stop_reason|Non documente|"tool_use" ou null|Utilisable|

---

## 13. Raw Test Data

### Test Q1 -- JSON final (success, file creation)

Exit code: 0. JSON array de 7 elements. Fichier hello.txt cree. session_id: 6548daf5-1bff-4e8e-b1cb-4a0561cac525 duration_ms: 7331, num_turns: 2, input_tokens: 26598, output_tokens: 140

### Test Q2 -- stream-json (NDJSON)

Exit code: 0. 7 lignes NDJSON. Pas besoin de --verbose. Fichier stream-test.txt cree. session_id: eaa7dce0-665d-4717-8478-93f705124683

### Test Q3 -- Resume

Session ID preserved: OUI (6548daf5-... identique). Stats cumulatifs: totalRequests: 4, totalCalls: 2.

### Test Q4 -- Error handling

Exit code: 0. is_error: false. subtype: "success". Agent repond en texte. num_turns: 1.

### Test Q5 -- cwd

cwd dans system/init: /Users/.../gump-cli-tests-20260302-204018 (correct, c'est le cwd du process). Fichier cree dans REPORT_DIR (pas CWD_DIR). cwd respecte.

### Test Q6 -- QWEN.md context

Agent repond "PUDDING-CANARY-42". QWEN.md lu automatiquement sans tool call.

### Test Q7 -- --model flag

Fonctionne. Le model dans init reste "coder-model" (nom interne).