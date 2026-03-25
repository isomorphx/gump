# OpenCode CLI — Headless Mode Reference

> **Version tested**: OpenCode **v1.2.15** **Date**: 2026-03-02 **Auth used for tests**: OpenAI API Key (modele par defaut) **OS**: macOS **Source**: Doc officielle (opencode.ai/docs/cli/) + `opencode run --help` v1.2.15 + tests reels **Tests**: 8/8 passes (O1, O3a-c, O4, O5, O6, O7, O8) **Parente**: Projet Go natif (pas un fork). BubbleTea TUI, SQLite storage.

---

## 1. CLI Help Output (verbatim from v1.2.15)

### `opencode run --help`

```
opencode run [message..]

run opencode with a message

Positionals:
  message  message to send                                    [array] [default: []]

Options:
  --command     the command to run, use message for args
  -c, --continue    continue the last session
  -s, --session     session id to continue
      --fork        fork the session before continuing
      --share       share the session
  -m, --model       model in format provider/model
      --agent       agent to use
      --format      default (formatted) or json (raw JSON events)  [default: "default"]
  -f, --file        file(s) to attach to message
      --title       title for the session
      --attach      attach to running opencode server
      --dir         directory to run in
      --port        port for the local server
      --variant     model variant (reasoning effort: high, max, minimal)
      --thinking    show thinking blocks                           [default: false]
```

### `opencode --help` (global)

```
Commands:
  opencode [project]           start opencode tui                               [default]
  opencode run [message..]     run opencode with a message
  opencode serve               starts a headless opencode server
  opencode attach <url>        attach to a running opencode server
  opencode auth                manage credentials
  opencode agent               manage agents
  opencode mcp                 manage MCP servers
  opencode models [provider]   list all available models
  opencode stats               show token usage and cost statistics
  opencode session             manage sessions
  opencode export [sessionID]  export session data as JSON
  opencode import <file>       import session data
  opencode github              manage GitHub agent
  opencode pr <number>         fetch and checkout a GitHub PR branch
  opencode web                 start server + web interface
  opencode acp                 start ACP server
  opencode upgrade [target]    upgrade opencode
  opencode uninstall           uninstall opencode
  opencode db                  database tools

Global Options:
  -m, --model        model in format provider/model
  -c, --continue     continue the last session
  -s, --session      session id to continue
      --fork         fork the session when continuing
      --prompt       prompt to use
      --agent        agent to use
      --print-logs   print logs to stderr
      --log-level    DEBUG, INFO, WARN, ERROR
      --port         port to listen on                              [default: 0]
      --hostname     hostname to listen on                [default: "127.0.0.1"]
```

---

## 2. Architecture CLI

**Differences fondamentales vs Claude/Gemini/Qwen** :

- Ecrit en **Go** (binaire natif, pas de Node.js)
- Commande headless : **`opencode run`** (PAS `opencode -p`)
- **Provider-agnostic** : 75+ providers via Models.dev
- Stockage : **SQLite** (pas JSONL)
- Permissions **auto-approved par defaut** en mode `run` (pas de --yolo)
- **`--dir` flag existe** sur `run` (unique parmi les CLIs testees)
- **ATTENTION** : le mode `--format default` **bloque sans TTY** (spinner/TUI). Toujours utiliser `--format json` en headless.

### Installation

```bash
curl -fsSL https://raw.githubusercontent.com/opencode-ai/opencode/refs/heads/main/install | bash
```

### Auth

```bash
opencode auth login                    # interactif
# ou env vars
export ANTHROPIC_API_KEY="sk-ant-xxxxx"
export OPENAI_API_KEY="sk-xxxxx"
export GEMINI_API_KEY="xxxxx"
```

---

## 3. Invocation Patterns

### Basic headless call

```bash
opencode run "Create hello.txt with hello world" --format json
```

### Full production call (Gump)

```bash
opencode run "<prompt>" \
  --format json \
  --model anthropic/claude-sonnet-4-5 \
  --dir /path/to/worktree
```

### Output formats

|Format|Flag|Comportement headless|
|---|---|---|
|json|`--format json`|NDJSON streaming events. **Seul mode viable en headless.**|
|default|(aucun)|**BLOQUE sans TTY** (spinner/TUI). Ne pas utiliser.|

### Working directory

```bash
# Methode 1 : --dir flag (recommande, unique a OpenCode) — TESTE O6
opencode run "<prompt>" --format json --dir /path/to/worktree

# Methode 2 : cmd.Dir en Go
cmd := exec.Command("opencode", "run", prompt, "--format", "json")
cmd.Dir = "/path/to/worktree"
```

