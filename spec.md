C'est tout à fait juste. J'ai trop résumé la partie sur les tests E2E et l'update checker dans ma proposition précédente, ce qui laisse une marge d'interprétation dangereuse à Cursor.

Voici la spec intégrale, **avec le même niveau de détail chirurgical que ton document original**, en intégrant uniquement nos changements sur la distribution publique et la synchro avec ton front-end Vercel.

Tu peux copier-coller ce bloc entier.

---

# Pudding — Spec d'Implémentation : Distribution, Installation et Update Check

> Spec destinée à Cursor Composer. Pas de code dans ce document. Zéro arbitrage métier laissé à l'implémenteur. Prérequis : étapes 1 à 8 implémentées, `go test ./...` passe. Résultat attendu : `goreleaser release`produit les binaires + Homebrew tap + install script. Le binaire affiche un message d'update si une nouvelle version est disponible, de manière non-bloquante avec cache 24h.

---

## Vue d'ensemble

Cette étape couvre 4 sous-systèmes :

1. **Versioning** : injection de la version `vMAJOR.MINOR.PATCH` au build via `-ldflags`.
    
2. **GoReleaser** : config `.goreleaser.yaml` pour produire les binaires cross-platform, le Homebrew tap, et le script d'installation.
    
3. **Synchronisation Vercel** : GitHub Action pour pousser le script d'installation dans le dossier `public/` du repo front-end (servi sur `pudding.build`).
    
4. **Update checker** : check HTTPS non-bloquant au lancement, cache 24h, message après commande réussie, désactivable.
    

---

## 1. Versioning

### Injection de version par ldflags

Aujourd'hui, `cmd/version.go` utilise une constante hardcodée. Remplacer par une variable injectable au build.

**Fichier `internal/version/version.go`** (nouveau) :

Déclarer trois variables package-level :

Go

```
var (
    Version   = "dev"
    Commit    = "unknown"
    BuildDate = "unknown"
)
```

Ces variables sont injectées par GoReleaser via `-ldflags`:

Plaintext

```
-X <module>/internal/version.Version={{.Version}}
-X <module>/internal/version.Commit={{.ShortCommit}}
-X <module>/internal/version.BuildDate={{.Date}}
```

Où `<module>` est le module path du `go.mod` existant.

**Fichier `cmd/version.go`** (modifier) :

`pudding --version` affiche :

Plaintext

```
pudding v0.0.1 (abc1234, 2026-03-18)
```

Format : `pudding <Version> (<Commit>, <BuildDate>)`.

Si `Version == "dev"` (build local sans ldflags) :

Plaintext

```
pudding dev (unknown, unknown)
```

### Format de version

Semver stricte : `vMAJOR.MINOR.PATCH`. Le `v` prefix est la convention Go et GoReleaser.

Les tags Git déclenchent le release : `git tag v0.0.1 && git push --tags` → GoReleaser CI crée le release GitHub.

---

## 2. GoReleaser

### Fichier `.goreleaser.yaml` à la racine du projet

YAML

```
version: 2

project_name: pudding

builds:
  - id: pudding
    main: .
    binary: pudding
    env:
      - CGO_ENABLED=0
    goos:
      - linux
      - darwin
    goarch:
      - amd64
      - arm64
    ldflags:
      - -s -w
      - -X {{.ModulePath}}/internal/version.Version={{.Version}}
      - -X {{.ModulePath}}/internal/version.Commit={{.ShortCommit}}
      - -X {{.ModulePath}}/internal/version.BuildDate={{.Date}}

archives:
  - id: default
    format: tar.gz
    name_template: "pudding_{{ .Os }}_{{ .Arch }}"

checksum:
  name_template: "checksums.txt"

release:
  github:
    owner: isomorphx
    name: pudding
  draft: false
  prerelease: auto
  extra_files:
    - glob: ./install.sh

brews:
  - name: pudding
    repository:
      owner: isomorphx
      name: homebrew-tap
      token: "{{ .Env.HOMEBREW_TAP_TOKEN }}"
    directory: Formula
    homepage: "https://pudding.build"
    description: "Orchestrate AI coding agents with declarative recipes"
    license: "MIT"
    install: |
      bin.install "pudding"
    test: |
      system "#{bin}/pudding", "--version"
```

