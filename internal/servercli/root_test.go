package servercli

import (
	"testing"
	"time"
)

func TestBackupFlagsRemainExplicitAndSecretFree(test *testing.T) {
	command := New()
	enabled, err := command.Flags().GetBool("backup-enabled")
	if err != nil || enabled {
		test.Fatal("server backup must remain opt-in")
	}
	for _, name := range []string{"backup-s3-config-file", "backup-recovery-key-file"} {
		value, err := command.Flags().GetString(name)
		if err != nil || value != "" {
			test.Fatal("backup secret file has an implicit default")
		}
	}
	for _, name := range []string{"backup-secret-access-key", "backup-session-token", "backup-recovery-key", "backup-master-key"} {
		if command.Flags().Lookup(name) != nil {
			test.Fatal("raw backup secret is accepted in argv")
		}
	}
	if err := command.Flags().Parse([]string{"--backup-enabled", "--backup-s3-config-file=/private/s3.json", "--backup-recovery-key-file=/private/recovery.key"}); err != nil {
		test.Fatal("explicit backup startup flags unavailable", err)
	}
	if enabled, _ := command.Flags().GetBool("backup-enabled"); !enabled {
		test.Fatal("explicit backup enable flag was lost")
	}
}

func TestBootstrapAdministratorFlags(test *testing.T) {
	command := New()
	for _, name := range []string{"bootstrap-admin-username", "bootstrap-admin-password-file"} {
		value, err := command.Flags().GetString(name)
		if err != nil || value != "" {
			test.Fatal("administrator initialization must be opt-in")
		}
	}
	if command.Flags().Lookup("bootstrap-admin-password") != nil {
		test.Fatal("administrator password must not be accepted in argv")
	}
}

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
