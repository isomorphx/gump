# Gump — Spec F1 : Stabilité Runtime — Prérequis Beta

> Spec destinée à Cursor Composer. Pas de code dans ce document. Prérequis : G1-G5 implémentées, `go test ./...` passe. Objectif : corriger les problèmes de stabilité identifiés en conditions réelles — context explosion sur retry, doctor inutilisable en cold start, traces de rebranding incomplètes, frictions DX, et surtout les tests G4 manquants. Résultat attendu : un runtime fiable pour les premiers beta testers.

---

## 1. Vue d'ensemble

### Partie A — Error Digest : extraction intelligente des erreurs pour retry

Le contexte injecté au retry explose quand le stderr de l'agent contient un stack trace ou un log de build complet (observé : 3 retries → context window saturée → $100 de run). Remplacer la troncature brute (`TruncateLines` dans `context/builder.go`) par un extracteur intelligent qui isole le message d'erreur clé.

### Partie B — Doctor hardening

Le timeout de 10 secondes du harness est trop court en cold start (premier appel d'un agent = auth, téléchargement modèle). Ajouter un flag `--<provider>` pour ne tester qu'un seul provider. Uniformiser l'affichage entre Cursor et les autres.

### Partie C — Init implicite

Il n'existe pas de commande `gump init`. Le répertoire `.gump/` est créé par `internal/cook/cook.go` au moment du run. Créer `gump init` comme commande facultative, et rendre le init implicite au début de `gump run`.

### Partie D — Bug fix : Cobra SilenceUsage

`cmd/root.go` ne configure ni `SilenceUsage = true` ni `SilenceErrors = true` sur rootCmd. Conséquence : cobra affiche le usage complet (help) à chaque erreur de commande. C'est un bug confirmé par l'audit.

### Partie E — Vérification next steps post-run

L'implémentation existe dans `internal/engine/display.go` (lignes 163, 171) et `cmd/run.go` (lignes 120, 200, 406). Vérifier que les messages sont corrects et ajouter les tests e2e manquants.

### Partie F — Alias `--wf`

Ajouter `--wf` comme alias de `--workflow` dans la commande `gump run`.

### Partie G — Rebranding final : zéro occurrence de `pudding`, `recipe`, `cook`

Audit exhaustif et remplacement de toutes les mentions résiduelles. L'audit identifie **582** occurrences de `pudding`, **509** de `cook` (hors `internal/cook/`), **268** de `recipe` (hors `internal/recipe/`). Cela inclut les noms de fonctions (`writeRecipe`, `FindInProgressCook`, `ErrCookAborted`), les structs (`CookDir`, `CookID`), les events ledger (`cook_started`, `cook_completed`), les fichiers de test (`.pudding-test-scenario.json`), et les commentaires.

### Partie H — Tests G4 manquants

L'audit révèle que `e2e/g4_test.go` ne contient qu'un seul test sur les 14 prévus par la spec G4. Les fonctionnalités sont implémentées (code présent dans display.go, template.go, etc.) mais non verrouillées par des tests. Écrire les 13 tests e2e manquants.

### Partie I — Robustesse crash et corruption

Verrouiller par des tests le comportement de Gump quand les choses cassent : manifest avec dernière ligne tronquée (crash mid-write), ligne non-JSON au milieu du manifest, state-bag.json corrompu au resume, Ctrl+C mid-run. L'audit confirme que les readers tolèrent déjà les lignes invalides — les tests verrouillent ce comportement. Le signal handler Ctrl+C nécessite un changement de code.

---

## 2. Blast Radius

### Partie A — Error Digest

```
internal/engine/error_digest.go          # NOUVEAU — extraction key message, troncature intelligente
internal/engine/error_digest_test.go     # NOUVEAU — tests unitaires par techno (Go, Node, Python, Rust, générique)
internal/context/builder.go              # Appeler ErrorDigest au lieu de TruncateLines pour les erreurs
internal/config/config.go                # Ajouter error_context.max_lines (défaut 20)
internal/config/loader.go               # Lire max_lines
```

### Partie B — Doctor hardening

```
cmd/doctor.go                            # Timeout 60s (actuellement 10s ligne 89 + 5 harness hardcodés), flag --<provider>, affichage uniforme
```

### Partie C — Init implicite

```
cmd/init.go                              # NOUVEAU — commande gump init
cmd/run.go                               # Appeler ensureInit() avant le run
internal/cook/cook.go                    # Extraire la logique de création .gump/ dans une fonction réutilisable
```

### Partie D — Cobra SilenceUsage

```
cmd/root.go                              # Ajouter SilenceUsage = true, SilenceErrors = true sur rootCmd
```

### Partie I — Robustesse crash et corruption

```
internal/ledger/reader.go                # Vérifier que les readers tolèrent les lignes tronquées/invalides (déjà le cas — audit confirmé)
internal/engine/resume.go                # Vérifier que Restore(state-bag) gère le JSON invalide avec erreur explicite
internal/engine/engine.go                # Ajouter un signal handler global (SIGINT/SIGTERM) qui appelle finishCook avant exit
cmd/run.go                               # Propager le signal handler
```

### Partie E — Next steps post-run

```
Aucun fichier modifié — vérification uniquement. Tests à écrire dans Partie H.
```

### Partie F — Alias --wf

```
cmd/run.go                               # Ajouter alias --wf pour --workflow
```

### Partie G — Rebranding final

```
**Tous les fichiers du repo** — audit via grep, renommage manuel.
Cibles prioritaires (les plus touchés) :
  internal/ledger/reader.go              # CookDir, CookID, FindInProgressCook, cook_started/completed
  internal/ledger/ledger.go              # cookDir, cookID
  internal/ledger/index.go               # CookID dans JSON
  internal/engine/engine.go              # ErrCookAborted, Cook, finishCook, printCookTotal
  cmd/run.go                             # messages "cook", "recipe"
  e2e/*.go                               # .pudding-test-scenario.json → .gump-test-scenario.json
                                         # writeRecipe → writeWorkflow
                                         # toutes les refs pudding/recipe/cook
  smoke/*.go                             # idem
  internal/context/builder.go            # refs pudding dans comments/templates
  internal/sandbox/worktree.go           # .pudding/ dans paths
```

### Partie H — Tests G4 manquants

```
e2e/g4_test.go                           # Ajouter les 13 tests manquants
```

### Fichiers à NE PAS toucher

```
internal/recipe/                         # Le package Go garde son nom "recipe" (détail d'implémentation)
internal/cook/                           # Le package Go garde son nom "cook" (détail d'implémentation)
                                         # Seuls les strings user-facing et comments sont renommés à l'intérieur
internal/agent/*.go                      # Pas de changement aux adapters
internal/validate/                       # Inchangé
internal/telemetry/                      # Inchangé
```

---

## 3. Partie A — Error Digest : Détail

### Problème

La troncature actuelle (`TruncateLines` dans `context/builder.go`, ligne 339) fait un split head/tail mécanique à `max_error_chars` (défaut 2000). Elle coupe sur des frontières de lignes (mieux que du brut), mais :
- Le key message est souvent au milieu ou à la fin du log — le head le rate.
- Le tail contient souvent des lignes de résumé inutiles (`FAIL`, `exit status 1`).
- Aucun filtrage des lignes non-informatiques (barres de progression, warnings répétés, lignes de download).
- L'accumulation entre retries n'est pas gérée au niveau du digest (mais elle est gérée au niveau du context builder — seul le dernier attempt est injecté, vérifié dans le code step 6).

### Nouveau comportement : ErrorDigest

L'ErrorDigest remplace `TruncateLines` **uniquement pour le champ `{error}`** dans le prompt de retry. `TruncateLines` reste pour `{diff}` (le diff n'a pas de key message à extraire — la troncature brute est adaptée).

