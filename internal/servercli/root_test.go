package servercli

import (
	"testing"
	"time"
)

func TestAdministratorUserIDFlag(test *testing.T) {
	command := New()
	flag := command.Flags().Lookup("admin-user-id")
	if flag == nil {
		test.Fatal("server CLI does not expose explicit administrator enrollment")
	}
	defaults, err := command.Flags().GetStringArray("admin-user-id")
	if err != nil || len(defaults) != 0 {
		test.Fatal("administrator allowlist is not empty by default")
	}
	if err := command.Flags().Parse([]string{"--admin-user-id=first-id", "--admin-user-id=second-id"}); err != nil {
		test.Fatal("administrator allowlist does not accept repeated flags")
	}
	ids, err := command.Flags().GetStringArray("admin-user-id")
	if err != nil || len(ids) != 2 || ids[0] != "first-id" || ids[1] != "second-id" {
		test.Fatal("administrator allowlist lost an explicitly configured ID")
	}
}

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

func TestSourceRefreshIntervalFlag(test *testing.T) {
	for _, argument := range []string{"", "0", "1s", "30s", "24h"} {
		test.Run(argument, func(test *testing.T) {
			command := New()
			expected := 15 * time.Minute
			if argument != "" {
				if err := command.Flags().Parse([]string{"--source-refresh-interval=" + argument}); err != nil {
					test.Fatal(err)
				}
				var err error
				expected, err = time.ParseDuration(argument)
				if err != nil {
					test.Fatal(err)
				}
			}
			actual, err := command.Flags().GetDuration("source-refresh-interval")
			if err != nil || actual != expected {
				test.Fatalf("source refresh interval=%v, expected=%v, error=%v", actual, expected, err)
			}
			lists, err := command.Flags().GetDuration("list-sync-interval")
			if err != nil || lists != 15*time.Minute {
				test.Fatal("source refresh flag changed the independent Lists interval")
			}
		})
	}
	test.Run("invalid-duration", func(test *testing.T) {
		command := New()
		if err := command.Flags().Parse([]string{"--source-refresh-interval=not-a-duration"}); err == nil {
			test.Fatal("invalid duration was accepted")
		}
	})
}