### Repos GitHub nécessaires

Deux repos dans l'organisation GitHub `isomorphx` :

1. `isomorphx/pudding` — le repo principal (repo courant).
    
2. `isomorphx/homebrew-tap` — le repo Homebrew tap. Créer un repo vide avec un `README.md`. GoReleaser y pushera automatiquement le fichier `Formula/pudding.rb` à chaque release.
    

### GitHub Actions workflow

Fichier `.github/workflows/release.yaml` :

YAML

```
name: Release

on:
  push:
    tags:
      - "v*"

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod

      - uses: goreleaser/goreleaser-action@v6
        with:
          version: "~> v2"
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
          HOMEBREW_TAP_TOKEN: ${{ secrets.HOMEBREW_TAP_TOKEN }}
```

**Secrets à configurer dans le repo `isomorphx/pudding`** :

- `HOMEBREW_TAP_TOKEN` : Personal Access Token GitHub avec permission `repo` sur `isomorphx/homebrew-tap`. Nécessaire pour que GoReleaser puisse pusher la formula.
    

---

## 3. Script d'installation (`install.sh`)

### Fichier `install.sh` à la racine du projet

Le script sera auto-hébergé sur `https://pudding.build/install.sh` (via le repo front-end) et également inclus dans chaque GitHub Release du repo CLI.

**Comportement du script** :

1. Détecte l'OS (`uname -s` → `linux` ou `darwin`).
    
2. Détecte l'architecture (`uname -m` → `amd64` ou `arm64`). Mapping : `x86_64` → `amd64`, `aarch64` ou `arm64` → `arm64`.
    
3. Détermine la dernière version via l'API GitHub : `https://api.github.com/repos/isomorphx/pudding/releases/latest`. Extraire le champ `tag_name` (ex: `v0.0.1`). Supprimer le prefix `v` pour le nom de l'archive.
    
4. Télécharge l'archive : `https://github.com/isomorphx/pudding/releases/download/<tag>/pudding_<os>_<arch>.tar.gz`.
    
5. Télécharge le fichier de checksums : `https://github.com/isomorphx/pudding/releases/download/<tag>/checksums.txt`.
    
6. Vérifie le checksum SHA256 de l'archive téléchargée contre `checksums.txt`. Si mismatch : erreur fatale, supprime les fichiers téléchargés, exit 1.
    
7. Extrait le binaire `pudding` de l'archive.
    
