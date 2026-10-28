package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/cli"
)

func TestPackSubscribeRejectsCredentialBeforeCreatingLocalState(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux client state")
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	credential := "not-a-valid-share-credential"
	var output bytes.Buffer
	command := cli.New()
	command.SetIn(strings.NewReader(credential))
	command.SetOut(&output)
	command.SetErr(&output)
	command.SetArgs([]string{"pack", "subscribe", "--server", "https://hub.example", "--token-stdin"})
	err := command.ExecuteContext(test.Context())
	if err == nil {
		test.Fatal("invalid credential accepted")
	}
	if strings.Contains(err.Error()+output.String(), credential) {
		test.Fatal("credential leaked through CLI diagnostics")
	}
	for _, directory := range []string{".local", ".config", ".agents"} {
		if _, err := os.Stat(filepath.Join(home, directory)); !os.IsNotExist(err) {
			test.Fatal("invalid credential created client directories")
		}
	}
}

func TestPackListWithoutStateDoesNotCreateIt(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("Linux client state")
	}
	home := test.TempDir()
	test.Setenv("HOME", home)
	var output bytes.Buffer
	command := cli.New()
	command.SetOut(&output)
	command.SetArgs([]string{"pack", "list"})
	if err := command.ExecuteContext(test.Context()); err != nil {
		test.Fatal(err)
	}
	if !strings.Contains(output.String(), "没有") {
		test.Fatal("missing empty subscription status")
	}
	if _, err := os.Stat(filepath.Join(home, ".local")); !os.IsNotExist(err) {
		test.Fatal("listing initialized state")
	}
}