**Couche 1 — Key message** : extraction du message d'erreur principal. Heuristique déterministe par techno :

| Techno | Pattern de scan (de bas en haut) | Extraction |
|--------|----------------------------------|------------|
| Go | Ligne commençant par `FAIL` ou `--- FAIL:`, ou contenant `panic:`, `error:` (hors `error_test.go`) | La ligne + la ligne précédente (fichier:ligne si dispo) |
| Node/npm | Lignes contenant `ERR!`, `Error:`, `SyntaxError:`, `TypeError:`, `ReferenceError:` | La ligne + 2 lignes de contexte |
| Python | Dernière ligne après le dernier `Traceback (most recent call last):` | La ligne d'erreur + la ligne `File "..."` la plus proche |
| Rust/Cargo | Lignes commençant par `error[E` | La ligne complète |
| Java/Kotlin | Lignes contenant `Exception:`, `Error:` (hors stack frames `at ...`) | La ligne + la première ligne `at ...` |
| Générique | Aucun pattern reconnu | Les 5 dernières lignes non-vides de stderr |

La détection de techno est basée sur le contenu du stderr (pas sur la config du projet). Si plusieurs patterns matchent, prendre le plus spécifique.

**Couche 2 — Contexte tronqué** : les N dernières lignes significatives du stderr, filtrées :
- Supprimer les lignes vides consécutives (garder une seule).
- Supprimer les barres de progression (`[=====>    ]`, `███░░░`, etc.) — regex `[\[=\->#\s\]]{10,}` ou `[█░▓▒]{3,}`.
- Supprimer les lignes répétées consécutives (garder la première + `... (repeated N times)`).
- Supprimer les lignes de warning redondantes (`npm warn`, `go: downloading` — non-erreurs).

