package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/search"
)

type fakeSearch struct {
	query   string
	limit   int
	results []search.Result
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestSearchCommandReturnsOutputFailure(t *testing.T) {
	command := newSearchCommand(&fakeSearch{results: []search.Result{{Name: "demo", Source: "owner/repo", SkillID: "demo", Installs: 1}}})
	command.SetOut(failingWriter{})
	command.SetArgs([]string{"query"})
	if err := command.ExecuteContext(t.Context()); err == nil {
		t.Fatal("ignored output failure")
	}
}

func (fake *fakeSearch) Search(_ context.Context, query string, limit int) ([]search.Result, error) {
	fake.query, fake.limit = query, limit
	return fake.results, nil
}

func TestSearchCommandDisplaysSanitizedResults(t *testing.T) {
	fake := &fakeSearch{results: []search.Result{{Name: "test\x1b[31mname", Source: "owner/repo", SkillID: "demo", Installs: 42}}}
	command := newSearchCommand(fake)
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"go", "--limit", "2"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if fake.query != "go" || fake.limit != 2 || !strings.Contains(output.String(), "owner/repo") || !strings.Contains(output.String(), "42") || strings.Contains(output.String(), "\x1b") {
		t.Fatalf("output = %q, query = %q, limit = %d", output.String(), fake.query, fake.limit)
	}
}

func TestSearchCommandDisplaysEmptyResults(t *testing.T) {
	command := newSearchCommand(&fakeSearch{})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetArgs([]string{"nothing"})
	if err := command.ExecuteContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "未发现") {
		t.Fatalf("empty output = %q", output.String())
	}
}
