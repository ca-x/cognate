//go:build !linux

package cli

import (
	"bytes"
	"errors"
	"os"
	"testing"

	"github.com/ca-x/cognate/internal/clientkey"
)

func assertClientKeyHomeEmpty(test *testing.T, home string) {
	test.Helper()
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		test.Fatal("unsupported/invalid operation changed home")
	}
}

func TestClientKeyEntrypointsUnsupported(test *testing.T) {
	for _, arguments := range [][]string{{"client-key", "init"}, {"pack", "sync"}, {"install-local", "unread", "--slug=demo"}} {
		home := test.TempDir()
		test.Setenv("HOME", home)
		command := New()
		command.SetArgs(arguments)
		command.SetOut(new(bytes.Buffer))
		if !errors.Is(command.ExecuteContext(test.Context()), clientkey.ErrUnsupported) {
			test.Fatal("unsupported platform claimed key capability")
		}
		assertClientKeyHomeEmpty(test, home)
	}
}