N = `error_context.max_lines` (défaut 20, configurable dans gump.toml).

**Couche 3 — Hard cap** : après extraction et filtrage, tronquer à `error_context.max_error_chars` (défaut 2000). Ce cap est un filet de sécurité.

### Format de sortie

```
Error: cannot find module 'foo' in pkg/bar/handler.go:42

Context (last 15 lines):
  cmd/server.go:12:3: undefined: foo.Handler
  cmd/server.go:18:9: too many arguments in call to bar.New
  FAIL	github.com/example/app/cmd [build failed]
```

### Intégration

Dans `internal/context/builder.go`, remplacer l'appel à `TruncateLines` pour le champ erreur par un appel à `DigestError`. Le champ diff continue d'utiliser `TruncateLines`.

### Règle d'accumulation

Vérifiée dans le code : seul le dernier attempt est injecté. Pas de changement nécessaire.

---

## 4. Partie B — Doctor hardening : Détail

### État actuel

- Timeout : 10s hardcodé dans chaque harness (lignes 89, 177, 234, 295, 360 de `cmd/doctor.go`).
- Pas de flag provider.
- Format d'affichage : quasi-identique pour tous les providers sauf alignement variable.

### Changements

**Timeout** : remplacer tous les `10 * time.Second` par `60 * time.Second`. Constante unique en haut du fichier.

**Flag provider ciblé** :

```bash
gump doctor                    # teste tous les providers (séquentiel)
gump doctor --claude           # ne teste que Claude (+ git toujours)
gump doctor --qwen             # ne teste que Qwen (+ git)
```

Un flag bool par provider connu dans cobra. Si aucun flag → tous. Si plusieurs flags → les providers demandés.

**Affichage uniforme** :

```
gump doctor

  git:        ✓ git 2.43.0
  claude:     ✓ claude 1.0.17 (harness ok)
  codex:      ✓ codex 0.9.2 (harness ok)
  gemini:     — not installed
  qwen:       ✓ qwen 2.1.0 (harness ok)
  opencode:   ✓ opencode 0.3.1 (harness ok)
  cursor:     ✓ cursor-agent 2026.03.20 (harness ok)
```

Colonnes alignées. Statuts : `✓` vert, `✗` rouge, `⚠` jaune (compat), `—` gris (absent).

---

## 5. Partie C — Init implicite : Détail

### État actuel

Pas de commande `gump init`. Le répertoire `.gump/` est créé par `NewCook()` dans `internal/cook/cook.go` (appel à `ensureGitignoreStateDir`). Si le run échoue avant `NewCook` (parsing workflow, etc.), `.gump/` n'existe pas.

### Changements

Créer `cmd/init.go` avec la commande `gump init`. Extraire la logique de `ensureGitignoreStateDir` dans une fonction `EnsureInit(repoRoot string) error` dans `internal/cook/` (ou un nouveau package `internal/init/`).

Au début de `gump run` (dans `cmd/run.go`), appeler `EnsureInit()` avant toute autre logique. Si `.gump/` existe déjà, no-op. Si créé, afficher `Initialized .gump/ in <repo-root>` sur stderr.

---

## 6. Partie D — Cobra SilenceUsage : Détail

### Bug

