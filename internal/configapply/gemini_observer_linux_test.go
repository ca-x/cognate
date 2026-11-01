package configapply

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestGeminiNoexecFreshObservation(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	deadline := time.Now().Add(time.Minute)
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, deadline)
	if err != nil {
		test.Fatalf("fixed complete source noexec observation: %v", err)
	}
	defer observation.Destroy()
	private := observation.get[geminiObserved]()
	if private == nil || private.engine != fixture.engine.private() || private.descriptor.PackageDigest == [32]byte{} || len(private.descriptor.Sources) == 0 || private.expires.After(deadline) {
		test.Fatal("observation missing owned context or extended deadline")
	}
	if _, err := os.Lstat(filepath.Join(fixture.home, ".gemini", "settings.json")); !os.IsNotExist(err) {
		test.Fatal("observer executed migrating consumer or wrote target")
	}
}

func TestGeminiPhysicalUnsafeInputsNeverWrite(test *testing.T) {
	for _, kind := range []string{"env-symlink", "env-hardlink", "settings-symlink", "settings-acl", "fifo", "writable-root", "symlink-root", "oversize-env", "pair-budget", "fallback-env", "unknown-selector"} {
		test.Run(kind, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, nil)
			outside := filepath.Join(test.TempDir(), "outside")
			original := []byte("synthetic-outside-object\n")
			if os.WriteFile(outside, original, 0o600) != nil {
				test.Fatal("private outside fixture")
			}
			target := filepath.Join(fixture.home, ".gemini")
			var err error
			switch kind {
			case "env-symlink":
				err = os.Symlink(outside, filepath.Join(target, ".env"))
			case "env-hardlink":
				err = os.Link(outside, filepath.Join(target, ".env"))
			case "settings-symlink":
				err = os.Symlink(outside, filepath.Join(target, "settings.json"))
			case "fifo":
				err = unix.Mkfifo(filepath.Join(target, ".env"), 0o600)
			case "writable-root":
				err = os.Chmod(target, 0o777)
			case "symlink-root":
				err = os.Rename(target, target+"-original")
				if err == nil {
					err = os.Symlink(target+"-original", target)
				}
			case "oversize-env":
				err = os.WriteFile(filepath.Join(target, ".env"), bytes.Repeat([]byte("x"), (256<<10)+1), 0o600)
			case "pair-budget":
				err = os.WriteFile(filepath.Join(target, ".env"), bytes.Repeat([]byte("#"), 200<<10), 0o600)
				if err == nil {
					err = os.WriteFile(filepath.Join(target, "settings.json"), bytes.Repeat([]byte(" "), 200<<10), 0o600)
				}
			case "fallback-env":
				err = os.WriteFile(filepath.Join(fixture.home, ".env"), []byte("FOREIGN=keep\n"), 0o600)
			case "unknown-selector":
				err = os.WriteFile(filepath.Join(target, "settings.json"), []byte(`{"security":{"auth":{"useExternal":true}}}`), 0o600)
			case "settings-acl":
				path := filepath.Join(target, "settings.json")
				err = os.WriteFile(path, []byte("{}"), 0o600)
				if err == nil {
					acl := binary.LittleEndian.AppendUint32(nil, 2)
					for _, entry := range []struct {
						tag, permissions uint16
						owner            uint32
					}{{1, 6, ^uint32(0)}, {2, 4, uint32(os.Geteuid())}, {4, 0, ^uint32(0)}, {16, 4, ^uint32(0)}, {32, 0, ^uint32(0)}} {
						acl = binary.LittleEndian.AppendUint16(acl, entry.tag)
						acl = binary.LittleEndian.AppendUint16(acl, entry.permissions)
						acl = binary.LittleEndian.AppendUint32(acl, entry.owner)
					}
					err = unix.Setxattr(path, "system.posix_acl_access", acl, 0)
				}
			}
			if err != nil {
				test.Fatal("private physical unsafe fixture setup")
			}
			observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
			observation.Destroy()
			if err == nil {
				test.Fatal("unsafe physical source admitted")
			}
			body, err := os.ReadFile(outside)
			if err != nil || !bytes.Equal(body, original) {
				test.Fatal("unsafe source changed outside object")
			}
		})
	}
}

