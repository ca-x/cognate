package repository

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
)

var componentPattern = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9_.-]*$`)

func GitHubURL(input string) (string, error) {
	path := input
	if strings.Contains(input, "://") {
		parsed, err := url.Parse(input)
		if err != nil || parsed.Scheme != "https" || parsed.Host != "github.com" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
			return "", fmt.Errorf("invalid GitHub repository URL")
		}
		path = strings.TrimPrefix(parsed.Path, "/")
	}
	parts := strings.Split(path, "/")
	if len(parts) != 2 {
		return "", fmt.Errorf("repository must be owner/name or a GitHub HTTPS URL")
	}
	parts[1] = strings.TrimSuffix(parts[1], ".git")
	for _, part := range parts {
		if !componentPattern.MatchString(part) || part == "." || part == ".." {
			return "", fmt.Errorf("invalid GitHub repository name")
		}
	}
	return "https://github.com/" + parts[0] + "/" + parts[1] + ".git", nil
}

func Clone(ctx context.Context, input, branch string) (string, func(), error) {
	remote, err := GitHubURL(input)
	if err != nil {
		return "", nil, err
	}
	return clone(ctx, remote, branch)
}

func clone(ctx context.Context, remote, branch string) (string, func(), error) {
	directory, err := os.MkdirTemp("", "skillhub-repo-*")
	if err != nil {
		return "", nil, fmt.Errorf("create temporary directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(directory) }
	root := filepath.Join(directory, strings.TrimSuffix(filepath.Base(remote), ".git"))
	args := []string{"clone", "--quiet", "--depth=1"}
	if branch != "" {
		args = append(args, "--branch="+branch)
	}
	args = append(args, "--", remote, root)
	command := exec.CommandContext(ctx, "git", args...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	if err := command.Run(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("clone repository (check Git, network and branch): %w", err)
	}
	return root, cleanup, nil
}
