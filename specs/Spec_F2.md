# Gump — Spec F2 : Observabilité Beta — Monitoring Day 1

> Spec destinée à Cursor Composer. Pas de code dans ce document. Prérequis : G1-G5 implémentées, F1 implémentée, `go test ./...` passe. Objectif : rendre Gump observable dès le day 1 de la beta — télémétrie avec backend, pricing engine pour estimer les coûts, context window dans le report, LOC dans la synthèse. Résultat attendu : le fondateur peut monitorer l'usage beta depuis un dashboard PostHog, chaque run produit un report avec coût estimé et context usage.

---

## 1. Vue d'ensemble

### Partie A — Télémétrie backend : intégration PostHog

Le CLI envoie déjà les events télémétrie (G3 Partie A implémentée). Le backend n'existe pas encore. Brancher l'envoi sur l'API PostHog Cloud et vérifier que le pipeline fonctionne end-to-end.

### Partie B — Pricing engine : fichier de prix embarqué

Gump ne peut pas calculer le coût estimé sans connaître les prix par token de chaque modèle. Ajouter un fichier de prix embarqué (source de vérité), un override utilisateur dans la config, et le calcul du coût estimé par turn et par step.

### Partie C — Context window dans le report

Ajouter une section "Context Usage" dans `gump report` avec des barres visuelles par step (vert/jaune/rouge). Ajouter une mini sparkline dans `gump report --detail <step>`.

### Partie D — LOC dans la synthèse run

Ajouter `lines_added` et `lines_removed` dans le state bag et dans la synthèse de `gump report`.

### Partie E — Compteur download

Ajouter un event PostHog dans le script d'installation curl pour compter les téléchargements. Documenter le compteur GitHub Releases.

---

## 2. Blast Radius

### Partie A — Télémétrie backend

```
internal/telemetry/telemetry.go          # Changer l'endpoint et le format d'envoi pour PostHog
internal/telemetry/telemetry_test.go     # Adapter les tests
internal/config/config.go                # Ajouter telemetry_endpoint override (optionnel)
```

### Partie B — Pricing engine

```
internal/pricing/models.go               # NOUVEAU — map modèle → prix par token
internal/pricing/models_test.go          # NOUVEAU
internal/pricing/calculator.go           # NOUVEAU — calcul coût estimé depuis tokens
internal/pricing/calculator_test.go      # NOUVEAU
internal/config/config.go                # Section [pricing] override
internal/config/loader.go               # Lire les overrides
internal/engine/engine.go               # Appeler le calculator après chaque agent_completed
internal/statebag/statebag.go           # Stocker cost_estimated en plus de cost_native
internal/report/render.go               # Afficher les deux coûts (natif + estimé)
internal/agent/claude.go                # Passer le model name au RunResult (pour le pricing)
internal/agent/codex.go                 # idem
internal/agent/gemini.go                # idem
internal/agent/qwen.go                  # idem
internal/agent/opencode.go              # idem
internal/agent/cursor.go                # idem
```

### Partie C — Context window dans report

```
internal/report/render.go               # Section Context Usage avec barres
internal/report/detail.go               # Mini sparkline par turn
internal/report/context_bar.go          # NOUVEAU — rendu des barres colorées
internal/report/context_bar_test.go     # NOUVEAU
```

### Partie D — LOC dans synthèse

```
internal/engine/engine.go               # Calculer git diff --stat après chaque step
internal/statebag/statebag.go           # Stocker lines_added, lines_removed
internal/report/render.go               # Afficher dans la synthèse
```

### Partie E — Compteur download

```
install.sh                               # Ajouter un hit PostHog avant le download
```

### Fichiers à NE PAS toucher

```
internal/agent/stub.go                   # Inchangé
internal/recipe/                         # Inchangé
internal/validate/                       # Inchangé
internal/template/                       # Inchangé
internal/sandbox/                        # Inchangé
```

---

## 3. Partie A — Télémétrie backend : Détail

### PostHog Cloud

