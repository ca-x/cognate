package desktop

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
)

func TestDesktopWatchCycleFreshManagerReleasesLock(test *testing.T) {
	fixture := realServer(test)
	service := &Service{Home: fixture.home, HTTP: fixture.server.Client()}
	if err := service.InitializeClientKey(test.Context()); err != nil {
		test.Fatal("explicit watch fixture initialization failed")
	}
	if _, err := service.Packs(test.Context(), "subscribe", fixture.server.URL, fixture.token, ""); err != nil {
		test.Fatal("could not prepare synthetic subscription")
	}
	for attempt := range 2 {
		result, records, err := service.WatchCycle(test.Context())
		if err != nil || len(records) != 1 || records[0].AppliedRevision == 0 || attempt == 0 && result.Installed != 1 || attempt == 1 && result.Reused != 1 {
			test.Fatal("watch cycle did not use real shared subscription installation")
		}
		ctx, cancel := context.WithTimeout(test.Context(), time.Second)
		session, err := install.OpenSession(ctx, filepath.Join(fixture.home, ".local", "share", "skillhub"))
		cancel()
		if err != nil {
			test.Fatal("completed cycle retained the installer lock")
		}
		if err := session.Close(); err != nil {
			test.Fatal(err)
		}
	}
}
