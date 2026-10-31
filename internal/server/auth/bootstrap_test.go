package auth

import (
	"bytes"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestBootstrapAdministratorPreservesIdentityAndPassword(test *testing.T) {
	service, databasePath := testService(test)
	password := []byte(rand.Text())
	passwordFile := filepath.Join(filepath.Dir(databasePath), "bootstrap.password")
	if err := os.WriteFile(passwordFile, password, 0o600); err != nil {
		test.Fatal(err)
	}
	user, err := service.BootstrapAdministrator(test.Context(), "Admin", passwordFile)
	if err != nil || user == "" {
		test.Fatalf("bootstrap failed: %v", err)
	}
	if _, err := service.Login(test.Context(), "admin", password); err != nil {
		test.Fatal("initial password did not authenticate")
	}
	replacement := []byte(rand.Text())
	if err := os.WriteFile(passwordFile, replacement, 0o600); err != nil {
		test.Fatal(err)
	}
	if actual, err := service.BootstrapAdministrator(test.Context(), "ADMIN", passwordFile); err != nil || actual != user {
		test.Fatalf("bootstrap identity changed: %v", err)
	}
	if _, err := service.Login(test.Context(), "admin", replacement); !errors.Is(err, ErrInvalidCredentials) {
		test.Fatal("restart replaced administrator password")
	}
	if err := os.Remove(passwordFile); err != nil {
		test.Fatal(err)
	}
	if actual, err := service.BootstrapAdministrator(test.Context(), "admin", passwordFile); err != nil || actual != user {
		test.Fatal("restart required original password file")
	}
	if _, err := service.BootstrapAdministrator(test.Context(), "other-admin", passwordFile); err == nil {
		test.Fatal("restart rebound administrator identity")
	}
	if _, err := service.Login(test.Context(), "admin", password); err != nil {
		test.Fatal("initial password stopped working")
	}
	for _, path := range []string{databasePath, databasePath + "-wal"} {
		data, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			test.Fatal(err)
		}
		if bytes.Contains(data, password) || bytes.Contains(data, replacement) {
			test.Fatal("plaintext bootstrap password in database")
		}
	}
}

func TestBootstrapAdministratorRefusesExistingAccounts(test *testing.T) {
	service, _ := testService(test)
	if _, err := service.Register(test.Context(), "existing", []byte(rand.Text())); err != nil {
		test.Fatal(err)
	}
	for _, username := range []string{"existing", "new-admin"} {
		if _, err := service.BootstrapAdministrator(test.Context(), username, "/missing/password"); err == nil {
			test.Fatal("existing instance admitted administrator initialization")
		}
	}
}

func TestBootstrapAdministratorRejectsUnsafePasswordInput(test *testing.T) {
	for _, scenario := range []string{"missing", "short", "oversized", "public", "symlink"} {
		test.Run(scenario, func(test *testing.T) {
			service, databasePath := testService(test)
			path := filepath.Join(filepath.Dir(databasePath), "bootstrap.password")
			password := []byte(rand.Text())
			if scenario == "short" {
				password = password[:7]
			}
			if scenario == "oversized" {
				password = bytes.Repeat(password, 64)
			}
			if scenario != "missing" {
				if err := os.WriteFile(path, password, 0o600); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "public" {
				if err := os.Chmod(path, 0o644); err != nil {
					test.Fatal(err)
				}
			}
			if scenario == "symlink" {
				link := path + ".link"
				if err := os.Symlink(path, link); err != nil {
					test.Skip("symlinks unavailable")
				}
				path = link
			}
			if _, err := service.BootstrapAdministrator(test.Context(), "admin", path); err == nil {
				test.Fatal("unsafe bootstrap input accepted")
			}
			if _, err := service.store.PasswordAccount(test.Context(), "admin"); err == nil {
				test.Fatal("failed initialization left an account")
			}
		})
	}
}