Service : PostHog Cloud (free tier, 1M events/mois). Domaine : `telemetry.gump.build` (CNAME ou reverse proxy vers PostHog, ou utilisation directe de l'API PostHog).

### Endpoint

L'endpoint actuel dans le code (G3) envoie vers `https://telemetry.gump.dev/v1/events`. Changer pour :

**Option simple (recommandée)** : envoyer directement vers l'API PostHog :

```
POST https://app.posthog.com/capture/
Content-Type: application/json

{
  "api_key": "<POSTHOG_PROJECT_API_KEY>",
  "event": "run_completed",
  "distinct_id": "<anonymous_id>",
  "properties": {
    ... payload G3 tel quel ...
  }
}
```

La `api_key` PostHog est publique (client-side key). Elle peut être embarquée dans le binaire sans risque de sécurité. PostHog est conçu pour ça.

**Option avec CNAME** : configurer `telemetry.gump.build` comme reverse proxy vers `app.posthog.com`. Avantage : les ad blockers ne bloquent pas le domaine custom. Inconvénient : config DNS + reverse proxy (Vercel, Cloudflare, ou GCP).

### Adaptation du payload

Le payload G3 est quasi-compatible PostHog. Changements :

| Champ G3 | Champ PostHog | Transformation |
|----------|---------------|----------------|
| `anonymous_id` | `distinct_id` | Renommer |
| `event` | `event` | Identique |
| Tout le reste | `properties` | Wrapper dans `properties` |
| — | `api_key` | Ajouter (constante embarquée) |
| `v`, `gump_version`, `os`, `arch` | `properties.$lib_version`, `properties.$os`, etc. | PostHog a des propriétés prédéfinies, mais custom est fine |

### Overridable

La variable d'environnement `GUMP_TELEMETRY_URL` permet d'override l'endpoint (pour les tests et le self-host). Si override, le format d'envoi reste le format G3 (pas PostHog). Le code doit détecter si l'URL est PostHog (contient `posthog.com` ou `telemetry.gump.build`) ou custom, et adapter le format.

### Vérification end-to-end

Après implémentation :
1. `gump run spec.md --workflow freeform --agent-stub` → pas d'envoi (premier run, anonymous_id vient d'être créé).
2. `gump run spec.md --workflow freeform --agent-stub` (deuxième run) → event envoyé.
3. Vérifier dans le dashboard PostHog que l'event `run_completed` apparaît avec toutes les propriétés.

---

## 4. Partie B — Pricing engine : Détail

### Fichier de prix embarqué

`internal/pricing/models.go` contient une map Go constante :

```
Modèle Gump alias → { InputPricePerMTok, OutputPricePerMTok, CacheReadPricePerMTok, CacheWritePricePerMTok }
```

Les prix sont en USD par million de tokens.

### Emplacement

`internal/agent/models.go` contient déjà `KnownModels` avec les context window sizes par modèle. Le pricing engine ajoute les prix par token dans la **même structure** (ou une structure parallèle dans `internal/pricing/models.go`). Ne pas dupliquer la table de modèles — utiliser `agent.LookupModel()` pour le context window et le pricing engine pour les prix.

### Table de prix initiale (source : pricing pages publiques, avril 2026)

Les cli-ref docs (`docs/connector-book/cli-ref-*.md`) doivent contenir une colonne "Price per MTok" dans leur section "Models and Gump Aliases" — la source de vérité documentaire pour les prix, synchronisée avec le code.

| Provider | Modèle Gump | Input $/MTok | Output $/MTok | Cache Read $/MTok | Cache Write $/MTok |
|----------|-------------|-------------|---------------|-------------------|---------------------|
| Claude | claude-opus | 15.00 | 75.00 | 1.50 | 18.75 |
| Claude | claude-sonnet | 3.00 | 15.00 | 0.30 | 3.75 |
| Claude | claude-haiku | 0.80 | 4.00 | 0.08 | 1.00 |
| Codex | codex-gpt54 | 2.50 | 10.00 | 0 | 0 |
| Codex | codex-o3 | 10.00 | 40.00 | 0 | 0 |
| Gemini | gemini-pro | 1.25 | 5.00 | 0 | 0 |
| Gemini | gemini-flash | 0.15 | 0.60 | 0 | 0 |
| Qwen | qwen | 0 | 0 | 0 | 0 |
| OpenCode | (via provider) | (résolu par alias) | | | |
| Cursor | cursor | 0 | 0 | 0 | 0 |

**Qwen et Cursor** : prix à 0 car Qwen est gratuit (open-source, coût infra utilisateur) et Cursor est un abonnement (le coût par token n'est pas exposé). Le pricing engine retourne `cost_estimated = 0` pour ces providers, ce qui est correct (le coût est l'abonnement, pas le token).