### Resume — TESTE O3a/O3b/O3c

```bash
opencode run --continue "<follow-up>" --format json            # derniere session
opencode run --session <ses_xxxxx> "<follow-up>" --format json # session specifique
opencode run --session <ses_xxxxx> --fork "<follow-up>"        # fork (nouveau session-id)
```

**Attention** : flag `--session`/`-s` (PAS `--resume` comme Claude/Qwen).

### Selection de modele

```bash
opencode run --model anthropic/claude-sonnet-4-5 "<prompt>"
opencode run --model openai/gpt-4.1 "<prompt>"
opencode run --model google/gemini-2.5-pro "<prompt>"
opencode run --model anthropic/claude-sonnet-4-5 --variant high "<prompt>"  # reasoning effort
```

### Agent specifique

```bash
opencode run --agent build "<prompt>"    # agent par defaut
opencode run --agent plan "<prompt>"     # agent plan (read-only)
```

### Context file — TESTE O5

OpenCode lit automatiquement :

- `AGENTS.md` a la racine du repo
- `CLAUDE.md` (desactivable via `OPENCODE_DISABLE_CLAUDE_CODE`)
- Regles dans `.opencode/rules/`

### Attach a un serveur (cold boot avoidance)

```bash
opencode serve                                             # terminal 1
opencode run --attach http://localhost:4096 "<prompt>"     # terminal 2
```

---

## 4. Output Format: JSON (NDJSON events)

`opencode run --format json` emet des evenements NDJSON en streaming.

**Difference fondamentale** : OpenCode n'a **PAS d'evenement `type: "result"` final**. Les metriques sont distribuees dans les `step_finish` events.

### 4 types d'evenements observes

#### Type 1: `step_start`

```jsonc
{
  "type": "step_start",
  "timestamp": 1772481616332,           // unix ms
  "sessionID": "ses_34fdce821ffe...",
  "part": {
    "id": "prt_cb02319cc001...",
    "sessionID": "ses_34fdce821ffe...",
    "messageID": "msg_cb02317fe001...",
    "type": "step-start",
    "snapshot": "e81cc1069298551d..."    // git SHA
  }
}
```

#### Type 2: `tool_use`

```jsonc
{
  "type": "tool_use",
  "timestamp": 1772481621258,
  "sessionID": "ses_...",
  "part": {
    "type": "tool",
    "callID": "call_SikLFFsSwXA2...",
    "tool": "apply_patch",
    "state": {
      "status": "completed",            // "completed" | "running" | "error"
      "input": {
        "patchText": "*** Begin Patch\n*** Add File: step1.txt\n+step 1 done\n*** End Patch"
      },
      "output": "Success. Updated the following files:\nA step1.txt",
      "title": "Success. Updated the following files:\nA step1.txt",
      "metadata": {
        "diff": "Index: .../step1.txt\n...",
        "files": [
          {
            "filePath": "/abs/path/step1.txt",
            "relativePath": "step1.txt",
            "type": "add",              // "add" | "modify" | "delete"
            "diff": "...",
            "before": "",
            "after": "step 1 done\n",
            "additions": 1,
            "deletions": 0
          }
        ],
        "diagnostics": {},
        "truncated": false
      },
      "time": {"start": ..., "end": ...}
    },
    "metadata": {"openai": {"itemId": "fc_..."}}
  }
}
```

#### Type 3: `text`

```jsonc
{
  "type": "text",
  "timestamp": 1772481624804,
  "sessionID": "ses_...",
  "part": {
    "type": "text",
    "text": "Done. Created `step1.txt` with:\n\n`step 1 done`",
    "time": {"start": ..., "end": ...}
  }
}
```

#### Type 4: `step_finish`

```jsonc
{
  "type": "step_finish",
  "timestamp": 1772481621290,
  "sessionID": "ses_...",
  "part": {
    "type": "step-finish",
    "reason": "tool-calls",             // "tool-calls" | "stop"
    "snapshot": "d17051182559...",       // git SHA apres modifications
    "cost": 0,                          // NOTE: toujours 0 dans tous les tests
    "tokens": {
      "total": 8862,
      "input": 8631,
      "output": 231,
      "reasoning": 190,                 // tokens de reasoning (extended thinking)
      "cache": {"read": 0, "write": 0}
    }
  }
}
```

### Sequence typique: file creation (6 lignes, test O3a)

```
Line 1: step_start     → debut step 1 (snapshot: SHA initial)
Line 2: tool_use       → apply_patch (creation fichier, diff structure)
Line 3: step_finish    → fin step 1 (reason: "tool-calls", tokens: 8862)
Line 4: step_start     → debut step 2 (snapshot: SHA apres patch)
Line 5: text           → reponse texte finale
Line 6: step_finish    → fin step 2 (reason: "stop", tokens: 8904, cache_read: 8704)
```

