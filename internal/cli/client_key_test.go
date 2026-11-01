package cli

import (
	"bytes"
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/clientkey"
)

func darwinClientKeyPTYDiagnostic(output string) string {
	if len(output) > 64<<10 {
		return "unknown"
	}
	classification := "unknown"
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSuffix(line, "\r")
		rest, found := strings.CutPrefix(line, "    client_key_output_darwin_test.go:")
		if !found {
			continue
		}
		lineNumber, phase, found := strings.Cut(rest, ": DARWIN_PTY_DIAGNOSTIC ")
		if !found || len(lineNumber) == 0 || len(lineNumber) > 5 || lineNumber[0] == '0' {
			continue
		}
		if _, err := strconv.ParseUint(lineNumber, 10, 16); err != nil {
			continue
		}
		switch phase {
		case "fixture", "identity", "flags-before", "independent-open", "write", "close", "borrowed-state",
			"routing-poll", "routing-read", "borrowed-usable", "backpressure-open", "partial-deadline", "fixed-cap",
			"plain-cancel", "close-join", "disconnect-open", "disconnect-master", "disconnect-write", "original-output":
			if classification != "unknown" && classification != phase {
				return "ambiguous"
			}
			classification = phase
		}
	}
	return classification
}

func TestClientKeyPTYDiagnostics(test *testing.T) {
	prefix := "    client_key_output_darwin_test.go:300: DARWIN_PTY_DIAGNOSTIC "
	cases := []struct {
		name, input, expected string
	}{
		{"recognized", prefix + "independent-open\n", "independent-open"},
		{"crlf", prefix + "routing-read\r\n", "routing-read"},
		{"repeated", strings.Repeat(prefix+"fixed-cap\n", 2), "fixed-cap"},
		{"ambiguous", prefix + "fixed-cap\n" + prefix + "close-join\n", "ambiguous"},
		{"surrounding-private", "SYNTHETIC_PRIVATE_CANARY\n" + prefix + "borrowed-state\nSYNTHETIC_PRIVATE_CANARY", "borrowed-state"},
		{"unknown-phase", prefix + "SYNTHETIC_PRIVATE_CANARY\n", "unknown"},
		{"extra-body", prefix + "independent-open SYNTHETIC_PRIVATE_CANARY\n", "unknown"},
		{"wrong-source", strings.ReplaceAll(prefix, "client_key_output_darwin_test.go", "private-user.go") + "fixed-cap\n", "unknown"},
		{"missing-line", strings.ReplaceAll(prefix, ":300:", ":missing:") + "fixed-cap\n", "unknown"},
		{"zero-line", strings.ReplaceAll(prefix, ":300:", ":0:") + "fixed-cap\n", "unknown"},
		{"unanchored", "SYNTHETIC_PRIVATE_CANARY" + prefix + "fixed-cap\n", "unknown"},
		{"empty", "", "unknown"},
		{"oversized", strings.Repeat("SYNTHETIC_PRIVATE_CANARY", 4096) + "\n" + prefix + "fixed-cap\n", "unknown"},
	}
	for _, fixture := range cases {
		test.Run(fixture.name, func(test *testing.T) {
			if actual := darwinClientKeyPTYDiagnostic(fixture.input); actual != fixture.expected {
				test.Fatal("unexpected fixed diagnostic classification")
			}
		})
	}
}

func TestClientKeyEntrypointsClassification(test *testing.T) {
	for _, sentinel := range []error{clientkey.ErrInitializationRequired, clientkey.ErrRecoveryRequired, clientkey.ErrUnavailable, clientkey.ErrUnsupported} {
		wrapped := errors.Join(errors.New("synthetic-private-key-error"), sentinel)
		if !clientKeyFailure(wrapped) || strings.Contains(clientKeyErrorMessage(wrapped), "synthetic-private") {
			test.Fatal("core classification was lost or exposed raw error")
		}
		for _, cancellation := range []error{context.Canceled, context.DeadlineExceeded} {
			if clientKeyErrorMessage(errors.Join(wrapped, cancellation)) != clientKeyErrorMessage(cancellation) {
				test.Fatal("cancellation did not take precedence")
			}
		}
		if nativeErrorMessage(wrapped) != clientKeyErrorMessage(wrapped) {
			test.Fatal("native configuration dropped core classification")
		}
	}
	if clientKeyFailure(context.Canceled) || clientKeyFailure(errors.New("client key unavailable")) {
		test.Fatal("classification matched text or generic cancellation")
	}
}

func TestClientKeyEntrypointsOutputBound(test *testing.T) {
	buffer := new(bytes.Buffer)
	output, err := prepareClientKeyOutput(test.Context(), buffer)
	if err != nil {
		test.Fatal(err)
	}
	defer output.Close()
	if _, err := output.Write(bytes.Repeat([]byte{'a'}, 4095)); err != nil {
		test.Fatal(err)
	}
	if _, err := output.Write([]byte("bc")); err == nil || buffer.Len() != 4095 {
		test.Fatal("combined output exceeded 4KiB")
	}
	ctx, cancel := context.WithCancel(test.Context())
	nested, err := prepareClientKeyOutput(ctx, output)
	if err != nil {
		test.Fatal(err)
	}
	defer nested.Close()
	cancel()
	if _, err := nested.Write([]byte("c")); !errors.Is(err, context.Canceled) || buffer.Len() != 4095 {
		test.Fatal("nested output lost cancellation")
	}
}

func TestClientKeyEntrypointsInvalidBeforeAdmission(test *testing.T) {
	for _, arguments := range [][]string{{"client-key", "unknown-private-argument"}, {"client-key", "init", "extra"}, {"client-key", "init", "--timeout=0s"}, {"client-key", "init", "--timeout=121s"}, {"client-key", "init", "--force"}} {
		test.Run(strings.Join(arguments, " "), func(test *testing.T) {
			home := test.TempDir()
			test.Setenv("HOME", home)
			command := New()
			command.SetArgs(arguments)
			command.SetOut(new(bytes.Buffer))
			if command.ExecuteContext(test.Context()) == nil {
				test.Fatal("invalid initialization accepted")
			}
			assertClientKeyHomeEmpty(test, home)
		})
	}
	for _, arguments := range [][]string{{"client-key", "--help"}, {"client-key", "init", "--help"}} {
		home := test.TempDir()
		test.Setenv("HOME", home)
		command := New()
		command.SetArgs(arguments)
		command.SetOut(new(bytes.Buffer))
		if err := command.ExecuteContext(test.Context()); err != nil {
			test.Fatal(err)
		}
		assertClientKeyHomeEmpty(test, home)
	}
}