func TestGeminiObservationCanceledAndOriginalDeadline(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	observation, err := fixture.engine.ObserveGemini(ctx, fixture.launch, time.Now().Add(time.Minute))
	observation.Destroy()
	if !errors.Is(err, context.Canceled) {
		test.Fatal("canceled observer admitted")
	}
	observation, err = fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(-time.Second))
	observation.Destroy()
	if !errors.Is(err, ErrGeminiExpired) {
		test.Fatal("observer renewed original expired deadline")
	}
}

func TestGeminiRuntimeInjectionEnvironmentRejected(test *testing.T) {
	for _, name := range []string{"LD_PRELOAD", "LD_LIBRARY_PATH", "LD_AUDIT", "OPENSSL_CONF", "OPENSSL_MODULES", "SSLKEYLOGFILE"} {
		test.Run(name, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, nil)
			test.Setenv(name, "/synthetic-untrusted-runtime-input")
			observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
			observation.Destroy()
			if !errors.Is(err, ErrGeminiUnsupported) {
				test.Fatal("runtime injection environment admitted by fixed profile")
			}
		})
	}
}

func TestGeminiTLSOverrideEnvironmentWitness(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	if err != nil {
		test.Fatal("original observed launch")
	}
	defer observation.Destroy()
	test.Setenv("SSL_CERT_FILE", "/synthetic-new-launch-trust-input")
	observed := observation.get[geminiObserved]()
	err = fixture.engine.private().withLock(test.Context(), func(ctx context.Context) error {
		return fixture.engine.private().validateObservation(ctx, observed, observed.baseline, nil)
	})
	if !errors.Is(err, ErrGeminiConflict) {
		test.Fatal("changed TLS launch override reused original observation witness")
	}
}
func TestGeminiObservationRejectsUnsafeInputs(test *testing.T) {
	for _, kind := range []string{"untrusted", "inherited-key", "inherited-proxy", "higher-env", "workspace-auth", "migration", "interpolation", "insecure-env", "wrong-node"} {
		test.Run(kind, func(test *testing.T) {
			fixture := newGeminiFixture(test, nil, nil)
			switch kind {
			case "untrusted":
				os.WriteFile(filepath.Join(fixture.home, ".gemini", "trustedFolders.json"), []byte(`{}`), 0o600)
			case "inherited-key":
				test.Setenv("GEMINI_API_KEY", "synthetic-override")
			case "inherited-proxy":
				test.Setenv("HTTPS_PROXY", "https://synthetic.invalid")
			case "higher-env":
				os.WriteFile(filepath.Join(fixture.launch.Directory, ".env"), []byte("FOREIGN=keep\n"), 0o600)
			case "workspace-auth":
				os.Mkdir(filepath.Join(fixture.launch.Directory, ".gemini"), 0o700)
				os.WriteFile(filepath.Join(fixture.launch.Directory, ".gemini", "settings.json"), []byte(`{"security":{"auth":{"selectedType":"oauth-personal"}}}`), 0o600)
			case "migration":
				os.WriteFile(filepath.Join(fixture.home, ".gemini", "settings.json"), []byte(`{"ui":{"accessibility":{"disableLoadingPhrases":true}}}`), 0o600)
			case "interpolation":
				os.WriteFile(filepath.Join(fixture.home, ".gemini", "settings.json"), []byte(`{"ui":{"theme":"${GEMINI_API_KEY}"}}`), 0o600)
			case "insecure-env":
				path := filepath.Join(fixture.home, ".gemini", ".env")
				os.WriteFile(path, []byte("FOREIGN=keep\n"), 0o644)
				os.Chmod(path, 0o644)
			case "wrong-node":
				fixture.launch.NodeExecutable = "/usr/bin/false"
			}
			result, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
			result.Destroy()
			if err == nil || !errors.Is(err, ErrGeminiUnsupported) && !errors.Is(err, ErrGeminiConflict) {
				test.Fatal("unsafe effective input admitted")
			}
		})
	}
}
func TestGeminiObservationBetweenChecksCAS(test *testing.T) {
	fixture := newGeminiFixture(test, nil, nil)
	changed := false
	fixture.engine.private().fault = func(label string) error {
		if label == "observation-between" && !changed {
			changed = true
			return os.WriteFile(filepath.Join(fixture.launch.Directory, ".env"), []byte("FOREIGN=late\n"), 0o600)
		}
		return nil
	}
	observation, err := fixture.engine.ObserveGemini(test.Context(), fixture.launch, time.Now().Add(time.Minute))
	observation.Destroy()
	if !changed || !errors.Is(err, ErrGeminiConflict) {
		test.Fatal("changed input source did not invalidate full observation")
	}
}