**OpenCode** : OpenCode route vers des modèles d'autres providers (anthropic/claude-sonnet, openai/gpt-5.4, etc.). Le pricing engine doit résoudre l'alias OpenCode vers le modèle sous-jacent pour trouver le prix. Les alias OpenCode sont dans `internal/agent/opencode.go`. Exemple : `opencode-sonnet` → `anthropic/claude-sonnet-4-6` → prix de `claude-sonnet`.

**Mise à jour** : les prix sont mis à jour à chaque release de Gump. Entre deux releases, l'utilisateur peut override via la config :

```toml
[pricing]
# Override le prix d'un modèle spécifique
[pricing.overrides]
"claude-opus" = { input = 15.00, output = 75.00 }
"custom-model" = { input = 1.00, output = 5.00 }
```

### Calcul du coût estimé

Après chaque `agent_completed`, le pricing engine calcule :

```
cost_estimated = (input_tokens * input_price / 1_000_000)
               + (output_tokens * output_price / 1_000_000)
               + (cache_read_tokens * cache_read_price / 1_000_000)
               + (cache_creation_tokens * cache_write_price / 1_000_000)
```

Si le modèle n'est pas dans la table de prix → `cost_estimated = 0`, log warning `"pricing: unknown model '<name>', cost estimated as $0.00"`.

### Double affichage : natif vs estimé

Les providers qui reportent le coût nativement (Claude Code = oui) ont un `cost_native` dans le `RunResult`. Gump calcule toujours `cost_estimated` en parallèle, même quand `cost_native` est disponible.

Le state bag stocke les deux :
- `<step>.cost` = `cost_native` si disponible, sinon `cost_estimated` (comportement existant, inchangé).
- `<step>.cost_native` = le coût reporté par le provider (0 si non disponible).
- `<step>.cost_estimated` = le coût calculé par Gump depuis les tokens.

Le report affiche les deux quand les deux sont disponibles :

```
  Cost: $2.65 (native) | $2.71 (estimated)
```

Si seul l'estimé est disponible :

```
  Cost: ~$2.71 (estimated)
```

Si aucun n'est disponible (tokens = 0, modèle inconnu) :

```
  Cost: —
```

### Télémétrie

Le payload télémétrie (G3) envoie `cost_usd` qui est le `<step>.cost` (natif ou estimé, le meilleur disponible). Pas de changement au payload — la sémantique reste "meilleure estimation disponible".

### Le model name dans le RunResult

Pour que le pricing engine sache quel modèle a été utilisé, chaque adapter doit inclure le model name (l'alias Gump, pas le flag CLI) dans le `RunResult`. Ajouter un champ `ModelName string` au `RunResult` (ou un champ similaire existant — vérifier la struct actuelle).

Le model name est l'alias Gump tel que résolu par l'engine (ex: `claude-sonnet`, pas `claude-sonnet-4-6` ni le flag CLI).

---

## 5. Partie C — Context window dans report : Détail

### Source de données

Le `context_usage` par step est déjà dans le state bag (G1 Partie B). C'est un float entre 0.0 et 1.0, représentant le pourcentage de la context window utilisée à la fin du step (dernière valeur reportée par l'agent). Confiance ~80% (vient du stream agent).

Pour les providers qui ne reportent pas le context usage : la valeur est 0. Le report affiche `—` dans ce cas.

### Fallback pour les providers sans context_usage natif

Si un provider ne reporte pas le context usage dans le stream, Gump peut l'estimer :

```
context_usage_estimated = total_tokens_step / context_window_size_model
```

Où `context_window_size_model` est la taille de la context window du modèle (en tokens), stockée dans le pricing engine (même fichier que les prix) :

| Modèle | Context window (tokens) |
|--------|------------------------|
| claude-opus | 200_000 |
| claude-sonnet | 200_000 |
| claude-haiku | 200_000 |
| codex-gpt54 | 200_000 |
| gemini-pro | 2_000_000 |
| gemini-flash | 1_000_000 |
| qwen | 128_000 |

L'estimation est une borne inférieure (les tokens du step, pas de la session complète). Confiance ~50%. Afficher avec un `~` :

```
  build/1/impl   claude-haiku  ████████░░  ~78%
```