8. Installe dans `/usr/local/bin/pudding` (avec `sudo` si nécessaire — détecter si le répertoire est writable par l'utilisateur courant).
    
9. Affiche un message de succès avec la version installée :
    

Plaintext

```
Pudding v0.0.1 installed to /usr/local/bin/pudding

Run 'pudding doctor' to verify your setup.
```

**Gestion d'erreur** :

- Si `curl` n'est pas installé, tenter `wget`. Si ni l'un ni l'autre : erreur `"curl or wget is required"`.
    
- Si l'OS ou l'architecture n'est pas supporté : erreur `"Unsupported platform: <os>/<arch>"`.
    
- Si le téléchargement échoue (HTTP != 200) : erreur avec l'URL tentée.
    
- Si le checksum échoue : erreur `"Checksum verification failed. The downloaded file may be corrupted."`.
    
- Toutes les erreurs affichent un message lisible sur stderr et exit 1.
    
- Le script utilise `set -euo pipefail` et un `trap` pour nettoyer les fichiers temporaires en cas d'erreur.
    

**Détail du contenu exact du script** :

Bash

```
#!/usr/bin/env bash
set -euo pipefail

REPO="isomorphx/pudding"
INSTALL_DIR="/usr/local/bin"
BINARY_NAME="pudding"

main() {
    detect_platform
    get_latest_version
    download_and_verify
    install_binary
    cleanup
    echo ""
    echo "Pudding ${VERSION} installed to ${INSTALL_DIR}/${BINARY_NAME}"
    echo ""
    echo "Run 'pudding doctor' to verify your setup."
}

detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)

    case "${OS}" in
        linux) OS="linux" ;;
        darwin) OS="darwin" ;;
        *) fatal "Unsupported OS: ${OS}" ;;
    esac

    case "${ARCH}" in
        x86_64|amd64) ARCH="amd64" ;;
        aarch64|arm64) ARCH="arm64" ;;
        *) fatal "Unsupported architecture: ${ARCH}" ;;
    esac
}

get_latest_version() {
    VERSION=$(fetch "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/')
    if [ -z "${VERSION}" ]; then
        fatal "Could not determine latest version"
    fi
}

download_and_verify() {
    TMPDIR=$(mktemp -d)
    trap 'rm -rf "${TMPDIR}"' EXIT

    ARCHIVE="pudding_${OS}_${ARCH}.tar.gz"
    DOWNLOAD_URL="https://github.com/${REPO}/releases/download/${VERSION}/${ARCHIVE}"
    CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

    echo "Downloading Pudding ${VERSION} for ${OS}/${ARCH}..."
    fetch "${DOWNLOAD_URL}" > "${TMPDIR}/${ARCHIVE}"
    fetch "${CHECKSUMS_URL}" > "${TMPDIR}/checksums.txt"

    EXPECTED=$(grep "${ARCHIVE}" "${TMPDIR}/checksums.txt" | awk '{print $1}')
    if [ -z "${EXPECTED}" ]; then
        fatal "Archive ${ARCHIVE} not found in checksums"
    fi

    ACTUAL=$(sha256sum "${TMPDIR}/${ARCHIVE}" 2>/dev/null || shasum -a 256 "${TMPDIR}/${ARCHIVE}" | awk '{print $1}')
    # Normalize: extract just the hash
    ACTUAL=$(echo "${ACTUAL}" | awk '{print $1}')

    if [ "${EXPECTED}" != "${ACTUAL}" ]; then
        fatal "Checksum verification failed. Expected ${EXPECTED}, got ${ACTUAL}"
    fi

    tar -xzf "${TMPDIR}/${ARCHIVE}" -C "${TMPDIR}" "${BINARY_NAME}"
}

install_binary() {
    if [ -w "${INSTALL_DIR}" ]; then
        mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    else
        echo "Elevated permissions required to install to ${INSTALL_DIR}"
        sudo mv "${TMPDIR}/${BINARY_NAME}" "${INSTALL_DIR}/${BINARY_NAME}"
    fi
    chmod +x "${INSTALL_DIR}/${BINARY_NAME}"
}

cleanup() {
    rm -rf "${TMPDIR}" 2>/dev/null || true
}

fetch() {
    if command -v curl >/dev/null 2>&1; then
        curl -fsSL "$1"
    elif command -v wget >/dev/null 2>&1; then
        wget -qO- "$1"
    else
        fatal "curl or wget is required"
    fi
}

fatal() {
    echo "Error: $1" >&2
    exit 1
}

main
```

---

## 4. Synchronisation du script d'installation (Vercel Frontend)

Le domaine `pudding.build` pointera vers une application front-end hébergée sur Vercel, dont le code source vivra dans un autre repository, par exemple `isomorphx/pudding-web`.

Pour que `curl -fsSL https://pudding.build/install.sh` fonctionne, le fichier `install.sh` doit être présent dans le répertoire `public/` de ce repo web.

La synchronisation se fait via un GitHub Actions workflow dans le repo CLI principal, qui copie `install.sh` vers le repo web à chaque release :

**Fichier `.github/workflows/sync-install-script.yaml`** dans `isomorphx/pudding` :

YAML

```
name: Sync install script

on:
  release:
    types: [published]

jobs:
  sync:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Push install.sh to site repo
        uses: dmnemec/copy_file_to_another_repo_action@main
        env:
          API_TOKEN_GITHUB: ${{ secrets.WEB_REPO_TOKEN }}
        with:
          source_file: install.sh
          destination_repo: isomorphx/pudding-web
          destination_folder: public/
          user_email: ci@pudding.build
          user_name: pudding-ci
          commit_message: "chore: sync install.sh from release ${{ github.ref_name }}"
```

Ce workflow utilise le secret `WEB_REPO_TOKEN` (qui a accès au repo front-end).

---

## 5. Update Checker

### Comportement

Au lancement d'une commande Pudding **métier**, un check asynchrone vérifie si une version plus récente est disponible. Le message est affiché **après** l'output de la commande, **seulement si la commande a réussi** (exit code 0).

**Commandes éligibles au check** : `cook`, `apply`, `doctor`, `report`, `cookbook`, `config`, `gc`, `status`, `guard`, `models`.

**Commandes exclues** (pas de check, jamais) : `--help`, `--version`, `completion`, et toute invocation sans sous-commande. Ces commandes doivent rester instantanées et sans side-effect.

**Implémentation de la whitelist** : dans `cmd/root.go`, le check est lancé dans `PersistentPreRunE` uniquement si le nom de la commande Cobra (`cmd.Name()`) est dans la liste des commandes éligibles. Si la commande n'est pas dans la liste, `updateCh` reste `nil` et aucune goroutine n'est lancée.

**Flux** :

1. Au démarrage de la commande racine (`cmd/root.go`), lancer le check en background (goroutine).
    
2. La commande s'exécute normalement.
    
3. Après la commande — uniquement si exit code == 0 — lire le résultat du check (channel).
    
4. Si une mise à jour est disponible, afficher le message sur stderr.
    
5. Si le check a échoué, timeout, ou pas de mise à jour : rien.
    

### Message d'update

Format exact sur stderr :

Plaintext

```

A new version of Pudding is available: v0.0.2 (current: v0.0.1)

  curl -fsSL https://pudding.build/install.sh | bash

  or: brew upgrade pudding

```

Le message est encadré de lignes vides pour la lisibilité. Le bloc entier est écrit en un seul `fmt.Fprint(os.Stderr, ...)` pour éviter l'interleaving avec d'autres outputs.

**Si `Version == "dev"`** (build local) : ne jamais afficher le message d'update, ne jamais faire le check.

### Check HTTPS

**Endpoint** : `https://api.github.com/repos/isomorphx/pudding/releases/latest`.

**Parsing** : extraire le champ `tag_name` du JSON. Comparer avec la version courante (`internal/version.Version`).

**Comparaison semver** : parser les deux versions comme `MAJOR.MINOR.PATCH` (ignorer le prefix `v`). La mise à jour est disponible si la version remote est strictement supérieure. Comparaison : MAJOR d'abord, puis MINOR, puis PATCH. Pas de dépendance externe pour le parsing semver — c'est 20 lignes de code.

### Contraintes non-fonctionnelles

|**Contrainte**|**Valeur**|**Raison**|
|---|---|---|
|Timeout HTTP|1 seconde|Ne jamais ralentir le CLI|
|Cache local|24 heures|Ne pas spammer l'API GitHub|
|Exécution|Goroutine (non-bloquant)|La commande démarre immédiatement|
|Affichage|Après commande réussie (exit 0)|Ne pas polluer un output d'erreur|
|Désactivable|Config + env var|Le dev contrôle|

### Cache

**Fichier cache** : `~/.pudding/cache/update-check.json`.

**Structure** :

JSON

```
{
    "checked_at": "2026-03-15T14:30:00Z",
    "latest_version": "v0.0.2"
}
```

**Logique** :

1. Lire le fichier cache. Si parse OK et `checked_at` < 24h : utiliser `latest_version` du cache. Pas de requête HTTP.
    
2. Si le cache n'existe pas, est corrompu, ou est périmé (> 24h) : requête HTTP.
    
3. Si la requête HTTP réussit : écrire le résultat dans le cache (créer le répertoire `~/.pudding/cache/` si nécessaire).
    
4. Si la requête HTTP échoue (timeout, erreur réseau, HTTP != 200, JSON invalide) : silencieux. Pas de cache écrit. Pas de message. Pas d'erreur.
    

**Écriture atomique** : écrire dans un fichier temporaire dans le même répertoire, puis `os.Rename`. Évite les lectures partielles si deux instances de Pudding tournent en parallèle.

### Désactivation

Deux mécanismes, chacun suffit :

1. **Variable d'environnement** : `PUDDING_NO_UPDATE_CHECK=1` (ou `true`, `yes` — toute valeur non vide).
    
2. **Config utilisateur** (`~/.pudding/config.toml`) ou **config projet** (`pudding.toml`) :
    

Ini, TOML

```
[update]
check = false
```

La résolution suit la cascade existante : env var > config utilisateur > config projet. Si l'un des trois dit non : pas de check.

### Nouveau champ dans Config

Ajouter dans la struct `Config` (fichier `internal/config/config.go`) :

Go

```
UpdateCheck bool  // défaut: true
```

Le loader (`internal/config/loader.go`) :

- Lit `[update] check = false` depuis le TOML (projet et utilisateur).
    
- Lit `PUDDING_NO_UPDATE_CHECK` depuis l'env. Si non vide → `UpdateCheck = false`.
    
- Défaut : `true`.
    

### Structure de fichiers

Plaintext

```
internal/
├── version/
│   ├── version.go          # variables Version, Commit, BuildDate
│   ├── update_check.go     # logique de check + cache + comparaison semver
│   └── update_check_test.go
```

**Package `internal/version`** : contient toute la logique. Pas de dépendance vers `internal/config` — le caller (`cmd/root.go`) passe un booléen `enabled` et la version courante.

### Interface publique du package `internal/version`

Go

```
// CheckForUpdate vérifie si une nouvelle version est disponible.
// Retourne la dernière version disponible si plus récente que currentVersion, sinon "".
// Non-bloquant si appelé dans une goroutine. Utilise le cache si frais.
// Ne retourne jamais d'erreur au caller — les erreurs sont silencieuses.
func CheckForUpdate(currentVersion string) string
```

Le caller (`cmd/root.go`) :

Le flow s'implémente en deux hooks Cobra sur la commande racine :

1. **`PersistentPreRunE`** : si les trois conditions sont réunies (version != "dev", config `UpdateCheck` activé, commande dans la whitelist éligible), lancer `CheckForUpdate` dans une goroutine et stocker le channel résultat.
    
2. **`PersistentPostRunE`** : si la commande a réussi (exit 0) et que le channel existe, lire le résultat avec un `select` + `default`. Si une version plus récente est disponible, afficher le message sur stderr. Si le check n'est pas terminé, ne pas attendre — le `default` garantit que le CLI ne bloque jamais.
    

**Point critique** : le `select` avec `default` garantit que si le check HTTP n'est pas terminé quand la commande finit, on n'attend pas. Le message ne s'affiche que si le résultat est déjà disponible. C'est un best-effort.

### Blast radius

Plaintext

```
internal/version/version.go                # NOUVEAU — variables Version, Commit, BuildDate
internal/version/update_check.go           # NOUVEAU — check + cache + semver compare
internal/version/update_check_test.go      # NOUVEAU — tests unitaires
internal/config/config.go                  # MODIFIÉ — ajouter UpdateCheck bool
internal/config/loader.go                  # MODIFIÉ — lire [update] check + env var
cmd/version.go                             # MODIFIÉ — utiliser internal/version au lieu de constante
cmd/root.go                                # MODIFIÉ — lancer le check en goroutine + afficher le message
.goreleaser.yaml                           # NOUVEAU — config GoReleaser
.github/workflows/release.yaml             # NOUVEAU — CI release
.github/workflows/sync-install-script.yaml # NOUVEAU — sync install.sh vers repo front-end
install.sh                                 # NOUVEAU — script d'installation
```

---

## 6. Ce qui est hors scope

Ne PAS implémenter :

- Auto-update (Pudding ne se met pas à jour tout seul — il informe seulement).
    
- Versioning des recettes (les recettes n'ont pas de version semver).
    
- Telemetry ou analytics.
    
- Canary/beta channels (une seule channel : `latest`).
    
- Windows (pas de GOOS `windows` dans GoReleaser pour l'instant).
    
- La création effective des repos GitHub (`isomorphx/homebrew-tap`, `isomorphx/pudding-web`) — documenter les étapes manuelles, ne pas automatiser.
    
- Le déploiement du site Vercel.
    
- Signature des binaires (GPG, Sigstore) — day 2.
    

---

## 7. Tests E2E — Application entière

Tests dans `e2e/e2e_test.go`. Même patterns que les tests existants (`setupRepoWithCommit`, `runPudding`, etc.).

**Test E2E : `pudding --version` affiche la version injectée par ldflags**

Plaintext

```
Setup   : compiler le binaire avec ldflags :
          go build -ldflags "-X <module>/internal/version.Version=v99.88.77 -X <module>/internal/version.Commit=abc1234 -X <module>/internal/version.BuildDate=2026-03-15" -o <tmpdir>/pudding .
Entrée  : pudding --version
Vérif   :
  - exit 0
  - stdout contient "v99.88.77"
  - stdout contient "abc1234"
  - stdout contient "2026-03-15"
```

**Test E2E : `pudding --version` sans ldflags affiche "dev"**

Plaintext

```
Setup   : compiler le binaire sans ldflags (build standard existant dans TestMain)
Entrée  : pudding --version
Vérif   :
  - exit 0
  - stdout contient "dev"
```

**Test E2E : l'update check n'affiche rien si la version est "dev"**

Plaintext

```
Setup   : compiler le binaire sans ldflags (Version == "dev")
          setupRepoWithCommit(), créer spec.md, commiter
Entrée  : pudding cookbook list
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"
  - stderr NE contient PAS "pudding.build"
```

**Test E2E : l'update check est désactivé par PUDDING_NO_UPDATE_CHECK**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1 (volontairement vieille)
          setupRepoWithCommit()
Entrée  : PUDDING_NO_UPDATE_CHECK=1 pudding cookbook list
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"
```

**Test E2E : l'update check est désactivé par config TOML**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1
          setupRepoWithCommit()
          créer pudding.toml avec :
            [update]
            check = false
Entrée  : pudding cookbook list
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"
```

**Test E2E : le cache update-check est écrit et relu**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1
          créer manuellement ~/.pudding/cache/update-check.json avec :
            {"checked_at": "<now>", "latest_version": "v99.0.0"}
          setupRepoWithCommit()
Entrée  : pudding cookbook list
Vérif   :
  - exit 0
  - stderr contient "v99.0.0" et "new version"
  - stderr contient "pudding.build/install.sh"
  - stderr contient "brew upgrade pudding"
```

**Test E2E : le cache expiré déclenche un nouveau check (avec serveur HTTP mock)**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1
          créer ~/.pudding/cache/update-check.json avec :
            {"checked_at": "<25 heures dans le passé>", "latest_version": "v0.0.1"}
          lancer un serveur HTTP local sur un port libre qui répond :
            GET /repos/isomorphx/pudding/releases/latest → {"tag_name": "v99.0.0"}
          override le endpoint dans l'env : PUDDING_UPDATE_URL=http://localhost:<port>/repos/isomorphx/pudding/releases/latest
          setupRepoWithCommit()
Entrée  : pudding cookbook list
Vérif   :
  - exit 0
  - stderr contient "v99.0.0"
  - ~/.pudding/cache/update-check.json a été mis à jour (checked_at récent)
```

**Note pour l'implémenteur** : pour que ce test fonctionne, `CheckForUpdate` doit lire l'URL de check depuis la variable d'environnement `PUDDING_UPDATE_URL` si elle est définie, sinon utiliser `https://api.github.com/repos/isomorphx/pudding/releases/latest`. C'est le seul hook de test — pas d'interface ni de mock framework.

**Test E2E : le message d'update n'apparaît PAS si la commande échoue**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1
          créer ~/.pudding/cache/update-check.json avec :
            {"checked_at": "<now>", "latest_version": "v99.0.0"}
          setupRepoWithCommit()
Entrée  : pudding cook nonexistent-spec.md --recipe freeform
Vérif   :
  - exit != 0
  - stderr NE contient PAS "new version"
  - stderr NE contient PAS "pudding.build"
```

**Test E2E : l'update check ne se déclenche PAS sur --help et --version**

Plaintext

```
Setup   : compiler le binaire avec ldflags Version=v0.0.1
          créer ~/.pudding/cache/update-check.json avec :
            {"checked_at": "<now>", "latest_version": "v99.0.0"}
Entrée  : pudding --help
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"

Entrée  : pudding --version
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"

Entrée  : pudding cook --help
Vérif   :
  - exit 0
  - stderr NE contient PAS "new version"
```

**Test E2E : comparaison semver correcte**

Ce test valide la logique de comparaison au niveau unitaire. L'ajouter dans `internal/version/update_check_test.go` (pas e2e) :

Plaintext

```
Cas de test pour isNewer(remote, current) bool :
  - ("v1.0.0", "v0.9.0") → true     (major bump)
  - ("v0.2.0", "v0.1.0") → true     (minor bump)
  - ("v0.1.1", "v0.1.0") → true     (patch bump)
  - ("v0.1.0", "v0.1.0") → false    (même version)
  - ("v0.1.0", "v0.2.0") → false    (remote plus vieille)
  - ("v1.0.0", "v1.0.0") → false    (même)
  - ("invalid", "v0.1.0") → false   (remote invalide)
  - ("v0.1.0", "invalid") → false   (current invalide)
  - ("v0.10.0", "v0.9.0") → true    (comparison numérique, pas lexicographique)
  - ("v0.1.0", "dev") → false       (dev version → jamais newer)
```

**Test E2E : les tests existants passent toujours (non-régression)**

Plaintext

```
Vérif   : tous les tests e2e des étapes précédentes continuent de passer sans modification
```

---

## 8. Critères de succès

L'étape est terminée quand :

1. `go build .` compile sans erreur.
    
2. Tous les tests (e2e + unitaires) passent : `go test ./...`.
    
3. `goreleaser check` valide la config GoReleaser (pas de release réel nécessaire pour le merge).
    
4. `pudding --version` affiche la version injectée ou "dev".
    
5. Le fichier `install.sh` est exécutable et parsable par `shellcheck` (si disponible).
    
6. L'update checker fonctionne avec le mock HTTP (cache lu, écrit, expiré, message affiché).
    
7. L'update checker est silencieux quand désactivé (env var, TOML, version "dev", ou commande exclue comme `--help`/`--version`).
    
8. L'update checker ne ralentit jamais le CLI (timeout 1s + goroutine + select default).
    
9. L'action GitHub de synchronisation cible correctement le dossier `public/` du repo web.
    

---

## 9. Étapes manuelles post-merge (à faire par le dev, pas par Cursor)

Checklist des actions manuelles à exécuter après le merge de cette étape :

1. **Créer le repo `isomorphx/homebrew-tap`** sur GitHub. Ajouter un `README.md` vide. GoReleaser y pushera `Formula/pudding.rb` au premier release.
    
2. **Créer le repo `isomorphx/pudding-web`** (ou équivalent) pour le site web Vercel et y lier le domaine `pudding.build`.
    
3. **Créer les secrets `HOMEBREW_TAP_TOKEN` et `WEB_REPO_TOKEN`** dans le repo `isomorphx/pudding`: Personal Access Tokens avec permission `repo` sur l'organisation.
    
4. **Tagger et pusher** : `git tag v0.0.1 && git push --tags` pour déclencher le premier release.
    
5. **Vérifier** : (Une fois le site Vercel déployé) `curl -fsSL https://pudding.build/install.sh | bash` installe le binaire. `brew install isomorphx/tap/pudding` fonctionne.
    

*** Prêt pour Cursor ! Dis-moi si tu as besoin de conseils sur la mise en place concrète des tokens GitHub (`WEB_REPO_TOKEN`, etc.) une fois que Cursor aura fini de bosser.