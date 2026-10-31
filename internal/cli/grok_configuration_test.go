package cli

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestGrokCommandRegistration(test *testing.T) {
	command := New()
	command.SetArgs([]string{"grok-assets", "--help"})
	var output bytes.Buffer
	command.SetOut(&output)
	command.SetErr(&output)
	if err := command.ExecuteContext(test.Context()); err != nil || !strings.Contains(output.String(), "order") || !strings.Contains(output.String(), "NOT server-validated") {
		test.Fatal("Grok owner command missing complete safe help")
	}
}

type grokRefuseInput struct{ reads int }

func (input *grokRefuseInput) Read([]byte) (int, error) {
	input.reads++
	return 0, io.EOF
}

func TestGrokFlagsRefuseBeforeCredentials(test *testing.T) {
	for _, args := range [][]string{
		{"list", "--apply=false"}, {"list", "--id", "private-canary"}, {"get"}, {"get", "--id", "id", "--expected-revision", "1"},
		{"create", "--draft-file", "file", "--key-action", "keep"}, {"create", "--draft-file", "file", "--key-action="},
		{"create", "--draft-file", "file", "--key-action", "replace"}, {"create", "--draft-file", "file", "--key-action", "clear", "--key-file="},
		{"create", "--draft-file", "file", "--key-file", "private-canary"}, {"update", "--id", "id", "--draft-file", "file"},
		{"update", "--id", "id", "--draft-file", "file", "--expected-revision", "-1"}, {"copy", "--id", "id", "--name", "private-canary"},
		{"delete", "--id", "id", "--expected-revision", "0"}, {"order"}, {"order", "set"}, {"order", "get", "--ids-file", "private-canary"},
		{"order", "replace", "--ids-file", "file"}, {"order", "replace", "--ids-file", "file", "--expected-order-revision", "-1"},
		{"list", "--token", "private-canary"}, {"list", "--owner", "private-canary"}, {"list", "extra-private-canary"}, {"private-canary"},
	} {
		test.Run(strings.Join(args, " "), func(test *testing.T) {
			command := New()
			command.SetArgs(append(append([]string{"grok-assets"}, args...), "--server", "https://127.0.0.1:1", "--token-stdin"))
			input := &grokRefuseInput{}
			command.SetIn(input)
			var output bytes.Buffer
			command.SetOut(&output)
			command.SetErr(&output)
			err := command.ExecuteContext(test.Context())
			if !errors.Is(err, errGrokCommand) || input.reads != 0 || strings.Contains(output.String(), "private-canary") {
				test.Fatal("invalid action/flag reached credentials or leaked values")
			}
		})
	}
}

func TestGrokKnownErrorsRemainSafe(test *testing.T) {
	for _, err := range []error{errGrokCommand, errGrokUnknown, errGrokDenied, errGrokConflict, errGrokInUse, errGrokUnsupported, errGrokExpired, errGrokNotFound, errGrokLimit} {
		if grokCommandError(err) != err || grokCommandError(grokCommandError(err)) != err {
			test.Fatal("safe error identity lost")
		}
	}
	if grokCommandError(errors.New("private-canary")) != errGrokCommand {
		test.Fatal("raw error escape")
	}
}