### Section Context Usage dans `gump report`

Après la section Cost dans le report, ajouter :

```
Context Usage
  decompose      claude-opus    ██░░░░░░░░  18%
  build/1/tests  claude-haiku   ████░░░░░░  42%
  build/1/impl   claude-haiku   ████████░░  78%  ⚠
  build/2/tests  claude-haiku   ███░░░░░░░  31%
  build/2/impl   claude-haiku   ██████████  95%  🔴
  quality        —              —
```

Règles de rendu :

- Barre : 10 caractères de large. `█` pour la partie remplie, `░` pour la partie vide.
- Couleur : vert (≤50%), jaune (51-80%), rouge (>80%). En terminal sans couleur : `⚠` après la valeur pour jaune, `🔴` pour rouge.
- Colonnes alignées : step name (largeur max du nom + 2), agent name (largeur max + 2), barre (12 chars), pourcentage.
- Si `context_usage == 0` et pas d'estimation possible → afficher `—`.

### Mini sparkline dans `gump report --detail <step>`

Dans la vue détaillée d'un step (G5 Partie D), ajouter l'évolution du context par turn :

```
Context: 12% → 28% → 45% → 62% → 78%  (peak: 78%)
```

Les valeurs viennent du TurnTracker si le provider reporte les tokens par turn (Claude = oui, Gemini = non). Si pas disponible par turn → afficher uniquement la valeur finale :

```
Context: 78% (final)
```

---

## 6. Partie D — LOC dans synthèse : Détail

### Calcul

Après chaque agent step (output mode `diff`), exécuter dans le worktree :

```bash
git diff --stat HEAD
```

Parser la dernière ligne de la sortie (`X files changed, Y insertions(+), Z deletions(-)`). Stocker dans le state bag :

- `<step>.lines_added` = Y
- `<step>.lines_removed` = Z
- `<step>.files_changed` = X

Pour les steps non-diff (output mode `plan`, `artifact`, `review`) → 0.

### Agrégation run

