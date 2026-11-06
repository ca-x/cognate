package githubproxysettings

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSettingsRejectCorruptOrForeignCiphertext(test *testing.T) {
	for name, plaintext := range map[string]string{
		"empty":             "",
		"missing fields":    `{}`,
		"missing URL":       `{"version":1,"revision":2}`,
		"null URL":          `{"version":1,"revision":2,"proxy_url":null}`,
		"missing revision":  `{"version":1,"proxy_url":""}`,
		"version":           `{"version":2,"revision":2,"proxy_url":""}`,
		"revision zero":     `{"version":1,"revision":0,"proxy_url":""}`,
		"negative revision": `{"version":1,"revision":-1,"proxy_url":""}`,
		"duplicate field":   `{"version":1,"revision":2,"revision":3,"proxy_url":""}`,
		"unknown field":     `{"version":1,"revision":2,"proxy_url":"","unknown":true}`,
		"invalid URL":       `{"version":1,"revision":2,"proxy_url":"https://synthetic-proxy-password@localhost/path"}`,
		"invalid JSON":      `synthetic-proxy-password`,
	} {
		test.Run(name, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			sealed := manager.aead.Seal(nil, nil, []byte(plaintext), []byte(settingsAAD))
			if err := manager.Close(); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "network", settingsName)
			if err := os.WriteFile(path, sealed, 0o600); err != nil {
				test.Fatal(err)
			}
			opened, err := Open(test.Context(), directory, testMaster())
			if opened != nil || !errors.Is(err, ErrUnavailable) {
				test.Fatalf("malformed authenticated settings were accepted: %v", err)
			}
			assertPrivate(test, err)
			after, readError := os.ReadFile(path)
			if readError != nil || !bytes.Equal(after, sealed) {
				test.Fatal("failed open overwrote the original settings")
			}
		})
	}
	for _, failure := range []string{"wrong master", "wrong AAD", "tamper", "truncate", "plaintext", "oversize"} {
		test.Run(failure, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			if _, err := manager.Update(test.Context(), 1, syntheticURL); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "network", settingsName)
			sealed, err := os.ReadFile(path)
			if err != nil {
				test.Fatal(err)
			}
			if failure == "wrong AAD" {
				sealed = manager.aead.Seal(nil, nil, []byte(`{"version":1,"revision":2,"proxy_url":""}`), []byte(settingsAAD+"/other"))
			}
			if err := manager.Close(); err != nil {
				test.Fatal(err)
			}
			master := testMaster()
			switch failure {
			case "wrong master":
				master[0] ^= 1
			case "tamper":
				sealed[len(sealed)-1] ^= 1
			case "truncate":
				sealed = sealed[:8]
			case "plaintext":
				sealed = []byte(syntheticURL)
			case "oversize":
				sealed = bytes.Repeat([]byte{'x'}, maxSettingsBytes+1)
			}
			if err := os.WriteFile(path, sealed, 0o600); err != nil {
				test.Fatal(err)
			}
			opened, err := Open(test.Context(), directory, master)
			if opened != nil || !errors.Is(err, ErrUnavailable) {
				test.Fatalf("untrusted ciphertext was accepted: %v", err)
			}
			assertPrivate(test, err)
		})
	}
}

func TestSettingsAtomicWriteFailures(test *testing.T) {
	for _, point := range []string{"create", "write", "short write", "file sync", "file close", "rename", "renamed error", "directory sync", "cleanup"} {
		test.Run(point, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			previous, err := manager.Update(test.Context(), 1, syntheticURL)
			if err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "network", settingsName)
			before, err := os.ReadFile(path)
			if err != nil {
				test.Fatal(err)
			}
			original := manager.files
			manager.files = faultFiles{atomicFiles: original, point: point}
			got, err := manager.Update(test.Context(), previous.Revision, "socks5h://127.0.0.1:19090")
			if !errors.Is(err, ErrUnavailable) || got != (Snapshot{}) {
				test.Fatalf("failed persistence reported success: %+v, %v", got, err)
			}
			assertPrivate(test, err)
			uncertain := point == "rename" || point == "renamed error" || point == "directory sync" || point == "cleanup"
			if uncertain {
				assertFailClosed(test, manager, err)
				if err := manager.Close(); err != nil {
					test.Fatal(err)
				}
				reopened := openTestManager(test, directory)
				recovered, err := reopened.Get(test.Context())
				want := previous
				if point != "rename" {
					want = Snapshot{Revision: 3, Configured: true, Endpoint: "socks5h://127.0.0.1:19090"}
				}
				if err != nil || recovered != want {
					test.Fatalf("restart did not resolve persistence outcome: %+v, %v", recovered, err)
				}
			} else {
				after, err := os.ReadFile(path)
				if err != nil || !bytes.Equal(before, after) {
					test.Fatal("pre-commit failure changed the durable record")
				}
				if got, err := manager.Get(test.Context()); err != nil || got != previous {
					test.Fatal("pre-commit failure changed the active revision")
				}
				manager.files = original
				if _, err := manager.Update(test.Context(), previous.Revision, ""); err != nil {
					test.Fatalf("safe pre-commit retry failed: %v", err)
				}
			}
			entries, err := os.ReadDir(filepath.Dir(path))
			if err != nil {
				test.Fatal(err)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".tmp") {
					test.Fatal("atomic writer left a candidate file")
				}
			}
		})
	}
}

