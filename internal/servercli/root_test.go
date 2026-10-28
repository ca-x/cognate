package servercli

import (
	"testing"
	"time"
)

func TestListSyncIntervalFlag(t *testing.T) {
	for _, argument := range []string{"", "0", "30s"} {
		t.Run(argument, func(t *testing.T) {
			command := New()
			want := 15 * time.Minute
			if argument != "" {
				if err := command.Flags().Parse([]string{"--list-sync-interval=" + argument}); err != nil {
					t.Fatal(err)
				}
				var err error
				want, err = time.ParseDuration(argument)
				if err != nil {
					t.Fatal(err)
				}
			}
			interval, err := command.Flags().GetDuration("list-sync-interval")
			if err != nil || interval != want {
				t.Fatalf("scheduler interval=%v, want=%v, error=%v", interval, want, err)
			}
		})
	}
}