### Sequence: tache impossible (3 lignes, test O4)

```
Line 1: step_start     → debut step
Line 2: text           → explication texte (pas de tool_use)
Line 3: step_finish    → fin (reason: "stop", tokens: 8802)
```

### Sequence: question simple (3 lignes, test O5)

```
Line 1: step_start     → debut
Line 2: text           → "PUDDING-CANARY-42"
Line 3: step_finish    → fin (reason: "stop", tokens: 8753)
```

---

## 5. Models and Gump Aliases

### Available models (--model flag, format `provider/model`)

|Model ID|Description|
|---|---|
|`anthropic/claude-opus-4-6`|Claude Opus 4.6 via Anthropic|
|`anthropic/claude-sonnet-4-6`|Claude Sonnet 4.6 via Anthropic|
|`anthropic/claude-haiku-4-5`|Claude Haiku 4.5 via Anthropic|
|`openai/gpt-5.4`|GPT-5.4 via OpenAI|
|`openai/gpt-5.3`|GPT-5.3 via OpenAI|
|`openai/gpt-5.2`|GPT-5.2 via OpenAI|
|`google/gemini-3.1-pro`|Gemini 3.1 Pro via Google|
|`google/gemini-2.5-pro`|Gemini 2.5 Pro via Google|

OpenCode utilise le format `provider/model` et supporte 75+ providers via Models.dev. Le `--variant` flag (low/medium/high/max) contrôle le reasoning effort. Le `--agent` flag (build/plan) sélectionne l'agent interne.

### Gump alias → model flag

|Alias Gump|`--model` flag|Description|
|---|---|---|
|`opencode`|(omis — défaut configuré)|Défaut de l'installation OpenCode|
|`opencode-opus`|`anthropic/claude-opus-4-6`|Claude Opus 4.6|
|`opencode-sonnet`|`anthropic/claude-sonnet-4-6`|Claude Sonnet 4.6|
|`opencode-haiku`|`anthropic/claude-haiku-4-5`|Claude Haiku 4.5|
|`opencode-gpt54`|`openai/gpt-5.4`|GPT-5.4|
|`opencode-gpt53`|`openai/gpt-5.3`|GPT-5.3|
|`opencode-gemini`|`google/gemini-3.1-pro`|Gemini 3.1 Pro|

Fallback : si l'alias ne matche pas la table, extraire la partie après `opencode-` et la passer telle quelle à `--model` (format `provider/model` attendu).

---

## 6. Mapping RunResult pour Gump

```go
func parseOpenCodeEvents(events []map[string]interface{}) *RunResult {
    result := &RunResult{}
    var totalInput, totalOutput, totalReasoning, totalCacheRead int
    var lastText string
    var firstTimestamp, lastTimestamp int64
    var hasStop bool

    for _, evt := range events {
        ts := getInt64(evt, "timestamp")
        if firstTimestamp == 0 { firstTimestamp = ts }
        lastTimestamp = ts

        // sessionID present dans TOUS les events
        if sid, ok := evt["sessionID"].(string); ok {
            result.SessionID = sid
        }

        switch evt["type"] {
        case "step_finish":
            part := getMap(evt, "part")
            tokens := getMap(part, "tokens")
            totalInput += getInt(tokens, "input")
            totalOutput += getInt(tokens, "output")
            totalReasoning += getInt(tokens, "reasoning")
            cache := getMap(tokens, "cache")
            totalCacheRead += getInt(cache, "read")
            result.NumTurns++
            if getString(part, "reason") == "stop" {
                hasStop = true
            }
        case "text":
            part := getMap(evt, "part")
            lastText = getString(part, "text")
        }
    }

    result.InputTokens = totalInput
    result.OutputTokens = totalOutput
    result.CacheReadTokens = totalCacheRead
    result.Result = lastText
    result.DurationMs = int(lastTimestamp - firstTimestamp)
    result.IsError = !hasStop  // pas de champ is_error : deduire de l'absence de "stop"
    result.CostUSD = 0         // toujours 0 dans les events. Calculer cote Gump.
    return result
}
```

---

## 7. Resume Behavior — TESTE O3a/O3b/O3c

|Step|Commande|sessionID|Fichier cree|Same session|
|---|---|---|---|---|
|O3a|`opencode run "..."`|`ses_34fdce821ffesYYSMB00M8ifRb`|step1.txt YES|—|
|O3b|`opencode run --continue "..."`|`ses_34fdce821ffesYYSMB00M8ifRb`|step2.txt YES|**YES**|
|O3c|`opencode run --session ses_... "..."`|(meme)|step3.txt YES|**YES**|

