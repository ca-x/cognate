package install

import (
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/ca-x/cognate/internal/stagename"
	"github.com/ca-x/cognate/internal/state"
	"github.com/spf13/afero"
)

func PlanLocalTargets(home, slug, id, hash string) (state.OperationPlan, error) {
	return planLocalTargets(home, slug, id, hash, nil)
}

func PlanLocalTargetsWithAgents(home, slug, id, hash string, agents []string) (state.OperationPlan, error) {
	selected := make(map[string]bool, len(agents))
	for _, agent := range agents {
		if agent == "" || selected[agent] {
			return state.OperationPlan{}, fmt.Errorf("invalid or duplicate selected agent")
		}
		selected[agent] = true
	}
	return planLocalTargets(home, slug, id, hash, selected)
}

func planLocalTargets(home, slug, id, hash string, selected map[string]bool) (state.OperationPlan, error) {
	if !filepath.IsAbs(home) || ValidateSlug(slug) != nil || !validLocalComponent(id) {
		return state.OperationPlan{}, fmt.Errorf("home, slug or operation id is invalid")
	}
	if len(hash) != 64 || hash != strings.ToLower(hash) {
		return state.OperationPlan{}, fmt.Errorf("invalid content hash")
	}
	if _, err := hex.DecodeString(hash); err != nil {
		return state.OperationPlan{}, fmt.Errorf("invalid content hash: %w", err)
	}
	stageName := stagename.Prefix + id
	if err := stagename.Validate(stageName); err != nil {
		return state.OperationPlan{}, err
	}
	canonical, err := filepath.EvalSymlinks(filepath.Join(home, ".agents", "skills"))
	if err != nil {
		return state.OperationPlan{}, fmt.Errorf("resolve canonical directory: %w", err)
	}
	info, err := os.Stat(canonical)
	if err != nil || !info.IsDir() {
		return state.OperationPlan{}, fmt.Errorf("canonical skills directory is not available")
	}
	plan := state.OperationPlan{ID: id, Action: "install", Slug: slug, ContentHash: hash, Targets: []state.OperationTarget{{Path: filepath.Join(canonical, slug), Kind: "canonical", StageName: stageName}}}
	directories := []os.FileInfo{info}
	targets, err := discoverTargets(afero.NewOsFs(), home, selected)
	if err != nil {
		return state.OperationPlan{}, err
	}
	for _, target := range targets {
		if selected != nil {
			if !selected[target.Agent] {
				continue
			}
			delete(selected, target.Agent)
		}
		info, err := os.Stat(target.SkillsDirectory)
		if errors.Is(err, fs.ErrNotExist) {
			if selected != nil {
				return state.OperationPlan{}, fmt.Errorf("selected agent skills directory is unavailable: %s", target.Agent)
			}
			if _, linkErr := os.Lstat(target.SkillsDirectory); errors.Is(linkErr, fs.ErrNotExist) {
				continue
			}
			return state.OperationPlan{}, fmt.Errorf("agent skills directory cannot be resolved: %s", target.Agent)
		}
		if err != nil || !info.IsDir() {
			return state.OperationPlan{}, fmt.Errorf("agent skills directory is not available: %s", target.Agent)
		}
		duplicate := false
		for _, directory := range directories {
			if os.SameFile(directory, info) {
				duplicate = true
				break
			}
		}
		if duplicate {
			continue
		}
		path, err := filepath.EvalSymlinks(target.SkillsDirectory)
		if err != nil {
			return state.OperationPlan{}, fmt.Errorf("resolve %s skills directory: %w", target.Agent, err)
		}
		resolved, err := os.Stat(path)
		if err != nil || !os.SameFile(info, resolved) {
			return state.OperationPlan{}, fmt.Errorf("%s skills directory changed during planning", target.Agent)
		}
		directories = append(directories, info)
		plan.Targets = append(plan.Targets, state.OperationTarget{Path: filepath.Join(path, slug), Kind: "symlink"})
	}
	if len(selected) != 0 {
		return state.OperationPlan{}, fmt.Errorf("selected agent is unavailable")
	}
	if selected != nil {
		chosen := make(map[string]bool, len(targets))
		for _, target := range targets {
			chosen[target.Agent] = true
		}
		for _, candidate := range targetCandidates() {
			if chosen[candidate.agent] {
				continue
			}
			info, err := os.Stat(filepath.Join(home, candidate.path))
			if err != nil || !info.IsDir() {
				continue
			}
			for _, directory := range directories {
				if os.SameFile(directory, info) {
					return state.OperationPlan{}, fmt.Errorf("unselected agent shares an installation directory: %s", candidate.agent)
				}
			}
		}
	}
	return plan, nil
}

func ValidateSlug(slug string) error {
	return state.ValidateSlug(slug)
}

func validLocalComponent(value string) bool {
	return value != "" && value != "." && value != ".." && !strings.ContainsAny(value, "/\\\x00") && filepath.IsLocal(value)
}