Cobra affiche automatiquement le usage quand une commande retourne une erreur. Ligne 33 de `cmd/root.go` : `rootCmd` n'a ni `SilenceUsage` ni `SilenceErrors`.

### Fix

Ajouter après la déclaration de `rootCmd` :

```
rootCmd.SilenceUsage = true
rootCmd.SilenceErrors = true
```

Et dans chaque handler d'erreur (`RunE` des commandes), s'assurer que les messages d'erreur contiennent uniquement une suggestion textuelle (`Run 'gump ...' for ...`), jamais un dump du help.

---

## 7. Partie E — Next steps post-run : Détail

L'implémentation existe. Vérifier via les tests e2e (Partie H, tests G4-E2E-8 et G4-E2E-9).

---

## 8. Partie F — Alias `--wf` : Détail

Ajouter l'alias cobra sur le flag `--workflow` dans `cmd/run.go`.

---

## 9. Partie G — Rebranding final : Détail

### Scope

582 occurrences `pudding`, 509 `cook` (hors package), 268 `recipe` (hors package). Le rebranding G1 a renommé les commandes CLI mais pas le code interne.

### Méthode

1. `grep -rn "pudding\|Pudding\|PUDDING" --include="*.go" --include="*.yaml" --include="*.toml" --include="*.md" --include="Makefile" .` → liste exhaustive.
2. Pour chaque occurrence, classifier : compat (garder), user-facing (renommer), interne (renommer).
3. Renommer par batch logique : d'abord les structs/fonctions (cascade sur les imports), puis les strings, puis les tests.
4. `go build ./...` après chaque batch.

### Règle du fichier scénario stub

`.pudding-test-scenario.json` → `.gump-test-scenario.json` dans tous les tests et dans le stub agent.

### Exceptions

- Package Go `internal/recipe/` et `internal/cook/` : garder le nom du package.
- Aliases deprecated dans cobra (`--recipe`) : garder pour compat.
- Fallback paths (`.pudding/recipes/`) : garder pour compat avec warning.
- Events ledger anciens (`cook_started`, `cook_completed`) : garder la lecture pour compat (G1 E2E 6).

---

## 9bis. Partie I — Robustesse crash et corruption : Détail

### Manifest crash resilience

L'audit du code confirme que les 3 readers (`parseManifestForResume`, `ReadStatus`, `ReadReplayInfo`) font déjà `json.Unmarshal → continue` sur les lignes invalides. Le manifest est donc déjà tolérant à une dernière ligne tronquée ou à une ligne corrompue au milieu. **Pas de changement de code nécessaire** — uniquement les tests e2e pour verrouiller ce comportement.

### State bag corrompu

`statebag.Restore(data)` appelle `json.Unmarshal`. Si le JSON est invalide, il retourne une erreur. Vérifier que `RunResume` dans `internal/engine/resume.go` propage cette erreur proprement au lieu de panic. Si c'est déjà le cas, **pas de changement de code** — uniquement le test e2e.

### Ctrl+C mid-run — signal handler global

Aujourd'hui, `SIGINT` n'est géré que dans le HITL pause (`hitlPauseAfterSuccess`). Si l'utilisateur fait Ctrl+C pendant l'exécution d'un agent (hors HITL), le process Go reçoit SIGINT et termine immédiatement sans appeler `finishCook`. Conséquence : le manifest peut être incomplet (pas de `run_completed`), le state-bag.json peut ne pas être persisté, et le ledger file handle n'est pas fermé proprement.

**Fix** : ajouter un signal handler global dans `Engine.Run()` :

1. `signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)` au début de `Run()`.
2. Dans une goroutine, attendre le signal.
3. Sur réception : kill l'agent en cours (via `Process.Kill()` ou le mécanisme de guard existant), puis laisser le flow normal continuer jusqu'à `finishCook` (qui émet `run_completed` avec status "aborted" et ferme le ledger).
4. Le mécanisme existant de `context.WithCancel` sur les agents devrait propager l'annulation.

L'objectif est que même après un Ctrl+C, le manifest et le state-bag sont dans un état valide et exploitable par `gump report` et `gump run --resume`.

---

## 10. Partie H — Tests G4 manquants : Détail

Les 13 tests suivants sont prévus par la spec G4 mais absents de `e2e/g4_test.go`. Les écrire.

### G4-E2E-1 : Template escaping