**Confirme** :

- Session ID preserve entre `--continue` et la session initiale
- `--session <id>` fonctionne en mode headless
- Les fichiers des steps precedents sont visibles par les steps suivants
- Le contexte conversationnel est conserve

---

## 7. Error Detection — TESTE O4, O7

### Exit codes

|Scenario|Exit code|Test|
|---|---|---|
|Run succes|**0**|O1, O3a, O5, O6|
|Tache impossible|**0**|O4|
|Flag invalide|**1**|O7|

### Strategie pour Gump

1. Si exit code **non-zero** → erreur CLI/infra (flag invalide, auth echouee, etc.)
2. Si exit code **zero** mais aucun event recu → erreur infra (timeout, pipe)
3. Si events recus → parser normalement. Pas de champ `is_error`.
4. Pas de distinction "erreur vs succes" dans les events : l'agent repond toujours en texte. Le diff git reste la source de verite pour le succes fonctionnel.

### Comparaison error detection

|CLI|Exit code succes|Exit code erreur agent|Exit code erreur CLI|
|---|---|---|---|
|Claude Code|0|0|0|
|Qwen Code|0|0|0|
|**OpenCode**|0|0|**1** (meilleur !)|

---

## 8. Permissions

**Confirme** : toutes les permissions sont auto-approved en mode `run`. Pas de `--yolo` ni `--permission-mode` necessaire.

---

## 9. Context File — TESTE O5

- `AGENTS.md` a la racine du repo : **LU automatiquement sans tool call**
- L'agent repond "PUDDING-CANARY-42" sans utiliser grep/read_file
- `CLAUDE.md` egalement lu (desactivable via `OPENCODE_DISABLE_CLAUDE_CODE`)

Pour Gump : generer `AGENTS.md` au lieu de `CLAUDE.md`.

---

## 10. `--dir` Flag — TESTE O6

- Commande lancee depuis REPORT_DIR, avec `--dir` pointant vers O6_DIR
- Fichier `dir-test.txt` cree dans **O6_DIR** (YES)
- Fichier absent de REPORT_DIR (NO)
- **Le flag fonctionne correctement**

Unique a OpenCode : aucune autre CLI testee n'a ce flag. Alternative pour les autres CLIs : utiliser `cmd.Dir` en Go.

---

## 11. Session Management — TESTE O8

### `opencode session list --format json`

```jsonc
[
  {
    "id": "ses_34fdf6327ffej12YBCE7sYWj65",
    "title": "New session - 2026-03-02T19:57:33.272Z",
    "updated": 1772481454505
    // ... autres champs
  }
]
```

JSON array standard. Format id : `ses_xxxxx`.

### Commandes session utiles

```bash
opencode session list                          # liste les sessions
opencode session list --format json            # format JSON
opencode export <sessionID>                    # export complet d'une session
```

---

## 12. Differences cles vs Claude Code et Qwen Code

|Aspect|Claude Code|Qwen Code|OpenCode|
|---|---|---|---|
|**Langage**|Node.js|Node.js (fork Gemini)|**Go natif**|
|**Commande headless**|`claude -p`|`qwen -p`|**`opencode run`**|
|**Resultat final**|`type: "result"` unique|`type: "result"` dans array|**PAS de result. Agreger step_finish**|
|**session_id format**|UUID|UUID|**`ses_xxxxx`** (prefixe)|
|**Champ session**|`session_id`|`session_id`|**`sessionID`** (camelCase)|
|**Resume flag**|`--resume <id>`|`--resume <id>`|**`--session <id>`**|
|**Verbose pour stream**|OBLIGATOIRE|PAS necessaire|N/A (un seul format json)|
|**Exit code erreur CLI**|0|0|**1**|
|**Exit code erreur agent**|0|0|0|
|**Cost dans output**|OUI (total_cost_usd)|NON|NON (cost=0)|
|**Tokens**|Agrege dans result|Agrege dans result|**Par step_finish, a sommer**|
|**Reasoning tokens**|Non distingue|Non distingue|**Champ `reasoning` separe**|
|**Git snapshots**|Non|Non|**OUI** (SHA dans step_start/finish)|
|**Diff structure**|Dans user event|Dans user event|**Dans tool_use** (tres riche: before/after/additions/deletions)|
|**`--dir` flag**|NON|NON|**OUI**|
|**`--max-turns`**|OUI|OUI (--max-session-turns)|**NON**|
|**`--allowed-tools`**|OUI (--allowedTools)|OUI (--allowed-tools)|**NON**|
|**Context file**|CLAUDE.md|QWEN.md|**AGENTS.md**|
|**Auto-approve headless**|NON (--permission-mode)|NON (--yolo)|**OUI** (par defaut)|
|**Default sans TTY**|OK (text)|OK (text)|**BLOQUE** (spinner/TUI)|
|**Pipe stdout**|OK|OK|**BLOQUE** (tee casse le streaming)|

