# Gump — Spec F4 : Test Coverage — Fiabilité Runtime

> Spec destinée à Cursor Composer. Pas de code dans ce document. Prérequis : G1-G5 implémentées, F1 implémentée (rebranding terminé, 13 tests G4 écrits, bug Cobra fixé), `go test ./...` passe. Objectif : atteindre la couverture qui garantit la fiabilité d'un runtime géré par des agents — ce qui n'est pas testé n'est pas garanti. 

> **Contexte** : l'audit croisé identifie 169 comportements spécifiés, dont 152 ont un test e2e (90%). Les 17 trous sont : les 13 tests G4 (couverts par F1 Partie H) + 4 edge cases ci-dessous. Cette spec couvre : (1) les 4 edge cases e2e restants, (2) les tests unitaires manquants sur les packages critiques, (3) les smoke tests par workflow built-in et multi-agent, (4) la documentation des limitations, (5) la policy bugfix.

---

## 1. Vue d'ensemble

### Partie A — Tests unitaires packages critiques

L'engine a 11% de ratio test/source (91 lignes de tests pour 1901 lignes de code). Le statebag a 16%. Les tests e2e compensent via le binaire, mais un refactor interne cassera sans filet. Écrire les tests unitaires manquants pour les fonctions exportées.

### Partie B — 4 tests e2e edge cases restants

Après F1 Partie H (13 tests G4), il reste 4 comportements sans test e2e :

| ID | Comportement | Criticité |
|----|---|---|
| A13 | Empty plan (0 items) → pas de boucle infinie | Haute |
| B20 | Error context non-accumulation (seul dernier attempt injecté) | Haute |
| G7 | Resume au milieu d'un foreach (item FATAL → resume reprend l'item) | Haute |
| G8 | Resume avec parallel branch FATAL | Moyenne |

### Partie C — Smoke tests : un par workflow built-in + multi-agent

Les smoke existants couvrent freeform, tdd, cross-provider, session-reuse, retry, parallel, replay, composition, guard, resume, on_failure conditionnel, report. Manquent : bugfix, refactor, cheap2sota, adversarial-review. Ajouter `make smoke AGENT=X` et `make smoke-matrix`.

### Partie D — Edge cases documentés + policy bugfix

Documenter les limitations connues. Établir "un bugfix = un test de régression".

---

## 2. Blast Radius

### Partie A — Tests unitaires

```
internal/engine/budget_test.go           # NOUVEAU
internal/engine/parallel_test.go         # NOUVEAU
internal/engine/restart_test.go          # NOUVEAU
internal/engine/review_test.go           # NOUVEAU
internal/engine/replay_test.go           # COMPLÉTER (3 tests existants + 3 à ajouter)
internal/statebag/statebag_test.go       # COMPLÉTER (4 tests existants + 16 à ajouter)
internal/cook/store_test.go              # NOUVEAU
internal/ledger/reader_test.go           # NOUVEAU
internal/report/render_test.go           # NOUVEAU (tests minimaux anti-panic)
internal/report/parser_test.go           # NOUVEAU
```

### Partie B — Tests e2e

```
e2e/f4_test.go                           # NOUVEAU — 4 tests edge cases
```

### Partie C — Smoke tests

```
smoke/smoke_test.go                      # Ajouter 4 smoke par workflow + override agent
smoke/matrix_test.go                     # NOUVEAU — matrice workflow × agent
smoke/helpers_test.go                    # Ajouter smokeAgent() helper
Makefile                                 # Ajouter targets smoke-matrix
```

### Partie D — Docs et policy

```
docs/known-limitations.md                # NOUVEAU
CONTRIBUTING.md                          # NOUVEAU ou compléter
```

### Fichiers à NE PAS toucher

```
internal/                                # Pas de changement au code de production
cmd/                                     # Pas de changement au CLI
```

---

## 3. Partie A — Tests unitaires : plan détaillé

### `internal/engine/budget_test.go` (NOUVEAU — 6 tests)

