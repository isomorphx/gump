# Fix : Blast Radius Severity + Group Retry Session Reset

> Spec destinée à Cursor Composer. Pas de code dans ce document.
> Prérequis : `go test ./...` passe.
> Objectif : (A) le blast radius enforcement devient configurable par workflow, (B) le group retry force une session fresh sur tous les steps internes pour éviter la désynchronisation worktree/session.

---

## Partie A — Blast Radius Severity Levels

### Problème

Le blast radius enforcement est binaire : si `item.files` est non vide, tout fichier modifié hors de la liste cause un gate fail. Quand le plan produit par un agent de décomposition omet un fichier (ex: `engine.go` nécessaire pour une intégration mais oublié dans le plan), le run boucle jusqu'au circuit breaker sans possibilité de récupération.

### Syntaxe workflow

```yaml
name: my-workflow
blast_radius: warn    # enforce | warn | off

steps:
  - name: impl
    agent: claude-sonnet
    output: diff
    prompt: "..."
```

Le champ `blast_radius` est au niveau **workflow** (pas au niveau step). Il s'applique à tous les steps du workflow.

### Trois niveaux

| Niveau | Comportement | Use case |
|--------|-------------|----------|
| `enforce` | Gate fail si un fichier est hors `item.files`. **Défaut** (backward compat). | Workflows stricts avec blast radius fiable |
| `warn` | Log warning dans le ledger + stderr, pas de gate fail. Le run continue. | Workflows avec plan agent (blast radius best-effort) |
| `off` | Aucun check. Pas de warning. | Freeform, expérimentation |

### Warning format

Quand `blast_radius: warn` et qu'un fichier est hors scope :

```
⚠ blast radius warning: files modified outside task.files scope:
  - internal/engine/engine.go (not in allowed list)
Allowed: internal/engine/error_digest.go, internal/context/builder.go
```

Le warning est émis sur stderr (display layer) et dans le ledger comme un event `blast_radius_warning`.

### Ledger event

```json
{"type": "blast_radius_warning", "step": "build/impl", "violators": ["internal/engine/engine.go"], "allowed": ["internal/engine/error_digest.go"]}
```

### Quand `item.files` est vide

Si `item.files` est vide ou absent, aucun check quel que soit le niveau. C'est le comportement actuel inchangé.

### Blast radius Partie A

```
internal/recipe/types.go               # Ajouter BlastRadius string sur Recipe
internal/recipe/parser.go              # Parser blast_radius (défaut "enforce")
internal/recipe/validator.go           # Valider les 3 valeurs
internal/engine/engine.go              # Lire recipe.BlastRadius, conditionner le check
internal/ledger/events.go              # Ajouter BlastRadiusWarning event
internal/engine/display.go             # Afficher le warning
```

### Tests e2e Partie A

#### A-E2E-1 : enforce (défaut, backward compat)

```
Setup  : workflow sans blast_radius (défaut), item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : gate fail avec "blast radius violation" (comportement actuel inchangé)
```

#### A-E2E-2 : warn

```
Setup  : workflow avec blast_radius: warn, item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0 (pas de gate fail), stderr contient "blast radius warning", manifest contient blast_radius_warning event
```

#### A-E2E-3 : off

```
Setup  : workflow avec blast_radius: off, item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0, stderr ne contient PAS "blast radius", manifest ne contient PAS blast_radius_warning
```

#### A-E2E-4 : empty files (aucun check quel que soit le niveau)

```
Setup  : workflow avec blast_radius: enforce, item.files = [], stub crée n'importe quoi
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0 (pas de check)
```

#### A-E2E-5 : dry-run affiche le niveau

```
Setup  : workflow avec blast_radius: warn
Entrée : gump run spec.md --workflow custom --dry-run
Vérif  : stdout contient "blast_radius: warn"
```

---

## Partie B — Group Retry Force Session Fresh

### Problème

Quand un group retry se déclenche (ex: une review fail dans un workflow adversarial-review), le worktree est reset au commit pré-groupe. Mais les sessions agent des steps internes ne sont **pas** invalidées. Au retry, un step avec `session: reuse` ou `session: reuse-on-retry` reprend sa session — l'agent croit que son travail est encore dans le worktree alors qu'il a été effacé par le reset.

