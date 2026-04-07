# Fix : StateBag ambiguous reference panic → return empty string

> Spec destinée à Cursor Composer. Pas de code dans ce document.
> Prérequis : `go test ./...` passe.
> Objectif : le state bag ne doit jamais panic. Une référence ambiguë retourne une chaîne vide (le template cleanup supprime la ligne).

---

## Problème

`internal/statebag/statebag.go:178` fait un `panic("statebag: ambiguous step reference ...")` quand un nom court (ex: `arbiter`) matche plusieurs entrées dans le state bag (ex: dans un foreach avec convergence loop, chaque itération produit un `arbiter`).

Le panic crash le process Gump. Conséquence : le run est perdu, le worktree est dans un état indéterminé, le manifest est incomplet.

Le cas concret : un prompt contient `{steps.arbiter.output}`. Au premier tour d'une boucle de convergence (reviews → arbiter → fix → restart_from reviews), l'arbiter n'a pas encore tourné. Le template engine appelle `stateBag.Get("arbiter", scopePath, "output")`. Le state bag trouve zéro ou plusieurs entrées et panic.

## Comportement attendu

`resolveInSource` dans `statebag.go` ne doit **jamais panic**. Quand la référence est ambiguë (plusieurs matches pour un nom court) :

1. Log un warning : `"statebag: ambiguous reference '%s' in scope '%s', returning empty (use fully-qualified path)"`.
2. Retourner `nil` (pas d'entrée trouvée).
3. Le caller (`Get`) retourne `""`.
4. Le template engine reçoit `""` → le template cleanup (G4 Partie B) supprime la ligne si la variable est seule sur la ligne.

Même comportement pour les autres cas de non-résolution : pas de panic, retourner `nil`.

## Blast radius

```
internal/statebag/statebag.go          # Remplacer le panic par un log warning + return nil
internal/statebag/statebag_test.go     # Ajouter un test pour la référence ambiguë
```

## Test

```
TestStateBag_AmbiguousReference :
  Setup  : state bag avec deux entrées "group1/arbiter.output" et "group2/arbiter.output"
  Action : Get("arbiter", "group1/reviews", "output")
  Vérif  : retourne "" (pas de panic), pas de crash
```

```
TestStateBag_AmbiguousReference_FullyQualifiedWorks :
  Setup  : même state bag
  Action : Get("group1/arbiter", "", "output")
  Vérif  : retourne la valeur de group1/arbiter
```

## Critères de succès

1. `go build ./...` compile.
2. Plus aucun `panic` dans `statebag.go`.
3. Le test ambiguous reference passe.
4. `go test ./...` passe.