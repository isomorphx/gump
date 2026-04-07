# Fix : Blast Radius Severity + Retry Worktree/Session Policy

> Spec destinée à Cursor Composer. Pas de code dans ce document.
> Prérequis : `go test ./...` passe.
> Objectif : (A) le blast radius enforcement devient configurable par workflow, (B) le retry ne reset le worktree que sur escalade/replan — pas sur `same`. Le group retry suit la même règle. Les sessions sont synchronisées avec le worktree : reset worktree = fresh session, worktree en place = reuse session.

---

## Partie A — Blast Radius Severity Levels

### Problème

Le blast radius enforcement est binaire : si `item.files` est non vide, tout fichier modifié hors de la liste cause un gate fail. Quand le plan produit par un agent de décomposition omet un fichier, le run boucle jusqu'au circuit breaker sans possibilité de récupération.

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

```
⚠ blast radius warning: files modified outside task.files scope:
  - internal/engine/engine.go (not in allowed list)
Allowed: internal/engine/error_digest.go, internal/context/builder.go
```

### Ledger event

```json
{"type": "blast_radius_warning", "step": "build/impl", "violators": ["internal/engine/engine.go"], "allowed": ["internal/engine/error_digest.go"]}
```

### Quand `item.files` est vide

Si `item.files` est vide ou absent, aucun check quel que soit le niveau. Comportement actuel inchangé.

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

## Partie B — Retry Worktree/Session Policy

### Problème

Aujourd'hui, tous les retries (step et group) reset le worktree au commit pré-step ou pré-groupe. Conséquences :

1. **Gaspillage** : un retry `same` après un gate fail (1 test échoue sur 200) jette 90% de bon travail. L'agent doit tout réécrire au lieu de fixer 3 lignes.
2. **Désynchronisation session/worktree** : le group retry reset le worktree mais ne reset pas les sessions. L'agent avec `session: reuse` croit que son travail est encore là → dit "already done" → les reviews voient un worktree vide → circuit breaker.
3. **Coût excessif** : un rebuild complet consomme 5-10x plus de tokens qu'un fix incrémental.

### Nouveau comportement

La règle est : **le worktree et la session sont toujours synchronisés**. Reset worktree = fresh session. Worktree en place = reuse session.

#### Step retry

| Stratégie | Worktree | Session | Rationale |
|-----------|----------|---------|-----------|
| `same` | **En place** (pas de reset) | Reuse | L'agent fixe son erreur sur son propre travail. `{error}` et `{diff}` injectés. |
| `escalate` | **Reset** au commit pré-step | Fresh | Le nouvel agent repart propre, pas d'héritage d'erreurs d'un agent moins capable. |
| `replan` | **Reset** au commit pré-step | Fresh | Nouvelle décomposition, tout change. |

#### Group retry

| Stratégie | Worktree | Session | Rationale |
|-----------|----------|---------|-----------|
| `same` | **En place** (pas de reset) | Reuse | Le groupe réessaie avec le code en place. Les steps internes ont leur contexte. |
| `escalate` | **Reset** au commit pré-groupe | Fresh (toutes les sessions du groupe invalidées) | Nouvel agent, propre. |
| `replan` | **Reset** au commit pré-groupe | Fresh | idem |

#### restart_from

| Situation | Worktree | Session |
|-----------|----------|---------|
| `restart_from` | **Reset** au commit pré-step-cible | Fresh |

Inchangé par rapport au comportement actuel.

### Changements par rapport à l'existant

| Aspect | Avant (v0.0.3) | Après |
|--------|---------------|-------|
| Step retry `same` | Reset worktree | **Worktree en place** |
| Step retry `escalate` | Reset worktree | Reset worktree (inchangé) |
| Group retry `same` | Reset worktree, session pas invalidée (**bug**) | **Worktree en place**, session reuse |
| Group retry `escalate` | Reset worktree, session pas invalidée (**bug**) | Reset worktree, **session fresh** |

### Implémentation

#### Step retry — ne plus reset sur `same`

Dans `internal/engine/engine.go`, la logique de retry step atomique (dans `runAtomicStep` ou `RunWithRetry`) :

1. **Avant** : chaque retry fait `git reset --hard <preStepCommit>` + `git clean -fd`.
2. **Après** : le reset ne se fait que si la stratégie est `escalate` ou `replan`. Pour `same`, le worktree est laissé en place.

Le `{error}` et `{diff}` sont toujours injectés dans le prompt de retry — l'agent voit ce qui a échoué et corrige sur place.

#### Group retry — ne plus reset sur `same`, invalider sessions sur `escalate`

Dans `internal/engine/engine.go`, la logique de group retry :

