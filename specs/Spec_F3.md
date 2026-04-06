# Gump — Spec F3 : Adapter Hardening — Confiance Multi-Provider

> Spec destinée à Cursor Composer. Pas de code dans ce document. Prérequis : G1-G5 implémentées, F1 implémentée, `go test ./...` passe. Objectif : s'assurer que chaque adapter est proprement factorisé, que les quirks provider sont documentés exhaustivement, que Cursor utilise les bons modèles par défaut, et que la suite de smoke tests couvre la matrice workflow × agent. Résultat attendu : un `make smoke-matrix` produit un tableau de confiance par provider, les cli-ref docs sont la source de vérité complète pour chaque connecteur.

---

## 1. Vue d'ensemble

### Partie A — Audit factorisation adapters

Vérifier qu'il n'y a aucune logique provider-specific hors de `internal/agent/`. Les `if provider == X` dans l'engine, le context builder, le template engine, le report, ou ailleurs sont des violations d'encapsulation. Les identifier et les migrer dans l'adapter concerné.

### Partie B — cli-ref docs complètes

Les fichiers `docs/connector-book/cli-ref-*.md` sont la source de vérité documentaire de chaque connecteur. Ils doivent couvrir exhaustivement : paramètres CLI, quirks de parsing, modèles et alias, format de stream, limites connues, tokens/cost reporting, context window sizes, changelog des breaking changes. Un agent qui compare un cli-ref à un changelog provider doit pouvoir identifier si une release Gump est nécessaire.

### Partie C — Cursor defaults : Composer

Le modèle par défaut de Cursor dans Gump est actuellement `claude-4.6-opus-high-thinking` (le défaut de `cursor-agent` quand `--model` est omis). Ajouter des alias `cursor-composer` (cheap → Composer 1.5) et `cursor-composer-2` (sota → Composer 2.0) pour les modèles maison Cursor. Mettre à jour le `cursor` default pour pointer vers `cursor-composer` (cheap par défaut, cohérent avec la philosophie Gump "cheap d'abord, escalade si nécessaire").

### Partie D — Smoke matrice multi-provider

Ajouter `make smoke AGENT=<provider>` (forcer un seul provider pour les tests single-agent), et `make smoke-matrix` (lancer la matrice complète workflow × agent). Le tableau de sortie identifie les trous de couverture.

---

## 2. Blast Radius

### Partie A — Audit factorisation

```
internal/engine/engine.go                # Auditer — déplacer toute logique provider-specific vers l'adapter
internal/engine/turn_tracker.go          # Auditer — la détection de turn est déjà provider-agnostic (vérifier)
internal/engine/display_format.go        # Auditer — le mapping tool names est déjà par provider (vérifier)
internal/context/builder.go              # Auditer — le context file name est déjà résolu par l'adapter (vérifier)
internal/agent/adapter.go               # Si besoin, enrichir l'interface AgentAdapter avec de nouvelles méthodes
```

### Partie B — cli-ref docs

```
docs/connector-book/cli-ref-claude.md    # Compléter les sections manquantes
docs/connector-book/cli-ref-codex.md     # idem
docs/connector-book/cli-ref-gemini.md    # idem
docs/connector-book/cli-ref-qwen.md      # idem
docs/connector-book/cli-ref-opencode.md  # idem
docs/connector-book/cli-ref-cursor-agent.md  # idem
```

### Partie C — Cursor defaults

```
internal/agent/cursor.go                 # Ajouter aliases Composer, changer le défaut
```

### Partie D — Smoke matrice

```
smoke/smoke_test.go                      # Lire GUMP_SMOKE_AGENT, filtrer les tests
smoke/matrix_test.go                     # NOUVEAU — tests matrice avec rapport tabulaire
smoke/helpers_test.go                    # Ajouter helper pour override agent
Makefile                                 # Ajouter targets smoke-matrix, smoke avec AGENT=
```

### Fichiers à NE PAS toucher

```
internal/recipe/                         # Inchangé
internal/validate/                       # Inchangé
internal/telemetry/                      # Inchangé
internal/template/                       # Inchangé
internal/pricing/                        # Inchangé (F2)
cmd/                                     # Inchangé (sauf si l'audit Partie A remonte un problème)
```

---

## 3. Partie A — Audit factorisation adapters : Détail

### Méthode d'audit

