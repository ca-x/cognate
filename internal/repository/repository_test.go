package repository_test

import (
	"testing"

	"github.com/ca-x/skillhub/internal/repository"
)

func TestGitHubURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"vercel-labs/skills", "https://github.com/vercel-labs/skills.git"},
		{"https://github.com/vercel-labs/skills.git", "https://github.com/vercel-labs/skills.git"},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			got, err := repository.GitHubURL(test.input)
			if err != nil || got != test.want {
				t.Fatalf("got %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestGitHubURLRejectsCredentialsAndInvalidInput(t *testing.T) {
	for _, input := range []string{
		"https://user:secret@github.com/owner/repo",
		"https://example.com/owner/repo",
		"owner/repo/extra",
		"-option/repo",
		"owner/../repo",
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := repository.GitHubURL(input); err == nil {
				t.Fatal("expected rejection")
			}
		})
	}
}