1. **Avant** : chaque group retry fait `git reset --hard <preGroupCommit>` + `git clean -fd`.
2. **Après** :
   - Si `same` : worktree en place, sessions en place. Les steps internes reprennent avec leur contexte.
   - Si `escalate` ou `replan` : worktree reset + `e.StateBag.ClearSessionIDsForGroup(groupPath)` + vider le `lastSessionByAgent` map local.

#### ClearSessionIDsForGroup

Nouvelle méthode dans `internal/statebag/statebag.go` :

Met à `""` tous les `session_id` des entrées dont le path commence par `groupPath`. Utilisée uniquement sur group retry `escalate`/`replan`.

#### Ledger event

Quand les sessions sont invalidées (group retry escalate/replan) :

```json
{"type": "group_retry_sessions_reset", "group": "build", "attempt": 2, "strategy": "escalate", "invalidated_sessions": ["build/impl", "build/reviews/arch-review"]}
```

Pas d'event quand `same` (rien n'est invalidé).

### Blast radius Partie B

```
internal/engine/engine.go              # Conditionner le worktree reset sur la stratégie
internal/engine/retry.go               # Passer la stratégie au code de reset
internal/statebag/statebag.go          # Ajouter ClearSessionIDsForGroup(groupPath string)
internal/statebag/statebag_test.go     # Test unitaire
internal/ledger/events.go              # Ajouter GroupRetrySessionsReset event
```

### Tests e2e Partie B

#### B-E2E-1 : Step retry same — worktree en place

```
Setup  : workflow avec step retry: 3, strategy: [same, same, same]
         stub: attempt 1 crée hello.go mais gate fail (test échoue)
         stub: attempt 2 fixe le test (hello.go toujours présent du attempt 1)
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0
         Le worktree au début du 2ème attempt contient hello.go (pas de reset)
         Le manifest ne contient PAS de git reset entre attempt 1 et 2
```

#### B-E2E-2 : Step retry escalate — worktree reset

```
Setup  : workflow avec step retry: 2, strategy: [same, "escalate: claude-opus"]
         stub: attempt 1 crée hello.go mais gate fail
         stub: attempt 2 (opus) crée hello.go correctement
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0
         Le worktree au début du 2ème attempt ne contient PAS hello.go du attempt 1 (reset)
         Le agent_launched du 2ème attempt a session_id="" (fresh)
```

#### B-E2E-3 : Group retry same — worktree en place, session reuse

```
Setup  : workflow adversarial-like avec group retry: 2, strategy: [same]
         Groupe : impl → review (review fail au 1er group attempt)
         stub impl: crée hello.go
         stub review: fail au 1er attempt, pass au 2ème
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0
         Au 2ème group attempt, hello.go est toujours dans le worktree (pas de reset)
         Le agent_launched de impl au 2ème group attempt a un session_id non vide (reuse)
         Pas de group_retry_sessions_reset event
```

#### B-E2E-4 : Group retry escalate — worktree reset, session fresh

```
Setup  : workflow avec group retry: 2, strategy: [same, "escalate: claude-opus"]
         stub: group attempt 1 fail (review)
         stub: group attempt 2 pass (opus)
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : Le worktree au 2ème group attempt est reset
         Le agent_launched de impl au 2ème group attempt a session_id="" (fresh)
         manifest contient group_retry_sessions_reset event avec strategy "escalate"
```

#### B-E2E-5 : Step retry reuse-on-retry préservé (non-régression)

```
Setup  : workflow avec step retry: 2, session: reuse-on-retry, strategy: [same]
         stub: attempt 1 fail, attempt 2 pass
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : Le agent_launched du 2ème attempt a le session_id du 1er (reuse)
         Le worktree contient les fichiers du 1er attempt (en place)
```

#### B-E2E-6 : Non-régression

```
Vérif  : go test ./... passe
         Les tests existants de retry (TestE2E3, TestE2EReuseOnRetry, TestE2EStep6R4-R10,
         TestG5_E2E_8-12, TestM3_E2E1-11) passent toujours
```

---

## Critères de succès

1. `go build -o gump .` compile.
2. `blast_radius: enforce` est le défaut (backward compat).
3. `blast_radius: warn` produit un warning sans bloquer le run.
4. `blast_radius: off` ne fait aucun check.
5. Les tests blast radius existants (`TestE2EBlastRadiusBR1`, `BR2`) passent toujours.
6. Step retry `same` ne reset pas le worktree — l'agent fixe sur place.
7. Step retry `escalate` reset le worktree + session fresh.
8. Group retry `same` ne reset pas le worktree — les sessions sont préservées.
9. Group retry `escalate` reset le worktree + invalide toutes les sessions du groupe.
10. Les tests de retry existants passent toujours (non-régression critique).
11. Les 11 tests e2e passent (5 Partie A + 6 Partie B).
12. `go test ./...` passe.