1. `grep -rn "claude\|codex\|gemini\|qwen\|opencode\|cursor" internal/engine/ internal/context/ internal/report/ internal/template/ internal/sandbox/ internal/config/ cmd/` → lister toutes les références à un provider nommé hors de `internal/agent/`.
2. Pour chaque occurrence, classifier :
   - **Acceptable** : nom de provider dans un message d'erreur user-facing, dans un commentaire de documentation, dans un test, ou dans une table de registry (l'engine résout le provider par préfixe — c'est du mapping, pas de la logique).
   - **Violation** : un `if` ou un `switch` sur le nom de provider qui implémente un comportement spécifique. Exemple : `if agentName == "codex" { parseCmdExecution(...) }` dans l'engine.
3. Pour chaque violation, migrer la logique dans l'adapter. L'adapter expose une méthode ou un champ dans l'interface `AgentAdapter` si nécessaire.

### Violations confirmées par l'audit

**`internal/engine/turn_tracker.go` lignes 93-110** : un `switch tt.provider` avec des cas pour `codex`, `opencode`, `cursor`, `gemini`, `qwen`, `claude`. C'est la violation la plus claire. La méthode `isNewTurn()` et `shouldCompleteTurn()` contiennent de la logique provider-specific dans l'engine.

**Migration recommandée (Option 1)** : chaque adapter set un champ `IsTurnBoundary bool` et `IsTurnComplete bool` sur les `StreamEvent` qu'il émet. Le TurnTracker ne fait que lire ces champs. La logique de détection (quand un turn commence, quand il se termine) est dans l'adapter — c'est l'adapter qui connaît son format de stream.

L'interface `AgentAdapter` n'a pas besoin de changer — les champs sont sur `StreamEvent` (struct, pas interface).

| Emplacement suspect | Violation ? | Migration |
|---------------------|------------|-----------|
| `engine.go` — registry switch | Mapping préfixe → adapter. C'est du registry, pas de la logique. | Acceptable |
| `turn_tracker.go` — détection turn boundary | "Claude/Qwen = assistant→user→assistant, Codex = turn.started/completed, Gemini = heuristique..." Si c'est un switch sur le provider name → violation. Si c'est basé sur le format des events → acceptable. | Vérifier |
| `display_format.go` — mapping tool names | "Codex est un cas particulier : tout passe par command_execution". Si c'est un `if codex` → violation. Si c'est une table de mapping par tool type → acceptable. | Vérifier |
| `context/builder.go` — context file name | Le nom du fichier (CLAUDE.md, AGENTS.md, GEMINI.md, QWEN.md, .cursor/rules/gump-agent.mdc) est résolu par l'adapter. Si l'adapter retourne le path → acceptable. Si le builder a un switch → violation. | Vérifier |
| `engine.go` — cross-provider resume check | La règle "resume interdit entre providers différents" est dans l'engine. C'est une règle engine, pas provider-specific → acceptable. | Acceptable |

### Ce qui doit rester dans l'engine

- Le registry (mapping préfixe → adapter).
- La règle cross-provider resume.
- L'appel à `adapter.Launch()` / `adapter.Resume()` / `adapter.Stream()` / `adapter.Wait()`.
- Les guards (max_turns, max_budget, no_write) — ils sont engine-level.

### Ce qui doit être dans l'adapter

