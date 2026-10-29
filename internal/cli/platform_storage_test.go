//go:build darwin

package cli

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/ca-x/cognate/internal/clientkey"
)

func TestDarwinExecutionCLIAdmission(test *testing.T) {
	home, err := filepath.EvalSymlinks(test.TempDir())
	if err != nil || os.Chmod(home, 0700) != nil {
		test.Fatal("DARWIN_INFRA canonical private home")
	}
	test.Setenv("HOME", home)
	var requests atomic.Int64
	endpoint := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		writer.WriteHeader(http.StatusNotFound)
	}))
	defer endpoint.Close()
	for _, arguments := range [][]string{{"install-public", "--server", endpoint.URL, "--skill", "synthetic-skill"}, {"pack", "sync"}, {"install-local", "unread-source", "--slug", "demo"}} {
		command := New()
		command.SetArgs(arguments)
		command.SetIn(bytes.NewReader(nil))
		command.SetOut(new(bytes.Buffer))
		command.SetErr(new(bytes.Buffer))
		err := command.ExecuteContext(test.Context())
		if requests.Load() != 0 {
			test.Fatal("uninitialized command contacted server")
		}
		for _, relative := range []string{".agents", ".local/share/skillhub/client.db", ".config/skillhub/client.key", ".config/skillhub/client-key-use.v1"} {
			if _, err := os.Lstat(filepath.Join(home, relative)); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("uninitialized command created business state")
			}
		}
		if !errors.Is(err, clientkey.ErrInitializationRequired) {
			if errors.Is(err, clientkey.ErrUnsupported) {
				test.Log("DARWIN_BASELINE_REJECTION code=cli-unsupported")
			}
			test.Fatal("Darwin CLI must admit through explicit-init core")
		}
	}
}