```
Setup  : workflow custom avec prompt contenant {{example}} et {steps.first.output}
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : context file contient "{example}" littéral ET la valeur résolue de first.output
```

### G4-E2E-2 : Empty template cleanup

```
Setup  : workflow custom avec {error} et {diff} dans le prompt, premier attempt
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : context file ne contient PAS de lignes vides résiduelles de {error}/{diff}
```

### G4-E2E-3 : Dirty check .gump/ exclusion

```
Setup  : repo git propre, modifier .gump/workflows/custom.yaml (non committed)
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : exit 0 (pas de "working tree is dirty" error)
```

### G4-E2E-4 : Budget accounting — guard kill

```
Setup  : guard max_turns: 2, stub rapporte tokens_in=5000 par turn, 5 turns
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : manifest contient agent_killed avec input_tokens > 0, state-bag.json run.tokens_in > 0
```

### G4-E2E-5 : Budget accounting — cost partial dans run_completed

```
Setup  : guard + on_failure retry: 2, stub attempt 1: guard kill, attempt 2: pass
Entrée : gump run + vérifier run_completed
Vérif  : run_completed.total_cost_usd inclut le coût partiel de l'attempt 1
```

### G4-E2E-6 : Ledger — stdout partiel sur kill

```
Setup  : guard max_turns: 2, stub émet 5 events avant kill
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : le fichier artefact stdout contient au moins 2 lignes NDJSON valides
```

### G4-E2E-7 : Ledger — NDJSON valide

```
Setup  : stub émet du stdout avec des caractères de contrôle (\x1b[31m etc.)
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : chaque ligne du manifest.ndjson est du JSON valide (json.Valid())
```

### G4-E2E-8 : DX — next steps après run réussi

```
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : stderr contient "gump report" ET "gump apply"
```

### G4-E2E-9 : DX — next steps après circuit breaker

```
Setup  : on_failure retry: 1, stub échoue toujours
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : stderr contient "gump report" ET "gump run --replay" ET "gump gc"
```

### G4-E2E-11 : Display — turn-based (default)

```
Setup  : stub émet 3 turns avec Read, Write, Bash events
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : stderr contient "T1", "T2", "T3" (pas de ░░░░░)
```

### G4-E2E-12 : Display — verbose

```
Entrée : gump run spec.md --workflow freeform --agent-stub --verbose
Vérif  : stderr contient "Read" et un path de fichier sous un turn
```

### G4-E2E-13 : Display — tokens par turn

```
Setup  : stub (Claude-like) rapporte tokens par turn
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : stderr contient "tok" (le format compact des tokens)
```

### G4-E2E-14 : Non-régression

```
Vérif  : go test ./... passe
```

---

## 11. Tests e2e (nouveaux F1)

### F1-E2E-1 : Error Digest — Go build error

```
Setup  : workflow avec retry: 2, stub échoue au premier attempt avec stderr contenant
         50 lignes de "go build" output incluant "FAIL" et "error: undefined: foo"
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : le context file du retry contient "undefined: foo" ET ne contient PAS les 50 lignes complètes
```

### F1-E2E-2 : Error Digest — pas d'accumulation

```
Setup  : workflow avec retry: 3, stub échoue à chaque attempt avec stderr différent
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : le context file du 3ème attempt contient uniquement l'erreur du 2ème
```

### F1-E2E-3 : Error Digest — fallback générique

```
Setup  : stub échoue avec un stderr sans pattern reconnu
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : le context file du retry contient les 5 dernières lignes non-vides du stderr
```

### F1-E2E-4 : Doctor — flag provider ciblé

```
Entrée : gump doctor --claude
Vérif  : stdout contient "claude:" ET "git:" ET ne contient PAS "codex:" ni "gemini:" etc.
```

### F1-E2E-5 : Init implicite

```
Setup  : repo git sans .gump/
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : exit 0, .gump/ existe, stderr contient "Initialized"
```

### F1-E2E-6 : Init implicite — idempotent

```
Setup  : repo git avec .gump/ déjà existant et un config.toml custom
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : exit 0, config.toml custom pas écrasé, stderr ne contient PAS "Initialized"
```

### F1-E2E-7 : Cobra — jamais de help dump

```
Entrée : gump run (sans arguments)
Vérif  : stderr ne contient PAS "Usage:" ni "Available Commands:"
```