| Test | Vérifie |
|------|---------|
| `TestBudgetTracker_NewWithZeroBudget` | Budget 0 = pas de limite |
| `TestBudgetTracker_AddCostUnderBudget` | Pas d'erreur sous le budget |
| `TestBudgetTracker_AddCostExceedsCookBudget` | `BudgetExceededError` avec scope "run" |
| `TestBudgetTracker_AddCostExceedsStepBudget` | `BudgetExceededError` avec scope "step" |
| `TestBudgetTracker_WarningIfUnavailable` | Warning quand cost=0 (provider ne reporte pas) |
| `TestBudgetTracker_CumulativeTracking` | Plusieurs AddCost s'accumulent correctement |

### `internal/engine/parallel_test.go` (NOUVEAU — 7 tests)

| Test | Vérifie |
|------|---------|
| `TestBuildParallelUnits_FromPlanTasks` | N tasks → N units avec bon step path |
| `TestBuildParallelUnits_WithoutForeach` | Steps parallèles sans foreach → 1 unit par step |
| `TestInferOutputMode_AllDiff` | Tous diff → "diff" |
| `TestInferOutputMode_Mixed` | Mix → "diff" |
| `TestFileIntersection_NoOverlap` | Deux listes disjointes → vide |
| `TestFileIntersection_WithOverlap` | Overlap → fichiers communs |
| `TestFileIntersection_Empty` | Liste vide → vide |

### `internal/engine/restart_test.go` (NOUVEAU — 4 tests)

| Test | Vérifie |
|------|---------|
| `TestJoinStepPath_PrefixAndName` | "build" + "impl" → "build/impl" |
| `TestJoinStepPath_EmptyPrefix` | "" + "impl" → "impl" |
| `TestFindStepIndexByName_Found` | Retourne l'index correct |
| `TestFindStepIndexByName_NotFound` | Retourne -1 |

### `internal/engine/review_test.go` (NOUVEAU — 5 tests)

| Test | Vérifie |
|------|---------|
| `TestParseReviewJSON_Pass` | `{"pass": true, "comment": "ok"}` → pass=true |
| `TestParseReviewJSON_Fail` | `{"pass": false, "comment": "bad"}` → pass=false, comment |
| `TestParseReviewJSON_InvalidJSON` | Contenu non-JSON → erreur |
| `TestParseReviewJSON_FileNotFound` | Fichier absent → erreur |
| `TestParseReviewJSON_MissingPassField` | `{"comment": "x"}` → erreur |

### `internal/engine/replay_test.go` (COMPLÉTER — 3 tests à ajouter)

| Test | Vérifie |
|------|---------|
| `TestLeafFullPath` | Concaténation correcte des segments |
| `TestResolveFromStep_ExactMatch` | Step trouvé dans la recette |
| `TestResolveFromStep_NotFound` | Step absent → erreur avec message |

### `internal/statebag/statebag_test.go` (COMPLÉTER — 16 tests à ajouter)

| Test | Vérifie |
|------|---------|
| `TestUpdateStepAgentMetrics` | Clés tokens_in, tokens_out, cost, turns, duration écrites |
| `TestAddRunCost_Accumulation` | Deux appels → somme correcte |
| `TestIncrementRunTokensIn` | Accumulation |
| `TestIncrementRunTokensOut` | Accumulation |
| `TestIncrementRunRetries` | Deux appels → 2 |
| `TestSetStepCheckResult` | Clé check_result écrite |
| `TestSetStepOutcome` | Clés status et retries écrites |
| `TestDeleteStepOutputsForRestart` | Outputs listés supprimés, autres préservés |
| `TestPrevSessionID_Exists` | Retourne le session_id du step |
| `TestPrevSessionID_NotExists` | Retourne "" |
| `TestSetRunMetric_GetRunMetric` | Set puis Get → même valeur |
| `TestCloneRun_SetRunAll` | Clone → modifier → SetRunAll → visible |
| `TestSerializeRestore_WithMetrics` | Serialize/Restore préserve les métriques step |
| `TestResetGroup_PreservesRunMetrics` | ResetGroup ne touche pas run.* |
| `TestGraft_PreservesRunMetrics` | Graft ne touche pas run.* du parent |
| `TestFormatCostUSDString` | Formatage correct |