func TestSettingsCancellationAroundCommit(test *testing.T) {
	for _, afterCommit := range []bool{false, true} {
		name := "before rename"
		if afterCommit {
			name = "after rename"
		}
		test.Run(name, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			ctx, cancel := context.WithCancelCause(test.Context())
			defer cancel(nil)
			point := "cancel before"
			if afterCommit {
				point = "cancel after"
			}
			manager.files = faultFiles{atomicFiles: manager.files, point: point, cancel: func() { cancel(errors.New("synthetic-proxy-password")) }}
			got, err := manager.Update(ctx, 1, syntheticURL)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) || got != (Snapshot{}) {
				test.Fatalf("canceled update reported success or lost cancellation: %+v, %v", got, err)
			}
			assertPrivate(test, err)
			if afterCommit {
				assertFailClosed(test, manager, err)
			} else if snapshot, err := manager.Get(test.Context()); err != nil || snapshot != (Snapshot{Revision: 1}) {
				test.Fatal("pre-commit cancellation changed the active revision")
			}
		})
	}
}

func TestSettingsCancellationAndCleanupFailure(test *testing.T) {
	manager := openTestManager(test, testDirectory(test))
	ctx, cancel := context.WithCancelCause(test.Context())
	defer cancel(nil)
	manager.files = faultFiles{
		atomicFiles: faultFiles{atomicFiles: manager.files, point: "cancel after", cancel: func() { cancel(syntheticFailure) }},
		point:       "cleanup",
	}
	snapshot, err := manager.Update(ctx, 1, syntheticURL)
	if snapshot != (Snapshot{}) || !errors.Is(err, context.Canceled) || !errors.Is(err, ErrUnavailable) {
		test.Fatalf("uncertain cleanup lost cancellation or reported success: %v", err)
	}
	assertPrivate(test, err)
	assertFailClosed(test, manager, err)
}

func TestSettingsExternalModificationFailsClosed(test *testing.T) {
	for _, modification := range []string{"delete", "replace", "lock delete", "directory move", "public permissions", "public data root"} {
		test.Run(modification, func(test *testing.T) {
			directory := testDirectory(test)
			manager := openTestManager(test, directory)
			if _, err := manager.Update(test.Context(), 1, syntheticURL); err != nil {
				test.Fatal(err)
			}
			path := filepath.Join(directory, "network", settingsName)
			switch modification {
			case "delete":
				if err := os.Remove(path); err != nil {
					test.Fatal(err)
				}
			case "replace":
				if err := os.WriteFile(path, []byte("synthetic-proxy-password"), 0o600); err != nil {
					test.Fatal(err)
				}
			case "lock delete":
				if err := os.Remove(filepath.Join(directory, "network", lockName)); err != nil {
					test.Fatal(err)
				}
			case "directory move":
				if err := os.Rename(filepath.Dir(path), filepath.Join(directory, "moved-network")); err != nil {
					test.Fatal(err)
				}
			case "public permissions":
				if err := os.Chmod(path, 0o644); err != nil {
					test.Fatal(err)
				}
			case "public data root":
				if err := os.Chmod(directory, 0o755); err != nil {
					test.Fatal(err)
				}
			}
			_, err := manager.Update(test.Context(), 2, "")
			assertFailClosed(test, manager, err)
		})
	}
}

func TestSettingsEncryptedRecordFormatting(test *testing.T) {
	assertPrivate(test, settingsRecord{Version: 1, Revision: 2, ProxyURL: new(syntheticURL)})
}

