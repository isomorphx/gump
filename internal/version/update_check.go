package version

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CheckForUpdate verifies if a newer version is available compared to currentVersion.
// It is intentionally error-silent: any failure (network/cache/parse) returns "".
//
// Caller typically runs this in a goroutine so the CLI stays responsive; this function
// still enforces a short HTTP timeout to guarantee upper bounds on work.
func CheckForUpdate(currentVersion string) string {
	if currentVersion == "dev" {
		// Local/dev builds are explicitly excluded from update checks.
		return ""
	}

	cachePath, err := updateCheckCachePath()
	if err != nil {
		return ""
	}

	// Best-effort fast path: use fresh cache without any HTTP round-trip.
	if latest, ok := readFreshCache(cachePath, 24*time.Hour); ok {
		if isNewer(latest, currentVersion) {
			return latest
		}
		return ""
	}

	endpoint := os.Getenv("PUDDING_UPDATE_URL")
	if endpoint == "" {
		endpoint = "https://api.github.com/repos/isomorphx/pudding/releases/latest"
	}

	latest, ok := fetchLatest(endpoint)
	if !ok {
		return ""
	}

	// Even if latest isn't newer, caching it avoids repeatedly hitting the API.
	_ = writeCacheAtomic(cachePath, latest)
	if isNewer(latest, currentVersion) {
		return latest
	}
	return ""
}

type updateCheckCache struct {
	CheckedAt     string `json:"checked_at"`
	LatestVersion string `json:"latest_version"`
}

func updateCheckCachePath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "", fmt.Errorf("missing home dir")
	}
	return filepath.Join(home, ".pudding", "cache", "update-check.json"), nil
}

func readFreshCache(cachePath string, maxAge time.Duration) (string, bool) {
	data, err := os.ReadFile(cachePath)
	if err != nil {
		return "", false
	}
	var c updateCheckCache
	if err := json.Unmarshal(data, &c); err != nil {
		return "", false
	}
	if c.CheckedAt == "" || c.LatestVersion == "" {
		return "", false
	}

	checkedAt, err := time.Parse(time.RFC3339, c.CheckedAt)
	if err != nil {
		return "", false
	}
	if time.Since(checkedAt) >= maxAge {
		return "", false
	}
	return c.LatestVersion, true
}

func writeCacheAtomic(cachePath, latestVersion string) error {
	if latestVersion == "" {
		return nil
	}
	dir := filepath.Dir(cachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	// Atomic write: prevents partially-written cache files if multiple CLI instances run.
	tmp, err := os.CreateTemp(dir, "update-check.json.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	enc := json.NewEncoder(tmp)
	enc.SetEscapeHTML(true)
	_ = enc.Encode(updateCheckCache{
		CheckedAt:     time.Now().UTC().Format(time.RFC3339),
		LatestVersion: latestVersion,
	})

	_ = tmp.Close()
	return os.Rename(tmpName, cachePath)
}

type latestResponse struct {
	TagName string `json:"tag_name"`
}

func fetchLatest(endpoint string) (string, bool) {
	client := &http.Client{Timeout: 1 * time.Second}
	resp, err := client.Get(endpoint)
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}

	var lr latestResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return "", false
	}
	if lr.TagName == "" {
		return "", false
	}
	return lr.TagName, true
}

// isNewer returns true if remoteVersion is strictly newer than currentVersion.
// It implements strict semver parsing for `vMAJOR.MINOR.PATCH` (with an optional leading `v`).
func isNewer(remoteVersion, currentVersion string) bool {
	remoteMaj, remoteMin, remotePatch, okRemote := parseStrictSemver(remoteVersion)
	currentMaj, currentMin, currentPatch, okCurrent := parseStrictSemver(currentVersion)
	if !okRemote || !okCurrent {
		return false
	}

	if remoteMaj != currentMaj {
		return remoteMaj > currentMaj
	}
	if remoteMin != currentMin {
		return remoteMin > currentMin
	}
	if remotePatch != currentPatch {
		return remotePatch > currentPatch
	}
	return false
}

func parseStrictSemver(v string) (major, minor, patch int, ok bool) {
	if v == "" || v == "dev" {
		return 0, 0, 0, false
	}
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return 0, 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	min, err2 := strconv.Atoi(parts[1])
	pat, err3 := strconv.Atoi(parts[2])
	if err1 != nil || err2 != nil || err3 != nil {
		return 0, 0, 0, false
	}
	// Strict semver for this project uses only numeric MAJOR/MINOR/PATCH.
	return maj, min, pat, true
}