### F1-E2E-8 : Alias --wf

```
Entrée : gump run spec.md --wf freeform --agent-stub
Vérif  : exit 0
```

### F1-E2E-9 : Ledger — tolérance dernière ligne tronquée

```
Setup  : gump run → pass (manifest complet)
         Tronquer la dernière ligne du manifest.ndjson (simuler un crash mid-write :
         ouvrir le fichier, couper les 20 derniers caractères de la dernière ligne)
Entrée : gump report
Vérif  : exit 0, le report s'affiche (le reader skip la ligne invalide)
         gump run --resume doit aussi fonctionner si la dernière ligne tronquée
         est un step_completed (le reader skip et continue)
```

### F1-E2E-10 : Ledger — ligne non-JSON au milieu

```
Setup  : gump run → pass (manifest complet)
         Insérer une ligne "CORRUPTED_GARBAGE_LINE" au milieu du manifest.ndjson
Entrée : gump report
Vérif  : exit 0, le report s'affiche (le reader skip la ligne invalide)
         Le nombre de steps dans le report est correct (la ligne garbage est ignorée)
```

### F1-E2E-11 : State bag JSON corrompu → resume erreur explicite

```
Setup  : gump run → FATAL (circuit breaker)
         Écraser state-bag.json avec du contenu invalide ("{{not json")
Entrée : gump run --resume
Vérif  : exit non-zero, stderr contient une erreur explicite sur le state-bag
         (pas de panic, pas de stack trace)
```

### F1-E2E-12 : Ctrl+C mid-run → cleanup propre

```
Setup  : workflow avec 2 steps, le premier step est lent (stub avec sleep ou beaucoup d'events)
Entrée : gump run spec.md --workflow custom --agent-stub
         Envoyer SIGINT au process gump pendant l'exécution du premier step
Vérif  : le process termine (exit non-zero)
         manifest.ndjson existe et chaque ligne est du JSON valide
         le worktree existe (pas supprimé)
         state-bag.json existe (persisté avant la sortie, ou absent si le run n'avait pas encore persisté — pas de fichier corrompu à moitié écrit)
Note   : ce test est délicat car il dépend du timing. Le test envoie SIGINT après
         avoir détecté que le premier agent_launched event a été émis (lire le manifest
         en polling).
```

### F1-E2E-13 : Rebranding — zéro pudding user-facing

```
Vérif  : grep -rn "pudding" --include="*.go" retourne uniquement les lignes de compat
```

### F1-E2E-14 : Non-régression

```
Vérif  : go test ./... passe
```

---

## 12. Smoke tests

**`TestSmokeErrorDigestLive`** : workflow tdd avec spec volontairement incomplète → retry → run termine en < $5.

**`TestSmokeDoctorColdStartLive`** : `gump doctor` → pas de timeout, exit 0.

**`TestSmokeDoctorSingleProvider`** : `gump doctor --claude` → stdout ne contient que git et claude.

**`TestSmokeInitImplicitLive`** : `gump run --workflow freeform --agent claude-haiku` dans un repo sans `.gump/` → exit 0, `.gump/` créé.

---

## 13. Critères de succès

1. `go build -o gump .` compile.
2. L'ErrorDigest extrait un key message de < 5 lignes depuis un stderr de 200 lignes.
3. Les retries n'accumulent pas les erreurs des attempts précédents.
4. `gump doctor` ne timeout pas en cold start (60s).
5. `gump doctor --qwen` ne teste que Qwen (+ git).
6. L'affichage doctor est identique pour tous les providers.
7. `gump run` sur un repo sans `.gump/` crée automatiquement la structure.
8. `gump run` (sans args) ne dump jamais le usage complet.
9. Les next steps sont affichés après chaque run.
10. `--wf` fonctionne comme alias de `--workflow`.
11. `grep -rn "pudding" --include="*.go"` ne retourne que du code de compat.
12. Les 13 tests G4 manquants passent.
13. Le manifest avec une dernière ligne tronquée est lisible par report et resume.
14. Le state-bag.json corrompu produit une erreur explicite au resume (pas de panic).
15. Ctrl+C mid-run laisse le manifest dans un état valide.
16. Les 14 tests F1 e2e passent.
17. `go test ./...` passe.