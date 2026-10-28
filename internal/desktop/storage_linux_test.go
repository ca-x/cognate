package desktop

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCredentialKeyPersistenceAndUntrustedStorage(test *testing.T) {
	home := test.TempDir()
	key, err := clientKey(home, true)
	if err != nil {
		test.Fatal(err)
	}
	again, err := clientKey(home, false)
	if err != nil || !bytes.Equal(key, again) {
		test.Fatal("shared client.key was not reused")
	}
	clear(key)
	clear(again)
	path := filepath.Join(home, ".config", "skillhub", "client.key")
	if err := os.Chmod(path, 0644); err != nil {
		test.Fatal(err)
	}
	if _, err := clientKey(home, false); err == nil {
		test.Fatal("publicly readable credential key accepted")
	}
	if err := os.Remove(path); err != nil {
		test.Fatal(err)
	}
	if _, err := clientKey(home, false); err == nil {
		test.Fatal("existing subscription key silently replaced")
	}
	target := filepath.Join(test.TempDir(), "key")
	if err := os.WriteFile(target, make([]byte, 32), 0600); err != nil {
		test.Fatal(err)
	}
	if err := os.Symlink(target, path); err != nil {
		test.Fatal(err)
	}
	if _, err := clientKey(home, true); err == nil {
		test.Fatal("symlinked credential key accepted")
	}

	other := test.TempDir()
	if err := os.Symlink(test.TempDir(), filepath.Join(other, ".local")); err != nil {
		test.Fatal(err)
	}
	if _, err := prepareHome(other); err == nil {
		test.Fatal("symlinked state ancestor accepted")
	}
}