- Le parsing du stream (format des events).
- La détection de turn boundary (peut être exposée via une méthode `IsNewTurn(event) bool` sur l'adapter, ou via un champ dans le `StreamEvent` que l'adapter set).
- Le mapping des tool names (peut être un champ `ToolNameMapping map[string]string` sur l'adapter).
- Le nom du context file.
- Le format de la commande CLI.
- La résolution des alias modèles.

### Si la détection de turn est déjà dans le TurnTracker (engine-level)

La spec G4 mentionne que la détection de turn est dans `turn_tracker.go` avec des heuristiques par provider. Si c'est implémenté comme un switch sur le provider name, c'est une violation. Deux options :

**Option 1 (recommandée)** : l'adapter set un champ `IsTurnBoundary bool` sur chaque `StreamEvent` qu'il émet. Le TurnTracker ne fait que lire ce champ. La logique de détection est dans l'adapter.

**Option 2** : le TurnTracker détecte les turns par le format des events (pas par le nom du provider). Si un event a type `turn.started` → nouvelle turn. Si un event a type `assistant` après un `user` → nouvelle turn. Heuristique basée sur le contenu, pas sur le provider.

Choisir l'option qui minimise le changement par rapport à l'implémentation actuelle. Si le TurnTracker fonctionne déjà en mode Option 2 → ne pas changer. Si c'est un switch → migrer vers Option 1.

---

## 4. Partie B — cli-ref docs complètes : Détail

### Structure attendue de chaque cli-ref

Chaque `docs/connector-book/cli-ref-<provider>.md` doit contenir ces sections, dans cet ordre :

1. **Header** : nom du provider, version CLI testée, date du dernier test.
2. **Installation** : commande d'installation, prérequis, auth.
3. **CLI Parameters** : table complète de tous les flags CLI utilisés par Gump, avec description et valeurs possibles.
4. **Stream Format** : format de sortie (NDJSON, JSON), exemples d'events, mapping vers les types Gump.
5. **Models and Gump Aliases** : table complète alias Gump → flag CLI → modèle sous-jacent. Prix par token (source : pricing page officielle).
6. **Context Window Sizes** : par modèle.
7. **Tokens and Cost Reporting** : quels métriques le provider reporte (tokens par turn ? agrégé ? cost natif ?), confiance associée.
8. **Session Resume** : format du session-id, commande de resume, limitations.
9. **Known Quirks** : tout ce qui est spécifique à ce provider et qui a nécessité un traitement particulier dans l'adapter. Exemples :
   - Codex : `command_execution` au lieu de Read/Write/Bash.
   - OpenCode : seul provider avec `--dir` natif, exit code 1 significatif, stdout capturé dans un fichier.
   - Gemini : tokens agrégés (pas par turn), pas de cost natif.
   - Cursor : camelCase dans les champs usage, pas de max-turns flag.
10. **Compat Mode** : conditions de déclenchement, comportement dégradé.
11. **Doctor Harness** : commande de test, critères vert/rouge/jaune.
12. **Changelog Watch** : liste des breaking changes passés du provider qui ont impacté Gump, et les points de surveillance pour détecter les prochains. Un agent qui compare cette section au changelog du provider peut identifier si une release Gump est nécessaire.

### Méthode

Pour chaque provider, vérifier le cli-ref existant section par section. Compléter les sections manquantes. Les sources d'information sont :
- Le code de l'adapter (`internal/agent/<provider>.go`).
- Les specs G1-G5 (qui documentent les quirks).
- Les tests e2e et smoke (qui montrent les cas couverts).
- La documentation officielle du provider CLI.

### Localisation

Les cli-ref sont dans `docs/connector-book/` qui est dans `.gitignore`. Ce sont des docs internes, pas publiées. Elles sont la source de vérité pour les développeurs et les agents qui maintiennent les adapters.

---

## 5. Partie C — Cursor defaults : Détail

### Contexte

G5 Partie E a implémenté l'adapter Cursor avec les alias suivants :

| Agent dans le workflow | `--model` flag |
|---|---|
| `cursor` | (omis — défaut provider) |
| `cursor-sonnet` | `claude-4.6-sonnet-medium` |
| `cursor-opus` | `claude-4.6-opus-high` |
| etc. | |

Le défaut quand `--model` est omis est le défaut de `cursor-agent` (actuellement `claude-4.6-opus-high-thinking`). C'est un modèle Anthropic routé par Cursor, pas un modèle maison Cursor.

### Changement

Cursor a ses propres modèles "Composer" qui sont des modèles maison optimisés pour le coding. Ajouter les alias :

| Agent dans le workflow | `--model` flag | Rôle |
|---|---|---|
| `cursor-composer` | `composer-1.5` | Cheap (modèle maison Cursor, rapide et économique) |
| `cursor-composer-2` | `composer-2.0` | SOTA (modèle maison Cursor, le meilleur) |

**Changer le défaut** : quand l'agent est `cursor` (sans suffixe), le `--model` flag doit être `composer-1.5` au lieu d'être omis. Rationale : Gump favorise le cheap par défaut (escalade si nécessaire), et les modèles Composer sont les modèles les plus adaptés au coding agent dans l'écosystème Cursor.

**Conséquence** : les workflows qui utilisent `agent: cursor` (sans suffixe) utiliseront Composer 1.5 au lieu de claude-4.6-opus-high-thinking. C'est un changement de comportement. Les workflows qui veulent explicitement l'ancien défaut peuvent utiliser `agent: cursor-opus-thinking`.

### Table mise à jour complète

| Agent dans le workflow | `--model` flag |
|---|---|
| `cursor` | `composer-1.5` |
| `cursor-composer` | `composer-1.5` |
| `cursor-composer-2` | `composer-2.0` |
| `cursor-sonnet` | `claude-4.6-sonnet-medium` |
| `cursor-sonnet-thinking` | `claude-4.6-sonnet-medium-thinking` |
| `cursor-opus` | `claude-4.6-opus-high` |
| `cursor-opus-thinking` | `claude-4.6-opus-high-thinking` |
| `cursor-gpt5` | `gpt-5.4-medium` |
| `cursor-gemini` | `gemini-3.1-pro` |

Le fallback passthrough reste : si l'alias n'est pas dans la table, extraire la partie après `cursor-` et la passer à `--model`.

### Vérification

Les noms de modèles Cursor (`composer-1.5`, `composer-2.0`) doivent être validés contre la documentation Cursor CLI ou par un test `cursor-agent --help` / `cursor-agent models`. Si les noms sont différents (ex: `cursor-composer-1.5` au lieu de `composer-1.5`), ajuster. Le passthrough garantit que même si le nom est inexact, l'utilisateur peut toujours spécifier le nom exact du modèle.

---

## 6. Partie D — Smoke matrice : Détail

### `make smoke AGENT=<provider>`

Comportement : ne lance que les tests single-agent en forçant le provider demandé. Les tests multi-provider (cross-provider) sont skippés.

Implémentation :
- Variable d'environnement `GUMP_SMOKE_AGENT`. Le Makefile la propage.
- Dans `smoke/helpers_test.go`, ajouter une fonction `smokeAgent(t *testing.T) string` qui lit `GUMP_SMOKE_AGENT`. Si non vide, vérifier que le CLI est installé (sinon skip), et retourner le provider.
- Les tests single-agent (freeform, tdd, bugfix, etc.) utilisent `smokeAgent(t)` pour déterminer l'agent. Si `GUMP_SMOKE_AGENT` est vide → utiliser le défaut du workflow.
- Les tests multi-provider ont un guard : `if os.Getenv("GUMP_SMOKE_AGENT") != "" { t.Skip("single-agent mode") }`.

```makefile
.PHONY: smoke smoke-matrix

smoke:
	GUMP_SMOKE_AGENT=$(AGENT) go test ./smoke/ -tags=smoke -v -timeout 30m -count=1

smoke-matrix:
	@for agent in claude codex gemini qwen opencode cursor; do \
		echo "=== Smoke: $$agent ==="; \
		GUMP_SMOKE_AGENT=$$agent go test ./smoke/ -tags=smoke -v -timeout 30m -count=1 2>&1 | tail -20; \
		echo ""; \
	done
```

### Override agent dans les tests

Quand `GUMP_SMOKE_AGENT=qwen`, les tests passent `--agent qwen-<model>` au lieu de l'agent spécifié dans le workflow. Le mapping cheap/sota par provider :

| Provider | Cheap (défaut) | SOTA |
|---|---|---|
| claude | claude-haiku | claude-sonnet |
| codex | codex-gpt54-mini | codex-gpt54 |
| gemini | gemini-flash | gemini-pro |
| qwen | qwen | qwen-plus |
| opencode | opencode-haiku | opencode-sonnet |
| cursor | cursor-composer | cursor-composer-2 |

Le mode cheap est utilisé par défaut dans les smoke tests (économie de tokens). Le mode SOTA est disponible via `GUMP_SMOKE_TIER=sota` :

| Provider | Cheap (défaut) | SOTA (`GUMP_SMOKE_TIER=sota`) |
|---|---|---|
| claude | claude-haiku | claude-sonnet |
| codex | codex-gpt54-mini | codex-gpt54 |
| gemini | gemini-flash | gemini-pro |
| qwen | qwen | qwen-plus |
| opencode | opencode-haiku | opencode-sonnet |
| cursor | cursor-composer | cursor-composer-2 |

Quand `GUMP_SMOKE_TIER=sota`, les tests utilisent le modèle SOTA au lieu du cheap. Cela permet de valider que les modèles performants fonctionnent aussi (certains peuvent avoir des formats de réponse différents).

### Tests matrice

`smoke/matrix_test.go` contient un test `TestSmokeMatrix` qui :

1. Itère sur chaque agent installé.
2. Pour chaque agent, lance les tests freeform, tdd, et bugfix (les 3 workflows les plus discriminants).
3. Collecte les résultats pass/fail.
4. Affiche un tableau récapitulatif :

```
Smoke Matrix Results:
                claude  codex  gemini  qwen  opencode  cursor
  freeform        ✓       ✓      ✓      ✓      ✓        ✓
  tdd             ✓       ✓      ✗      ✓      ✓        ✓
  bugfix          ✓       ✓      ✓      ✗      ✓        ✓
```

Le test matrice n'est PAS lancé par `make smoke` (trop long, trop coûteux). Il est lancé uniquement par `make smoke-matrix`. Il est protégé par un build tag supplémentaire ou par la variable `GUMP_SMOKE_MATRIX=1`.

### Couverture des rôles agent

Les tests matrice doivent vérifier que chaque agent fonctionne dans les 3 rôles clés :

- **Plan** (output: plan) : l'agent produit un plan JSON structuré. Vérifié par le workflow tdd (step decompose).
- **Dev** (output: diff) : l'agent produit du code. Vérifié par tous les workflows.
- **Review** (output: review) : l'agent produit un avis pass/fail. Vérifié par le workflow adversarial-review (si inclus dans la matrice) ou par un test dédié.

Le test freeform couvre le rôle Dev. Le test tdd couvre Plan + Dev. Pour le rôle Review, ajouter un test matrice dédié qui utilise un mini workflow avec un step review :

```yaml
name: review-smoke
steps:
  - name: generate
    agent: <AGENT>
    output: diff
    prompt: "Create a function Add(a, b int) int and a test."
    gate: [compile]
  - name: review
    agent: <AGENT>
    output: review
    session: fresh
    prompt: "Review the code. Pass if it has tests, fail otherwise."
```

---

## 7. Tests e2e

### F3-E2E-1 : Audit — pas de provider switch dans l'engine

```
Vérif  : grep -rn "agentName\|providerName\|agent.Name" internal/engine/ internal/context/ internal/report/ internal/template/
         aucune occurrence n'est dans un if/switch qui implémente du comportement provider-specific
         (les occurrences dans des messages d'erreur, des logs, ou des tables de registry sont acceptables)
```

### F3-E2E-2 : Cursor default model

```
Setup  : stub cursor
Entrée : gump run spec.md --workflow freeform --agent cursor --agent-stub
Vérif  : agent_launched dans le manifest contient "--model" et "composer" (pas omis comme avant)
```

### F3-E2E-3 : Cursor composer alias

```
Setup  : stub cursor
Entrée : gump run spec.md --workflow freeform --agent cursor-composer-2 --agent-stub
Vérif  : agent_launched contient "--model" et "composer-2.0"
```

### F3-E2E-4 : Smoke agent override

```
Setup  : GUMP_SMOKE_AGENT=claude
Entrée : go test ./smoke/ -tags=smoke -run TestSmokeFreeform -v -timeout 5m
Vérif  : le test utilise claude (pas le défaut du workflow)
```

### F3-E2E-5 : Non-régression

```
Vérif  : go test ./... passe
```

---

## 8. Smoke tests

**`TestSmokeAllWorkflowsLive`** : pour chaque workflow built-in (tdd, bugfix, refactor, freeform, cheap2sota, parallel-tasks, adversarial-review), lancer un run avec l'agent par défaut sur un spec triviale → vérifier exit 0. Skip si l'agent n'est pas installé.

**`TestSmokeMatrixLive`** : la matrice workflow × agent (cf Partie D). Lancé uniquement par `make smoke-matrix`.

**`TestSmokeReviewRoleLive`** : le mini workflow review-smoke → vérifier que l'agent produit un review pass/fail parsable.

---

## 9. Critères de succès

1. `go build -o gump .` compile.
2. Aucune logique provider-specific hors de `internal/agent/`.
3. Les 6 cli-ref docs contiennent toutes les 12 sections.
4. `cursor` par défaut utilise Composer 1.5.
5. `cursor-composer-2` route vers Composer 2.0.
6. `make smoke AGENT=qwen` lance les smoke tests avec Qwen uniquement.
7. `make smoke-matrix` produit un tableau workflow × agent.
8. Les 3 rôles (plan, dev, review) sont testés par agent dans la matrice.
9. Les 5 tests e2e passent.
10. `go test ./...` passe.