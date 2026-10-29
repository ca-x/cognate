package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/clientkey"
)

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