func assertFailClosed(test *testing.T, manager *Manager, cause error) {
	test.Helper()
	if !errors.Is(cause, ErrUncertain) || !errors.Is(cause, ErrUnavailable) {
		test.Fatalf("uncertain persistence did not fail closed: %v", cause)
	}
	if snapshot, err := manager.Get(test.Context()); !errors.Is(err, ErrUncertain) || snapshot != (Snapshot{}) {
		test.Fatal("uncertain manager reported settings success")
	}
	if _, err := manager.Update(test.Context(), 2, ""); !errors.Is(err, ErrUncertain) {
		test.Fatal("uncertain manager accepted another update")
	}
	request, _ := http.NewRequestWithContext(test.Context(), http.MethodGet, "https://api.github.com/repos", nil)
	if response, err := manager.RoundTrip(request); response != nil || !errors.Is(err, ErrUncertain) {
		if response != nil {
			_ = response.Body.Close()
		}
		test.Fatal("uncertain manager continued GitHub traffic")
	}
}

type faultFiles struct {
	atomicFiles
	point  string
	cancel func()
}

var syntheticFailure = errors.New("synthetic-proxy-user:synthetic-proxy-password@private-proxy.invalid")

func (files faultFiles) Create(name string) (atomicFile, error) {
	if files.point == "create" {
		return nil, syntheticFailure
	}
	file, err := files.atomicFiles.Create(name)
	if err != nil {
		return nil, err
	}
	return faultFile{atomicFile: file, point: files.point, cancel: files.cancel}, nil
}

func (files faultFiles) Rename(previous, next string) error {
	if files.point == "rename" {
		return syntheticFailure
	}
	if err := files.atomicFiles.Rename(previous, next); err != nil {
		return err
	}
	if files.point == "renamed error" {
		return syntheticFailure
	}
	return nil
}

func (files faultFiles) Sync() error {
	if files.point == "directory sync" {
		return syntheticFailure
	}
	if err := files.atomicFiles.Sync(); err != nil {
		return err
	}
	if files.point == "cancel after" {
		files.cancel()
	}
	return nil
}

func (files faultFiles) Remove(name string) error {
	err := files.atomicFiles.Remove(name)
	if files.point == "cleanup" {
		return syntheticFailure
	}
	return err
}

type faultFile struct {
	atomicFile
	point  string
	cancel func()
}

func (file faultFile) Write(data []byte) (int, error) {
	switch file.point {
	case "write":
		return 0, syntheticFailure
	case "short write":
		return file.atomicFile.Write(data[:len(data)-1])
	default:
		return file.atomicFile.Write(data)
	}
}

func (file faultFile) Sync() error {
	if file.point == "file sync" {
		return syntheticFailure
	}
	if err := file.atomicFile.Sync(); err != nil {
		return err
	}
	if file.point == "cancel before" {
		file.cancel()
	}
	return nil
}

func (file faultFile) Close() error {
	err := file.atomicFile.Close()
	if file.point == "file close" {
		return syntheticFailure
	}
	return err
}

func TestResponseBodyErrorsAndLeaseRelease(test *testing.T) {
	for _, failure := range []error{io.EOF, syntheticFailure, context.Canceled, context.DeadlineExceeded} {
		released := 0
		body := &leasedBody{ReadCloser: failingBody{err: failure}, ctx: test.Context(), release: func() { released++ }}
		_, err := body.Read(make([]byte, 1))
		if err == nil {
			test.Fatal("body read discarded its error")
		}
		if !errors.Is(failure, io.EOF) && !errors.Is(err, ErrUnavailable) {
			test.Fatal("body read failure was not classified as unavailable")
		}
		if errors.Is(failure, context.Canceled) && !errors.Is(err, context.Canceled) || errors.Is(failure, context.DeadlineExceeded) && !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("body read failure lost its cancellation classification")
		}
		assertPrivate(test, err)
		assertPrivate(test, body)
		if err := body.Close(); !errors.Is(err, ErrUnavailable) {
			test.Fatal("body close failure was not classified as unavailable")
		}
		_ = body.Close()
		if released != 1 {
			test.Fatal("response lease was not released exactly once")
		}
	}
}

type failingBody struct{ err error }

func (body failingBody) Read([]byte) (int, error) { return 0, body.err }

func (failingBody) Close() error { return syntheticFailure }