Conséquence observée : l'agent dit "Already implemented, no changes needed", le worktree est vide, les reviews détectent que rien n'a été implémenté, le run boucle jusqu'au circuit breaker.

### Comportement attendu

**Au début de chaque group retry** (après le worktree reset, avant la ré-exécution des steps internes) :

1. Invalider les session IDs de tous les steps internes au groupe dans le state bag.
2. Les steps internes démarrent avec une **session fresh** au group retry, même si leur YAML dit `session: reuse` ou `session: reuse-on-retry`.
3. Émettre un événement `group_retry_sessions_reset` dans le ledger.

### Règle précise

| Contexte | `session: reuse` | `session: reuse-on-retry` | `session: fresh` |
|----------|-----------------|--------------------------|-----------------|
| Step retry (même step) | Reuse | Reuse (attempt > 1) | Fresh |
| Group retry (groupe parent) | **Fresh** (worktree reset) | **Fresh** (worktree reset) | Fresh |

La distinction est : **step retry** = le step lui-même a échoué et retry → la session est valide (le worktree n'est pas reset au step retry, seulement au commit pré-step). **Group retry** = un step du groupe a échoué, tout le groupe recommence → le worktree est reset au commit pré-groupe → toutes les sessions du groupe sont invalides.

### Ledger event

```json
{"type": "group_retry_sessions_reset", "group": "build", "attempt": 2, "invalidated_sessions": ["build/impl", "build/reviews/arch-review"]}
```

### Implémentation

Dans `internal/engine/engine.go`, au moment du group retry (dans la boucle de `executeSteps` quand un groupe composite fail et que le retry s'applique) :

1. Après le worktree reset (`git reset --hard <preGroupCommit>` + `git clean -fd`).
2. Avant la ré-exécution des steps internes.
3. Appeler `e.StateBag.ClearSessionIDsForGroup(groupPath)` — une nouvelle méthode qui met à "" tous les session_id des steps dont le path commence par le group path.
4. Vider le `lastSessionByAgent` map local au groupe.

Le `lastSessionByAgent` est déjà un map local passé en paramètre à `executeSteps`. Au group retry, ce map est recréé vide → les steps ne trouvent pas de session précédente → `Launch` au lieu de `Resume`. C'est peut-être déjà le cas — **vérifier**.

Si le `lastSessionByAgent` est déjà recréé vide au group retry, le seul fix nécessaire est de clear les session IDs dans le state bag (pour que `session: reuse` qui cherche via le state bag ne trouve pas l'ancienne session invalide).

### Blast radius Partie B

```
internal/statebag/statebag.go          # Ajouter ClearSessionIDsForGroup(groupPath string)
internal/statebag/statebag_test.go     # Test unitaire
internal/engine/engine.go              # Appeler ClearSessionIDsForGroup au group retry
internal/ledger/events.go              # Ajouter GroupRetrySessionsReset event
```

### Tests e2e Partie B

#### B-E2E-1 : Group retry force session fresh

```
Setup  : workflow adversarial-review-like avec group retry: 2
         stub: step impl passe, review fail au premier group attempt
         Au deuxième group attempt, vérifier que impl a une session fresh
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : manifest contient group_retry_sessions_reset event
         Le agent_launched du step impl au 2ème group attempt a session_id="" (fresh, pas reuse)
```

#### B-E2E-2 : Step retry préserve la session (pas de régression)

```
Setup  : workflow avec step retry: 2 (pas de group retry), session: reuse-on-retry
         stub: step fail au 1er attempt, pass au 2ème
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : Le agent_launched du 2ème attempt a le session_id du 1er attempt (reuse, pas fresh)
         Pas de group_retry_sessions_reset event
```

#### B-E2E-3 : Non-régression

```
Vérif  : go test ./... passe
```

---

## Critères de succès

1. `go build -o gump .` compile.
2. `blast_radius: enforce` est le défaut (backward compat).
3. `blast_radius: warn` produit un warning sans bloquer le run.
4. `blast_radius: off` ne fait aucun check.
5. Les tests blast radius existants (`TestE2EBlastRadiusBR1`, `BR2`) passent toujours.
6. Le group retry force une session fresh sur tous les steps internes.
7. Le step retry préserve la session (pas de régression sur `reuse-on-retry`).
8. Les 8 tests e2e passent (5 Partie A + 3 Partie B).
9. `go test ./...` passe.