### `internal/cook/store_test.go` (NOUVEAU — 8 tests)

| Test | Vérifie |
|------|---------|
| `TestEnsureCookDir` | Crée le répertoire, idempotent |
| `TestWriteReadStatus` | Write puis Read → même status |
| `TestWriteStatusWithSteps` | JSON contient steps_count |
| `TestListCooks_Empty` | Pas de run → liste vide |
| `TestListCooks_Multiple` | Triés par date desc |
| `TestFindLatestPassingCook` | Retourne le plus récent avec status "pass" |
| `TestFindLatestPassingCook_NonePass` | Aucun pass → erreur |
| `TestWriteStateBag` | JSON valide et restorable |

### `internal/ledger/reader_test.go` (NOUVEAU — 5 tests)

| Test | Vérifie |
|------|---------|
| `TestFindInProgressCook_NoRuns` | Retourne "" |
| `TestFindInProgressCook_AllCompleted` | Retourne "" |
| `TestFindInProgressCook_OneInProgress` | Retourne le bon dir |
| `TestReadStatus_HappyPath` | Parse un manifest et construit le snapshot |
| `TestReadStatus_CompatCookStarted` | Parse l'ancien format `cook_started` |

### `internal/report/` (NOUVEAU — 6 tests minimaux)

| Test | Vérifie |
|------|---------|
| `TestRenderCookReport_NoPanic` | Un CookReport se rend sans panic |
| `TestRenderAggregateReport_NoPanic` | Un AggregateReport se rend sans panic |
| `TestFormatDuration` | 1234ms → "1.2s", 125000ms → "2m5s" |
| `TestFormatIntThousands` | 1200 → "1.2k" |
| `TestParseStdoutLine_Claude` | Event Claude parsé |
| `TestProviderForAgent` | "claude-sonnet" → Claude |

**Total tests unitaires : ~60 tests dans 10 fichiers.**

---

## 4. Partie B — 4 tests e2e edge cases

### F4-E2E-1 : Empty plan (0 items)

```
Setup  : workflow avec decompose step, stub produit [] (plan JSON vide)
         Le foreach référence decompose
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0 ou exit non-zero avec erreur explicite
         Pas de boucle infinie, pas de hang
         Si exit 0 : le run skip le foreach et passe à quality gate
         Si exit non-zero : stderr contient un message sur le plan vide
```

### F4-E2E-2 : Error context non-accumulation

```
Setup  : workflow avec retry: 3, stub échoue aux 3 attempts avec stderr différent :
         attempt 1 stderr = "ERROR_AAA"
         attempt 2 stderr = "ERROR_BBB"
         attempt 3 stderr = "ERROR_CCC"
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : le context file du 3ème attempt contient "ERROR_BBB" (erreur du 2ème)
         le context file du 3ème attempt ne contient PAS "ERROR_AAA" (pas d'accumulation)
         le context file du 2ème attempt contient "ERROR_AAA" et ne contient PAS "ERROR_BBB"
```

### F4-E2E-3 : Resume au milieu d'un foreach

```
Setup  : workflow tdd-like avec decompose → foreach 3 items → impl
         stub: item 1 pass, item 2 FATAL (circuit breaker après retries épuisés), item 3 non atteint
Entrée : gump run spec.md --workflow custom --agent-stub → exit 1
         Modifier le stub pour que item 2 passe
         gump run --resume --agent-stub → exit 0
Vérif  : le resume ne ré-exécute PAS item 1 (déjà pass)
         le resume reprend à item 2
         item 3 est exécuté après item 2
         manifest du resume contient run_resumed event
```

### F4-E2E-4 : Resume avec parallel branch FATAL

```
Setup  : workflow parallel: true avec 2 branches, stub: branch 1 pass, branch 2 FATAL
Entrée : gump run → exit 1
         Modifier le stub pour que branch 2 passe
         gump run --resume → exit 0
Vérif  : le resume ne ré-exécute PAS branch 1
         le resume relance branch 2
```

