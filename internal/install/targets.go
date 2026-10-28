package install

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/spf13/afero"
)

type Target struct {
	Agent           string
	SkillsDirectory string
}

func DiscoverTargets(filesystem afero.Fs, home string) ([]Target, error) {
	return discoverTargets(filesystem, home, nil)
}

func DiscoverTargetsForAgents(filesystem afero.Fs, home string, agents []string) ([]Target, error) {
	selected := make(map[string]bool, len(agents))
	for _, agent := range agents {
		selected[agent] = true
	}
	return discoverTargets(filesystem, home, selected)
}

func discoverTargets(filesystem afero.Fs, home string, selected map[string]bool) ([]Target, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("home directory must be absolute")
	}
	var targets []Target
	for _, candidate := range targetCandidates() {
		if selected != nil && !selected[candidate.agent] {
			continue
		}
		path := filepath.Join(home, candidate.path)
		info, err := filesystem.Stat(filepath.Dir(path))
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("inspect %s agent directory: %w", candidate.agent, err)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("%s agent path is not a directory", candidate.agent)
		}
		info, err = filesystem.Stat(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("inspect %s skills directory: %w", candidate.agent, err)
		}
		if err == nil && !info.IsDir() {
			return nil, fmt.Errorf("%s skills path is not a directory", candidate.agent)
		}
		targets = append(targets, Target{Agent: candidate.agent, SkillsDirectory: path})
	}
	return targets, nil
}

type targetCandidate struct {
	agent string
	path  string
}

func targetCandidates() []targetCandidate {
	return []targetCandidate{
		{"claude", filepath.Join(".claude", "skills")},
		{"codex", filepath.Join(".codex", "skills")},
		{"gemini", filepath.Join(".gemini", "skills")},
		{"opencode", filepath.Join(".config", "opencode", "skills")},
		{"cursor", filepath.Join(".cursor", "skills")},
		{"windsurf", filepath.Join(".codeium", "windsurf", "skills")},
		{"adal", filepath.Join(".adal", "skills")},
		{"aider-desk", filepath.Join(".aider-desk", "skills")},
		{"augment", filepath.Join(".augment", "skills")},
		{"codebuddy", filepath.Join(".codebuddy", "skills")},
		{"codemaker", filepath.Join(".codemaker", "skills")},
		{"codestudio", filepath.Join(".codestudio", "skills")},
		{"command-code", filepath.Join(".commandcode", "skills")},
		{"continue", filepath.Join(".continue", "skills")},
		{"crush", filepath.Join(".config", "crush", "skills")},
		{"firebender", filepath.Join(".firebender", "skills")},
		{"forgecode", filepath.Join(".forge", "skills")},
		{"github-copilot", filepath.Join(".copilot", "skills")},
		{"goose", filepath.Join(".config", "goose", "skills")},
		{"iflow-cli", filepath.Join(".iflow", "skills")},
		{"junie", filepath.Join(".junie", "skills")},
		{"kiro-cli", filepath.Join(".kiro", "skills")},
		{"kode", filepath.Join(".kode", "skills")},
		{"lingma", filepath.Join(".lingma", "skills")},
		{"mcpjam", filepath.Join(".mcpjam", "skills")},
		{"mistral-vibe", filepath.Join(".vibe", "skills")},
		{"moxby", filepath.Join(".moxby", "skills")},
		{"mux", filepath.Join(".mux", "skills")},
		{"neovate", filepath.Join(".neovate", "skills")},
		{"ona", filepath.Join(".ona", "skills")},
		{"openhands", filepath.Join(".openhands", "skills")},
		{"pochi", filepath.Join(".pochi", "skills")},
		{"qoder", filepath.Join(".qoder", "skills")},
		{"qoder-cn", filepath.Join(".qoderwork", "skills")},
		{"qwen-code", filepath.Join(".qwen", "skills")},
		{"reasonix", filepath.Join(".reasonix", "skills")},
		{"roo", filepath.Join(".roo", "skills")},
		{"rovodev", filepath.Join(".rovodev", "skills")},
		{"terramind", filepath.Join(".terramind", "skills")},
		{"tinycloud", filepath.Join(".tinycloud", "skills")},
		{"trae", filepath.Join(".trae", "skills")},
		{"trae-cn", filepath.Join(".trae-cn", "skills")},
		{"zencoder", filepath.Join(".zencoder", "skills")},
	}
}
