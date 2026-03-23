package engine

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/isomorphx/gump/internal/recipe"
)

func joinStepPath(pathPrefix, name string) string {
	if pathPrefix == "" {
		return name
	}
	return pathPrefix + "/" + name
}

func findStepIndexByName(steps []recipe.Step, name string) int {
	for i := range steps {
		if steps[i].Name == name {
			return i
		}
	}
	return -1
}

// ErrRestartFrom signals that the workflow should reset the worktree and resume at the named sibling step.
type ErrRestartFrom struct {
	TargetName  string
	CurrentPath string
}

func (e *ErrRestartFrom) Error() string {
	return fmt.Sprintf("restart_from:%s", e.TargetName)
}

// CommitBeforeLatestStepSnapshot returns the parent of the most recent [gump] snapshot commit for stepName, or false if none.
func CommitBeforeLatestStepSnapshot(worktree, stepName string) (commit string, ok bool, err error) {
	grep := fmt.Sprintf("step:%s task:", stepName)
	cmd := exec.Command("git", "log", "-1", "--format=%H", "--grep", grep, "HEAD")
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil || len(strings.TrimSpace(string(out))) == 0 {
		return "", false, nil
	}
	h := strings.TrimSpace(string(out))
	p := exec.Command("git", "rev-parse", h+"^")
	p.Dir = worktree
	pout, err := p.Output()
	if err != nil {
		return "", false, err
	}
	return strings.TrimSpace(string(pout)), true, nil
}

func gitCleanFD(worktree string) error {
	cmd := exec.Command("git", "clean", "-fd")
	cmd.Dir = worktree
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git clean -fd: %w: %s", err, out)
	}
	return nil
}

func gitDiffNameOnly(worktree, base, head string) ([]string, error) {
	if base == "" || head == "" || base == head {
		return nil, nil
	}
	cmd := exec.Command("git", "diff", "--name-only", base+".."+head)
	cmd.Dir = worktree
	out, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