### F4-E2E-5 : Resume après replan FATAL

```
Setup  : workflow avec retry: 2, strategy: [same, "replan: claude-sonnet"]
         stub: attempt 1 fail (gate), attempt 2 = replan produit un plan, sous-tâches du replan FATAL
Entrée : gump run → exit 1 (circuit breaker sur le replan)
         Modifier le stub pour que le replan passe
         gump run --resume → exit 0
Vérif  : le resume reprend dans le contexte du replan (pas le step original)
         manifest du resume contient run_resumed
```

### F4-E2E-6 : Non-régression

```
Vérif  : go test ./... passe
```

---

## 5. Partie C — Smoke tests

### 4 smoke par workflow manquant

#### TestSmokeBugfixLive

```
Setup  : repo avec bug intentionnel (Multiply retourne a+b au lieu de a*b)
         Code + test qui fail déjà committés
         spec = "Bug: Multiply returns a+b instead of a*b. Fix it."
Entrée : gump run spec.md --workflow bugfix
Vérif  : exit 0, assertCookPass, applyAndReset, assertGoTestPasses
```

#### TestSmokeRefactorLive

```
Setup  : repo avec code fonctionnel mal structuré (tout dans main.go, 2 fonctions + tests)
         spec = "Refactor: extract Add and Subtract into a calc package"
Entrée : gump run spec.md --workflow refactor
Vérif  : exit 0, assertCookPass, applyAndReset, assertGoTestPasses
```

#### TestSmokeCheap2SotaLive

```
Setup  : spec triviale (une seule fonction + test)
Entrée : gump run spec.md --workflow cheap2sota
Vérif  : exit 0, assertCookPass
Note   : cheap devrait passer au premier attempt → pas d'escalade → rapide et pas cher
```

#### TestSmokeAdversarialReviewLive

```
Setup  : spec triviale
Entrée : gump run spec.md --workflow adversarial-review
Vérif  : exit 0, assertCookPass, manifest contient des steps avec output=review
Guard  : skip si GUMP_SMOKE_BUDGET < 10.00
```

### `make smoke AGENT=X` — forcer un provider

Variable d'environnement `GUMP_SMOKE_AGENT`. Si non vide, tous les tests single-agent utilisent ce provider au lieu du défaut du workflow.

```makefile
.PHONY: smoke smoke-matrix smoke-full

smoke:
	GUMP_SMOKE_AGENT=$(AGENT) go test ./smoke/ -tags=smoke -v -timeout 30m -count=1

smoke-matrix:
	@for agent in claude codex gemini qwen opencode cursor; do \
		echo "=== Smoke: $$agent ===" ; \
		GUMP_SMOKE_AGENT=$$agent go test ./smoke/ -tags=smoke -v -timeout 30m -count=1 2>&1 | tail -20 ; \
		echo "" ; \
	done

smoke-full:
	GUMP_SMOKE_BUDGET=20.00 go test ./smoke/ -tags=smoke -v -timeout 60m -count=1
```

Mapping cheap/sota par provider :

| Provider | Cheap (défaut) | SOTA (`GUMP_SMOKE_TIER=sota`) |
|---|---|---|
| claude | claude-haiku | claude-sonnet |
| codex | codex-gpt54-mini | codex-gpt54 |
| gemini | gemini-flash | gemini-pro |
| qwen | qwen | qwen-plus |
| opencode | opencode-haiku | opencode-sonnet |
| cursor | cursor-composer | cursor-composer-2 |

**Helper** :

```go
func smokeAgent(t *testing.T) string {
    agent := os.Getenv("GUMP_SMOKE_AGENT")
    if agent == "" { return "" }
    requireAgent(t, agent)
    return cheapModelFor(agent)
}
```

Tests multi-provider skippés quand `GUMP_SMOKE_AGENT` est set.

### Matrice `make smoke-matrix`

`TestSmokeMatrix` : pour chaque agent installé, lance freeform + tdd + bugfix. Tableau de sortie :