---

## 13. Flags confirmes

### Testes

|Flag|Status|Test|
|---|---|---|
|`--format json`|✅|O1, O3a, O4, O5, O6|
|`--continue`|✅|O3b|
|`--session <id>`|✅|O3c|
|`--dir`|✅|O6|

### Confirmes par help (non testes)

`--model`, `--agent`, `--variant`, `--thinking`, `--fork`, `--file`, `--title`, `--attach`, `--share`, `--port`, `--command`

### Flags absents (confirme par help)

- Pas de `--max-turns` (ni equivalent)
- Pas de `--system-prompt` (utiliser AGENTS.md ou .opencode/rules/)
- Pas de `--yolo` (auto-approve par defaut en run)
- Pas de `--allowedTools` / `--excludedTools` sur `run`
- Pas de `-q` / `--quiet`

---

## 14. Gotchas pour l'implementation

1. **`--format default` bloque sans TTY** : toujours `--format json`
2. **Pipe avec `| tee` bloque aussi** : rediriger vers fichier (`> file`) seulement
3. **Pas de `type: "result"` final** : il faut sommer les `step_finish` events
4. **`sessionID` en camelCase** (pas `session_id`)
5. **`cost: 0` toujours** dans les events : calculer cote Gump avec le pricing du provider
6. **Le field `total` dans tokens** inclut input+output+reasoning, mais PAS le cache. Utiliser input/output/reasoning separement.
7. **Git est obligatoire** : OpenCode refuse de fonctionner sans repo git initialise

---

## 15. Commande Launch pour Gump

```bash
opencode run "<prompt>" \
  --format json \
  --model <provider/model> \
  --dir /path/to/worktree
```

### Commande Resume

```bash
opencode run --session "<ses_xxxxx>" "<follow-up prompt>" \
  --format json \
  --dir /path/to/worktree
```

### Commande Continue (derniere session)

```bash
opencode run --continue "<follow-up prompt>" \
  --format json \
  --dir /path/to/worktree
```

---

## 16. Raw Test Data

### Test O1 (premier script) — run --format json (success)

- Exit: 0. 6 lignes NDJSON.
- sessionID: `ses_34fee4acfffe3cyoYXJekJMVsx`
- Tokens step 1: total=8822, input=8634, output=188, reasoning=150
- Tokens step 2: total=8860, input=139, output=17, reasoning=0, cache_read=8704
- Fichier hello.txt cree: YES

### Test O3a — initial run (success)

- Exit: 0. 6 lignes NDJSON.
- sessionID: `ses_34fdce821ffesYYSMB00M8ifRb`
- Tokens step 1: total=8862, input=8631, output=231, reasoning=190
- Tokens step 2: total=8904, input=180, output=20, reasoning=0, cache_read=8704
- Fichier step1.txt cree: YES

### Test O3b — --continue

- Exit: 0. Session ID preserve: **YES** (identique a O3a)
- Fichier step2.txt cree: YES

### Test O3c — --session <id>

- Exit: 0.
- Fichier step3.txt cree: YES

### Test O4 — error handling (tache impossible)

- Exit: 0. 3 lignes NDJSON (step_start + text + step_finish). Pas de tool_use.
- sessionID: `ses_34fdc8135ffeLohKdasxj0YQSt`
- Tokens: total=8802, input=8629, output=173, reasoning=12
- L'agent repond en texte avec explication detaillee

### Test O5 — AGENTS.md context file

- Exit: 0. 3 lignes NDJSON.
- Agent repond: "PUDDING-CANARY-42" — **match exact**
- Tokens: total=8753, input=8693, output=60, reasoning=45
- AGENTS.md lu automatiquement sans tool call

### Test O6 — --dir flag

- Exit: 0.
- Fichier dir-test.txt dans O6_DIR: **YES**
- Fichier dir-test.txt dans REPORT_DIR: **NO**
- --dir fonctionne correctement

### Test O7 — exit codes

- Flag invalide: exit **1**

### Test O8 — session list

- Format: JSON array
- Champs: id (`ses_xxxxx`), title, updated (timestamp ms)