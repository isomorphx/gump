package engine

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/isomorphx/gump/internal/brand"
	"github.com/isomorphx/gump/internal/workflow"
)

func pathAllowedNoWrite(rel string) bool {
	norm := filepath.ToSlash(strings.TrimSpace(rel))
	norm = strings.TrimPrefix(norm, "./")
	p := brand.StateDir()
	if norm == p+"/out" || strings.HasPrefix(norm, p+"/out/") {
		return true
	}
	if norm == p+"/artefacts" || strings.HasPrefix(norm, p+"/artefacts/") {
		return true
	}
	if strings.HasPrefix(norm, p+"/") && (strings.Contains(norm, "engine-step-attempt") || strings.Contains(norm, "stub-launch-seq")) {
		return true
	}
	return false
}

func checkGitWorktreeNoWrite(worktree, baseCommit string) error {
	if worktree == "" || baseCommit == "" {
		return nil
	}
	seen := make(map[string]struct{})
	var paths []string
	if out, err := exec.Command("git", "-C", worktree, "diff", "--name-only", baseCommit).Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; !ok {
				seen[line] = struct{}{}
				paths = append(paths, line)
			}
		}
	}
	if out, err := exec.Command("git", "-C", worktree, "ls-files", "--others", "--exclude-standard").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			if _, ok := seen[line]; !ok {
				seen[line] = struct{}{}
				paths = append(paths, line)
			}
		}
	}
	var bad []string
	for _, line := range paths {
		if pathAllowedNoWrite(line) {
			continue
		}
		if strings.HasSuffix(filepath.ToSlash(line), ".stub") {
			continue
		}
		if isProviderContextFileLine(line) {
			continue
		}
		bad = append(bad, line)
	}
	if len(bad) > 0 {
		return fmt.Errorf("no_write: changes outside %s/out: %s", brand.StateDir(), strings.Join(bad, ", "))
	}
	return nil
}

func isProviderContextFileLine(line string) bool {
	norm := filepath.ToSlash(strings.TrimSpace(line))
	switch norm {
	case "AGENTS.md", "CLAUDE.md", "GEMINI.md", "QWEN.md", "CODEX.md", "CURSOR.md":
		return true
	}
	return strings.HasPrefix(norm, brand.StateDir()+"-original-")
}

func checkNonGitDirNoWrite(root string) error {
	return filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if pathAllowedNoWrite(rel) {
			return nil
		}
		if strings.HasSuffix(filepath.ToSlash(rel), ".stub") {
			return nil
		}
		if isProviderContextFileLine(rel) {
			return nil
		}
		return fmt.Errorf("no_write: %s", rel)
	})
}

func (e *Engine) maybeCheckNoWritePostStep(_ *workflow.Step, gr *GuardRuntime, baseCommit, agentWT, _ string) error {
	if gr == nil || gr.cfg.NoWrite == nil || !*gr.cfg.NoWrite {
		return nil
	}
	checkWT := agentWT
	if _, err := os.Stat(filepath.Join(checkWT, ".git")); err == nil {
		return checkGitWorktreeNoWrite(checkWT, baseCommit)
	}
	return checkNonGitDirNoWrite(checkWT)
}