```
             claude  codex  gemini  qwen  opencode  cursor
  freeform      ✓      ✓      ✓      ✓      ✓        ✓
  tdd           ✓      ✓      ✗      ✓      ✓        ✓
  bugfix        ✓      ✓      ✓      ✗      ✓        ✓
```

Protégé par `GUMP_SMOKE_MATRIX=1`. Pas lancé par `make smoke`.

### Guard de budget

`GUMP_SMOKE_BUDGET` (défaut 2.00). Les workflows dont `max_budget` dépasse le smoke budget sont skippés. `make smoke-full` met 20.00.

---

## 6. Partie D — Edge cases documentés + Policy bugfix

### `docs/known-limitations.md`

| ID | Edge case | Comportement | Test |
|----|-----------|-------------|------|
| L-001 | Cross-provider session resume | Fresh session forcée | `TestE2E5CrossProviderQwenOpenCode` |
| L-002 | Parallel merge conflict | Circuit breaker | `TestE2EParallelP3` |
| L-003 | Agent timeout mid-stream | agent_killed + métriques partielles | G4 e2e (F1 Partie H) |
| L-004 | Provider CLI not in PATH | doctor gris, run erreur | doctor tests |
| L-005 | Empty plan (0 items) | Skip foreach ou erreur explicite | `F4-E2E-1` |
| L-006 | Plan with 100+ items | Budget guard protège | pas de test (edge rare) |
| L-007 | Agent invalid JSON for plan | Gate schema fail, retry | `TestStep5V7_SchemaPass` |
| L-008 | Agent empty diff | Gate compile/test, retry | pas de test e2e isolé |
| L-009 | Apply on failed run | Erreur "no completed run" | `TestApplyFailsWhenNoCompletedRun` |
| L-010 | Multiple concurrent runs | Worktrees distincts | `TestTwoCooksCreateTwoWorktrees` |
| L-011 | Circular workflow | Erreur au parsing | `TestM1_5_CycleDetectionRestartFrom` |
| L-012 | Resume on passed run | Erreur "no fatal step" | `TestG5_E2E_6_ResumePassRefused` |
| L-013 | `.gump/` in submodule | Pas supporté | documentation only |
| L-014 | `{steps.nonexistent.output}` | Chaîne vide | template tests |
| L-015 | Network failure during telemetry | Silencieux (best-effort) | `TestG3_E2E4_OptOut` |
| L-016 | Session-id format invalide | Fallback Launch + warning | compat mode tests |
| L-017 | Turn tracker switch provider | Violation encapsulation (F3 résout) | — |
| L-018 | Resume mid-foreach | Reprend l'item FATAL | `F4-E2E-3` |

### Policy bugfix dans CONTRIBUTING.md

```markdown
## Bug Fix Policy

Every bug fix must include a regression test.

1. **Reproduce**: Write a test that fails before the fix.
2. **Fix**: Implement the fix.
3. **Verify**: The test passes after the fix.
4. **Document**: Add edge case to `docs/known-limitations.md` if relevant.

Test naming: prefix `TestBug_` (e.g. `TestBug_EmptyPlanInfiniteLoop`).
Location: package bug → unit test, cross-package → e2e.
```

---

## 7. Critères de succès

1. `go build -o gump .` compile.
2. ~60 tests unitaires ajoutés dans 10 fichiers.
3. Ratio test/source ≥ 30% sur `internal/engine/` (vs 11% avant).
4. Ratio test/source ≥ 40% sur `internal/statebag/` (vs 16% avant).
5. `internal/cook/store_test.go` et `internal/ledger/reader_test.go` existent.
6. Les 6 tests e2e F4 passent (5 edge cases + non-régression).
7. Les 4 workflows manquants ont un smoke test (bugfix, refactor, cheap2sota, adversarial-review).
8. `make smoke AGENT=qwen` lance les tests single-agent avec Qwen.
9. `make smoke-matrix` produit un tableau workflow × agent.
10. `docs/known-limitations.md` existe avec ≥ 18 entries.
11. CONTRIBUTING.md contient la policy bugfix.
12. `go test ./...` passe.
13. `make smoke` passe (avec les agents installés).