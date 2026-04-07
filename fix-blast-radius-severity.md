# Fix : Blast Radius Severity Levels

> Spec destinée à Cursor Composer. Pas de code dans ce document.
> Prérequis : `go test ./...` passe.
> Objectif : le blast radius enforcement devient configurable par workflow. Trois niveaux : `enforce` (défaut actuel, gate fail), `warn` (log warning, pas de fail), `off` (aucun check). Résultat attendu : un workflow peut déclarer `blast_radius: warn` et l'agent est libre de toucher les fichiers nécessaires, avec un warning dans le ledger.

---

## 1. Problème

Le blast radius enforcement est binaire : si `item.files` est non vide, tout fichier modifié hors de la liste cause un gate fail. Quand le plan produit par un agent de décomposition omet un fichier du blast radius (ex: `engine.go` nécessaire pour une intégration mais oublié dans le plan), le run boucle jusqu'au circuit breaker sans possibilité de récupération.

## 2. Comportement attendu

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
{"type": "blast_radius_warning", "step": "build/impl", "violators": ["internal/engine/engine.go"], "allowed": ["internal/engine/error_digest.go", "internal/context/builder.go"]}
```

### Quand `item.files` est vide

Si `item.files` est vide ou absent, aucun check quel que soit le niveau. C'est le comportement actuel inchangé.

## 3. Blast radius

```
internal/recipe/types.go               # Ajouter BlastRadius string sur Recipe
internal/recipe/parser.go              # Parser blast_radius (défaut "enforce")
internal/recipe/validator.go           # Valider les 3 valeurs
internal/engine/engine.go              # Lire recipe.BlastRadius, conditionner le check
internal/ledger/events.go              # Ajouter BlastRadiusWarning event
internal/engine/display.go             # Afficher le warning
```

## 4. Tests e2e

### Test 1 : enforce (défaut, backward compat)

```
Setup  : workflow sans blast_radius (défaut), item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : gate fail avec "blast radius violation" (comportement actuel inchangé)
```

### Test 2 : warn

```
Setup  : workflow avec blast_radius: warn, item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0 (pas de gate fail), stderr contient "blast radius warning", manifest contient blast_radius_warning event
```

### Test 3 : off

```
Setup  : workflow avec blast_radius: off, item.files = ["hello.go"], stub crée hello.go + extra.go
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0, stderr ne contient PAS "blast radius", manifest ne contient PAS blast_radius_warning
```

### Test 4 : empty files (aucun check quel que soit le niveau)

```
Setup  : workflow avec blast_radius: enforce, item.files = [], stub crée n'importe quoi
Entrée : gump run spec.md --workflow custom --agent-stub
Vérif  : exit 0 (pas de check)
```

## 5. Critères de succès

1. `go build -o gump .` compile.
2. `blast_radius: enforce` est le défaut (backward compat).
3. `blast_radius: warn` produit un warning sans bloquer le run.
4. `blast_radius: off` ne fait aucun check.
5. Les 4 tests e2e passent.
6. Les tests existants de blast radius (`TestE2EBlastRadiusBR1`, `BR2`) passent toujours.
7. `go test ./...` passe.