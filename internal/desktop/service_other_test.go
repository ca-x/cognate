//go:build !linux

package desktop

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/ca-x/cognate/internal/clientkey"
	"github.com/egoist/mygo/ui"
)

func TestClientKeyEntrypointsUnsupported(test *testing.T) {
	service := &Service{Home: test.TempDir()}
	if err := service.InitializeClientKey(test.Context()); !errors.Is(err, clientkey.ErrUnsupported) {
		test.Fatal("non-Linux initialization did not fail closed")
	}
	model, pump := modelFixture(test, service)
	model.Page = 3
	tester := ui.NewTester(model.View, 920, 760)
	if !tester.HasText("本机客户端密钥初始化与本地安装当前仅支持 Linux") {
		test.Fatal("unsupported native action has no visible explanation")
	}
	tester.Click(initializeClientKeyLabel)
	pump()
	if model.pendingJobs.Load() != 0 {
		test.Fatal("disabled unsupported button started initialization")
	}
	if entries, err := os.ReadDir(service.Home); err != nil || len(entries) != 0 {
		test.Fatal("unsupported entrypoint modified filesystem")
	}
}

func TestUnsupportedInstallationIsHonest(test *testing.T) {
	service := &Service{Home: test.TempDir()}
	if err := service.InitializeClientKey(test.Context()); !errors.Is(err, clientkey.ErrUnsupported) {
		test.Fatal("non-Linux initialization must use the core unsupported result")
	}
	if entries, err := os.ReadDir(service.Home); err != nil || len(entries) != 0 {
		test.Fatal("unsupported initialization changed the filesystem")
	}
	if err := service.Install(test.Context(), "https://example.org", nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		test.Fatal("non-Linux installation must fail honestly")
	}
	if _, err := service.Packs(test.Context(), "sync", "https://example.org", "", ""); err == nil {
		test.Fatal("non-Linux Pack kernel must not report success")
	}
}