- `run.lines_added` = somme des `<step>.lines_added` de tous les steps (dernier attempt de chaque step, pas les attempts échoués).
- `run.lines_removed` = idem.
- `run.files_changed` = nombre de fichiers uniques modifiés dans le diff final (pas la somme des steps — un fichier modifié par deux steps ne compte qu'une fois). Calculé via `git diff --stat <initial-commit>..HEAD` sur le worktree final.

### Affichage dans le report

Dans la ligne de synthèse du run :

```
✓ run completed in 4m12s | $0.47 | 26 turns | +342/-18 lines (12 files)
```

Le format `+N/-M lines (F files)` est compact et standard (proche du format `git diff --stat`).

---

## 7. Partie E — Compteur download : Détail

### Script d'installation

Le fichier `install.sh` (hébergé sur `gump.build/install.sh` via Vercel) est le point d'entrée pour l'installation via curl.

Ajouter un hit PostHog au début du script :

```bash
# Anonymous install tracking (best-effort, non-blocking)
curl -s -o /dev/null -X POST https://app.posthog.com/capture/ \
  -H "Content-Type: application/json" \
  -d '{"api_key":"<POSTHOG_PROJECT_API_KEY>","event":"install","distinct_id":"anonymous","properties":{"method":"curl","os":"'"$(uname -s)"'","arch":"'"$(uname -m)"'"}}' \
  2>/dev/null &
```

Le hit est non-bloquant (background `&`), best-effort (échec silencieux). Le `distinct_id` est "anonymous" (pas de tracking individuel à l'installation).

### GitHub Releases

Les download counts de GitHub Releases sont disponibles nativement via l'API GitHub (`GET /repos/{owner}/{repo}/releases` → `assets[].download_count`). Pas de code à écrire — c'est un compteur automatique. Documenter dans le README ou un ADR que le compteur fiable est la somme des deux sources : PostHog `install` events + GitHub Releases download count.

---

## 8. Tests e2e

### F2-E2E-1 : Télémétrie — envoi PostHog

```
Setup  : GUMP_TELEMETRY_URL="http://localhost:PORT/capture/" (mock server local)
         anonymous_id déjà existant (pas premier run)
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : le mock server a reçu un POST avec "event":"run_completed" et "distinct_id" non vide
```

### F2-E2E-2 : Télémétrie — pas d'envoi au premier run

```
Setup  : GUMP_TELEMETRY_URL="http://localhost:PORT/capture/" (mock server local)
         supprimer ~/.gump/anonymous_id
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : le mock server n'a reçu aucun POST, anonymous_id a été créé
```

### F2-E2E-3 : Télémétrie — opt-out respecté

```
Setup  : gump config set analytics false, anonymous_id existe
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : le mock server n'a reçu aucun POST
```

### F2-E2E-4 : Pricing — coût estimé calculé

```
Setup  : stub rapporte tokens_in=10000, tokens_out=2000, model="claude-sonnet"
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : state-bag.json contient cost_estimated > 0 (10000*3/1M + 2000*15/1M = 0.06)
```

### F2-E2E-5 : Pricing — double affichage natif vs estimé

```
Setup  : stub rapporte cost=0.05 (natif) ET tokens_in=10000, tokens_out=2000
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : state-bag.json contient cost_native=0.05 ET cost_estimated=0.06
```

### F2-E2E-6 : Pricing — modèle inconnu

```
Setup  : stub rapporte model="unknown-future-model", tokens_in=1000
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : state-bag.json contient cost_estimated=0, stderr contient "pricing: unknown model"
```

### F2-E2E-7 : Pricing — override config

```
Setup  : config avec [pricing.overrides] "custom-model" = { input = 5.0, output = 20.0 }
         stub rapporte model="custom-model", tokens_in=1000, tokens_out=500
Entrée : gump run spec.md --workflow freeform --agent-stub
Vérif  : state-bag.json contient cost_estimated = (1000*5/1M + 500*20/1M) = 0.015
```

### F2-E2E-8 : Context window — dans report

```
Setup  : stub rapporte context_usage=0.78
Entrée : gump run spec.md --workflow freeform --agent-stub, puis gump report
Vérif  : stdout contient "78%" ET "████" (barre) ET "⚠" (jaune)
```

### F2-E2E-9 : Context window — fallback estimation

```
Setup  : stub rapporte context_usage=0, tokens_in=50000, tokens_out=10000, model="claude-sonnet"
Entrée : gump run + gump report
Vérif  : stdout contient "~" et un pourcentage (estimation) ET ne contient PAS "—"
```

### F2-E2E-10 : LOC dans synthèse

```
Setup  : stub produit un diff avec 3 fichiers, +50/-10 lignes
Entrée : gump run + gump report
Vérif  : stdout contient "+50/-10" et "3 files"
```

### F2-E2E-11 : LOC — step non-diff

```
Setup  : workflow avec step plan (output: plan)
Entrée : gump run + vérifier state-bag
Vérif  : le step plan a lines_added=0, lines_removed=0
```

### F2-E2E-12 : Non-régression

```
Vérif  : go test ./... passe
```

---

## 9. Smoke tests

**`TestSmokeTelemetryLive`** : `gump run --workflow freeform --agent claude-haiku` avec `GUMP_TELEMETRY_URL` pointant vers un mock → event reçu avec les propriétés attendues.

**`TestSmokePricingLive`** : après un run claude-haiku live → state-bag contient cost_native > 0 ET cost_estimated > 0 ET les deux sont du même ordre de grandeur.

**`TestSmokeContextUsageLive`** : après un run claude-sonnet live (spec non triviale) → `gump report` contient une section Context Usage avec au moins un step ayant un % > 0.

**`TestSmokeLOCLive`** : après un run freeform live qui produit du code → `gump report` contient `+N/-M lines`.

---

## 10. Critères de succès

1. `go build -o gump .` compile.
2. La télémétrie envoie vers PostHog au deuxième run.
3. Le premier run ne déclenche pas d'envoi.
4. L'opt-out (`analytics false`) empêche tout envoi.
5. Le pricing engine calcule un coût estimé pour les modèles connus.
6. Le coût estimé est affiché à côté du coût natif dans le report.
7. Les modèles inconnus produisent `cost_estimated = 0` avec un warning.
8. L'override config fonctionne pour des modèles custom.
9. Le report affiche la section Context Usage avec barres colorées.
10. L'estimation de context usage est disponible en fallback.
11. Le report affiche les LOC dans la synthèse.
12. Le script d'installation envoie un hit PostHog.
13. Les 12 tests e2e passent.
14. `go test ./...` passe.