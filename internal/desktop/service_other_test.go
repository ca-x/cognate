//go:build !linux

package desktop

import (
	"strings"
	"testing"
)

func TestUnsupportedInstallationIsHonest(test *testing.T) {
	service := &Service{Home: test.TempDir()}
	if err := service.Install(test.Context(), "https://example.org", nil); err == nil || !strings.Contains(err.Error(), "unsupported") {
		test.Fatal("non-Linux installation must fail honestly")
	}
	if _, err := service.Packs(test.Context(), "sync", "https://example.org", "", ""); err == nil {
		test.Fatal("non-Linux Pack kernel must not report success")
	}
}
