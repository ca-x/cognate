package clientkey

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/install"
	"github.com/ca-x/cognate/internal/state"
)

func keyPaths(home string) (string, string, string) {
	return filepath.Join(home, ".config", "skillhub", "client.key"), filepath.Join(home, ".config", "skillhub", "client-key-use.v1"), filepath.Join(home, ".local", "share", "skillhub")
}

type keyChild struct {
	command *exec.Cmd
	input   io.WriteCloser
	output  *bufio.Reader
	wait    func() error
}

func startKeyChild(test *testing.T, home, mode string, extra ...string) *keyChild {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 6*time.Second)
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestClientKeyProcess$")
	command.Env = append(os.Environ(), "COGNATE_KEY_TEST_HOME="+home, "COGNATE_KEY_TEST_MODE="+mode)
	command.Env = append(command.Env, extra...)
	input, err := command.StdinPipe()
	if err != nil {
		test.Fatal("child input")
	}
	output, err := command.StdoutPipe()
	if err != nil {
		test.Fatal("child output")
	}
	if err := command.Start(); err != nil {
		test.Fatal("child start")
	}
	wait := sync.OnceValue(command.Wait)
	child := &keyChild{command: command, input: input, output: bufio.NewReader(output), wait: wait}
	test.Cleanup(func() { _ = input.Close(); cancel(); _ = wait() })
	return child
}

func childReceipt(test *testing.T, child *keyChild, expected string) {
	test.Helper()
	line, err := child.output.ReadString('\n')
	if err != nil || line != expected+"\n" {
		test.Fatal("child event receipt mismatch")
	}
}

func TestClientKeyProcess(test *testing.T) {
	home := os.Getenv("COGNATE_KEY_TEST_HOME")
	if home == "" {
		return
	}
	_, _, directory := keyPaths(home)
	switch os.Getenv("COGNATE_KEY_TEST_MODE") {
	case "init", "init-reject":
		fmt.Fprintln(os.Stdout, "ready")
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			test.Fatal("child start barrier")
		}
		initializeErr := Initialize(test.Context(), home)
		if os.Getenv("COGNATE_KEY_TEST_MODE") == "init-reject" {
			if !errors.Is(initializeErr, ErrUnavailable) {
				test.Fatal("replaced root admitted")
			}
			return
		}
		if initializeErr != nil {
			test.Fatal("child explicit initialize failed")
		}
		if os.Getenv("COGNATE_KEY_FAULT") == "pause-locked" {
			_ = os.Unsetenv("COGNATE_KEY_FAULT")
		}
		key, err := LoadExisting(test.Context(), home)
		defer clear(key)
		if err != nil || len(key) != 32 {
			test.Fatal("child key load failed")
		}
	case "session":
		session, err := install.OpenSession(test.Context(), directory)
		if err != nil {
			test.Fatal("real install session failed")
		}
		fmt.Fprintln(os.Stdout, "session-locked")
		if _, err := io.ReadFull(os.Stdin, make([]byte, 1)); err != nil {
			_ = session.Close()
			test.Fatal("session release barrier")
		}
		if err := session.Close(); err != nil {
			test.Fatal("session close")
		}
	case "session-probe":
		ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
		defer cancel()
		session, err := install.OpenSession(ctx, directory)
		if session != nil {
			_ = session.Close()
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("initializer did not hold the physical install lock")
		}
		fmt.Fprintln(os.Stdout, "session-blocked")
	}
}

func TestClientKeyConcurrentInitialization(test *testing.T) {
	home := test.TempDir()
	first := startKeyChild(test, home, "init")
	second := startKeyChild(test, home, "init")
	childReceipt(test, first, "ready")
	childReceipt(test, second, "ready")
	if _, err := io.WriteString(first.input, "x"); err != nil {
		test.Fatal("first barrier")
	}
	if _, err := io.WriteString(second.input, "x"); err != nil {
		test.Fatal("second barrier")
	}
	if first.wait() != nil || second.wait() != nil {
		test.Fatal("concurrent initialization failed")
	}
	key, err := LoadExisting(test.Context(), home)
	defer clear(key)
	if err != nil || len(key) != 32 {
		test.Fatal("concurrent children did not establish a stable key")
	}
	if err := Initialize(test.Context(), home); err != nil {
		test.Fatal("stable initialization not reusable")
	}
	after, err := LoadExisting(test.Context(), home)
	defer clear(after)
	if err != nil || !bytes.Equal(key, after) {
		test.Fatal("concurrent initialization changed stable key")
	}
}

func TestClientKeyInstallationLock(test *testing.T) {
	if os.Getenv("COGNATE_KEY_FAULT_ACTIVE") == "1" {
		test.Run("initializer-holds-real-lock", func(test *testing.T) {
			home := test.TempDir()
			child := startKeyChild(test, home, "init", "COGNATE_KEY_FAULT=pause-locked")
			childReceipt(test, child, "ready")
			_, _ = io.WriteString(child.input, "x")
			childReceipt(test, child, "both-locked")
			probe := startKeyChild(test, home, "session-probe")
			childReceipt(test, probe, "session-blocked")
			if probe.wait() != nil {
				test.Fatal("real Session did not block")
			}
			_, _ = io.WriteString(child.input, "x")
			if child.wait() != nil {
				test.Fatal("initializer release did not drain")
			}
		})
	}
	home := test.TempDir()
	keyPath, _, directory := keyPaths(home)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		test.Fatal("session root")
	}
	child := startKeyChild(test, home, "session")
	childReceipt(test, child, "session-locked")
	ctx, cancel := context.WithTimeout(test.Context(), 100*time.Millisecond)
	defer cancel()
	if err := Initialize(ctx, home); !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("core did not wait on actual session lock")
	}
	if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("canceled initializer created key")
	}
	if _, err := io.WriteString(child.input, "x"); err != nil {
		test.Fatal("release session")
	}
	if err := child.wait(); err != nil {
		test.Fatal("session did not drain")
	}
	expectRefusal(test, home, ErrRecoveryRequired)
}

func TestClientKeyDurableFailures(test *testing.T) {
	home := test.TempDir()
	keyPath, _, _ := keyPaths(home)
	original := bytes.Repeat([]byte{0x76}, 32)
	defer clear(original)
	writeFixture(test, keyPath, original)
	key, err := LoadExisting(test.Context(), home)
	defer clear(key)
	if err != nil || !bytes.Equal(key, original) {
		test.Fatal("complete pre-marker key not reusable")
	}
	if os.Getenv("COGNATE_KEY_FAULT_ACTIVE") != "1" {
		return
	}
	for _, mode := range []string{"key-short", "marker-short", "key-write", "key-sync", "marker-sync", "key-parent", "marker-parent", "key-close", "marker-close", "root-close", "release", "late-cancel"} {
		test.Run(mode, func(test *testing.T) {
			home := test.TempDir()
			keyPath, marker, _ := keyPaths(home)
			partial := mode == "key-short" || mode == "marker-short" || mode == "key-write"
			if !partial {
				writeFixture(test, keyPath, original)
			}
			test.Setenv("COGNATE_KEY_FAULT", mode)
			if partial {
				if err := Initialize(test.Context(), home); !errors.Is(err, ErrUnavailable) {
					test.Fatal("partial write acknowledged")
				}
			} else {
				key, err := LoadExisting(test.Context(), home)
				defer clear(key)
				if key != nil || !errors.Is(err, ErrUnavailable) {
					test.Fatal("fault delivered tentative key")
				}
				if mode == "late-cancel" && !errors.Is(err, context.Canceled) {
					test.Fatal("late cancellation lost")
				}
			}
			before, _ := os.ReadFile(keyPath)
			defer clear(before)
			test.Setenv("COGNATE_KEY_FAULT", "")
			if partial {
				expectRefusal(test, home, ErrRecoveryRequired)
				if mode == "marker-short" {
					data, _ := os.ReadFile(marker)
					if len(data) == 0 || len(data) >= len(markerBody) || len(before) != 32 {
						test.Fatal("marker short-write evidence missing")
					}
				}
			} else {
				key, err := LoadExisting(test.Context(), home)
				defer clear(key)
				if err != nil || !bytes.Equal(before, key) || !bytes.Equal(original, key) {
					test.Fatal("fault retry replaced key or failed to reestablish durability")
				}
			}
			after, _ := os.ReadFile(keyPath)
			defer clear(after)
			if !bytes.Equal(before, after) {
				test.Fatal("retry repaired or regenerated key")
			}
		})
	}
	for _, phase := range []string{"crash-key", "crash-marker"} {
		test.Run(phase, func(test *testing.T) {
			home := test.TempDir()
			keyPath, marker, directory := keyPaths(home)
			child := startKeyChild(test, home, "init", "COGNATE_KEY_FAULT="+phase)
			childReceipt(test, child, "ready")
			_, _ = io.WriteString(child.input, "x")
			failure, ok := errors.AsType[*exec.ExitError](child.wait())
			if !ok || failure.ExitCode() != 72 {
				test.Fatal("durable crash boundary not reached")
			}
			before, _ := os.ReadFile(keyPath)
			defer clear(before)
			if len(before) != 32 {
				test.Fatal("crash did not preserve complete key")
			}
			if phase == "crash-key" {
				if _, err := os.Lstat(marker); !errors.Is(err, os.ErrNotExist) {
					test.Fatal("marker preceded durable key boundary")
				}
				if err := Initialize(test.Context(), home); err != nil {
					test.Fatal("key-first crash not retryable")
				}
				key, err := LoadExisting(test.Context(), home)
				defer clear(key)
				if err != nil || !bytes.Equal(key, before) {
					test.Fatal("crash restart changed key")
				}
			} else {
				if _, err := os.Stat(marker); err != nil {
					test.Fatal("durable marker missing")
				}
				_ = os.Remove(keyPath)
				_ = os.RemoveAll(directory)
				expectRefusal(test, home, ErrRecoveryRequired)
			}
		})
	}
	test.Run("replaced-root", func(test *testing.T) {
		home := test.TempDir()
		keyPath, _, _ := keyPaths(home)
		child := startKeyChild(test, home, "init-reject", "COGNATE_KEY_FAULT=pause-locked")
		childReceipt(test, child, "ready")
		_, _ = io.WriteString(child.input, "x")
		childReceipt(test, child, "both-locked")
		root := filepath.Dir(keyPath)
		if os.Rename(root, root+".old") != nil || os.Mkdir(root, 0o700) != nil {
			test.Fatal("replace fixture root")
		}
		_, _ = io.WriteString(child.input, "x")
		if child.wait() != nil {
			test.Fatal("replacement not refused")
		}
		if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("replacement root received key")
		}
	})
}

func expectRefusal(test *testing.T, home string, expected error) {
	test.Helper()
	key, err := LoadExisting(test.Context(), home)
	defer clear(key)
	initializeErr := Initialize(test.Context(), home)
	if key != nil || !errors.Is(err, expected) || !errors.Is(initializeErr, expected) {
		test.Fatal("key admission refusal classification changed")
	}
	if home != "" && strings.Contains(err.Error(), home) {
		test.Fatal("error exposed a filesystem path")
	}
}

func TestClientKeyMissingKeyResidue(test *testing.T) {
	for _, mode := range []string{"database", "wal", "native", "backup", "unknown-file", "unknown-directory", "marker-only", "removed-state"} {
		test.Run(mode, func(test *testing.T) {
			home := test.TempDir()
			keyPath, marker, directory := keyPaths(home)
			if err := os.MkdirAll(directory, 0o700); err != nil {
				test.Fatal("fixture root")
			}
			switch mode {
			case "database":
				store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
				if err != nil {
					test.Fatal("real SQLite fixture")
				}
				if err := store.Close(); err != nil {
					test.Fatal("SQLite close")
				}
			case "wal":
				writeFixture(test, filepath.Join(directory, "client.db-wal"), []byte("synthetic-wal"))
			case "native", "backup", "unknown-directory":
				if err := os.Mkdir(filepath.Join(directory, mode), 0o700); err != nil {
					test.Fatal("residue directory")
				}
			case "unknown-file":
				writeFixture(test, filepath.Join(directory, "unknown"), []byte("preserve"))
			case "marker-only":
				writeFixture(test, marker, []byte(markerBody))
			case "removed-state":
				if err := Initialize(test.Context(), home); err != nil {
					test.Fatal("initial marker")
				}
				if err := os.Remove(keyPath); err != nil {
					test.Fatal("remove fixture key")
				}
				if err := os.RemoveAll(directory); err != nil {
					test.Fatal("remove fixture state")
				}
			}
			expectRefusal(test, home, ErrRecoveryRequired)
			if _, err := os.Lstat(keyPath); !errors.Is(err, os.ErrNotExist) {
				test.Fatal("residue caused a replacement key")
			}
		})
	}
}

func TestClientKeyTrustAndBounds(test *testing.T) {
	for _, home := range []string{"", "relative", "/", "/tmp/../tmp", "/tmp/"} {
		expectRefusal(test, home, ErrUnavailable)
	}
	for _, name := range []string{"client.key", "client-key-use.v1", "client-key.init.lock", "client.db.install.lock", "client.db"} {
		for _, mode := range []string{"symlink", "hardlink", "fifo", "public"} {
			test.Run(name+"/"+mode, func(test *testing.T) {
				home := test.TempDir()
				keyPath, _, directory := keyPaths(home)
				path := filepath.Join(filepath.Dir(keyPath), name)
				if strings.HasPrefix(name, "client.db") {
					path = filepath.Join(directory, name)
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					test.Fatal("fixture root")
				}
				target := filepath.Join(test.TempDir(), "target")
				writeFixture(test, target, bytes.Repeat([]byte{0x61}, 32))
				switch mode {
				case "symlink":
					if err := os.Symlink(target, path); err != nil {
						test.Fatal("symlink fixture")
					}
				case "hardlink":
					if err := os.Link(target, path); err != nil {
						test.Fatal("hardlink fixture")
					}
				case "fifo":
					if err := syscall.Mkfifo(path, 0o600); err != nil {
						test.Fatal("FIFO fixture")
					}
				case "public":
					writeFixture(test, path, []byte("unsafe"))
					if err := os.Chmod(path, 0o666); err != nil {
						test.Fatal("mode fixture")
					}
				}
				expectRefusal(test, home, ErrUnavailable)
				data, _ := os.ReadFile(target)
				if !bytes.Equal(data, bytes.Repeat([]byte{0x61}, 32)) {
					test.Fatal("unsafe target was mutated")
				}
			})
		}
	}
	for _, component := range []string{".config", ".local", ".local/share/skillhub"} {
		test.Run("root/"+component, func(test *testing.T) {
			home := test.TempDir()
			path := filepath.Join(home, component)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				test.Fatal("root fixture")
			}
			if err := os.Symlink(test.TempDir(), path); err != nil {
				test.Fatal("root link")
			}
			expectRefusal(test, home, ErrUnavailable)
		})
	}
	for _, data := range [][]byte{nil, []byte("short"), bytes.Repeat([]byte{0x62}, 33)} {
		home := test.TempDir()
		keyPath, _, _ := keyPaths(home)
		writeFixture(test, keyPath, data)
		expectRefusal(test, home, ErrRecoveryRequired)
		after, _ := os.ReadFile(keyPath)
		if !bytes.Equal(after, data) {
			test.Fatal("invalid key repaired")
		}
	}
	for _, marker := range []string{"", "{", `{"version":1,"root":"default-client-state","extra":0}`, `{"version":1,"version":1,"root":"default-client-state"}`, `{"Version":1,"root":"default-client-state"}`, `{"version":2,"root":"default-client-state"}`, `{"version":1,"root":"other"}`, markerBody + `{}`, strings.Repeat("x", 257)} {
		home := test.TempDir()
		keyPath, path, _ := keyPaths(home)
		writeFixture(test, keyPath, bytes.Repeat([]byte{0x63}, 32))
		writeFixture(test, path, []byte(marker))
		expectRefusal(test, home, ErrRecoveryRequired)
		after, _ := os.ReadFile(path)
		if string(after) != marker {
			test.Fatal("corrupt marker repaired")
		}
	}
	ctx, cancel := context.WithCancel(test.Context())
	cancel()
	key, err := LoadExisting(ctx, test.TempDir())
	if key != nil || !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation not preserved")
	}
}

func writeFixture(test *testing.T, path string, data []byte) {
	test.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		test.Fatal("fixture directories failed")
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		test.Fatal("fixture write failed")
	}
}

func TestClientKeyLegacyEnrollment(test *testing.T) {
	for _, registered := range []bool{false, true} {
		test.Run(map[bool]string{false: "legacy", true: "registered"}[registered], func(test *testing.T) {
			home := test.TempDir()
			keyPath, marker, directory := keyPaths(home)
			original := bytes.Repeat([]byte{0x93}, 32)
			defer clear(original)
			writeFixture(test, keyPath, original)
			if registered {
				writeFixture(test, marker, []byte(markerBody))
			}
			if err := os.MkdirAll(directory, 0o700); err != nil {
				test.Fatal("state root")
			}
			store, err := state.Open(test.Context(), filepath.Join(directory, "client.db"))
			if err != nil {
				test.Fatal("real SQLite setup")
			}
			if err := store.Close(); err != nil {
				test.Fatal("SQLite close")
			}
			database, _ := os.ReadFile(filepath.Join(directory, "client.db"))
			writeFixture(test, filepath.Join(directory, "native", "opaque"), []byte("synthetic-ciphertext"))
			serverKey := filepath.Join(filepath.Dir(keyPath), "master.key")
			writeFixture(test, serverKey, []byte("unrelated-synthetic-server-key"))
			key, err := LoadExisting(test.Context(), home)
			defer clear(key)
			if err != nil || !bytes.Equal(key, original) {
				test.Fatal("valid legacy key was not enrolled unchanged")
			}
			after, _ := os.ReadFile(filepath.Join(directory, "client.db"))
			if !bytes.Equal(database, after) {
				test.Fatal("core touched SQLite")
			}
			data, _ := os.ReadFile(serverKey)
			if string(data) != "unrelated-synthetic-server-key" {
				test.Fatal("unrelated sibling changed")
			}
		})
	}
}

func TestClientKeyInitializeAndLoad(test *testing.T) {
	home := test.TempDir()
	keyPath, marker, directory := keyPaths(home)
	key, err := LoadExisting(test.Context(), home)
	if key != nil || !errors.Is(err, ErrInitializationRequired) {
		test.Fatal("pristine load did not require explicit initialization")
	}
	for _, name := range []string{keyPath, marker, filepath.Join(directory, "client.db")} {
		if _, err := os.Lstat(name); !errors.Is(err, os.ErrNotExist) {
			test.Fatal("load created key, marker or database")
		}
	}
	if err := Initialize(test.Context(), home); err != nil {
		test.Fatal("explicit initialization failed")
	}
	first, err := LoadExisting(test.Context(), home)
	if err != nil || len(first) != 32 {
		test.Fatal("initialized key not admitted")
	}
	defer clear(first)
	before, _ := os.Stat(keyPath)
	if err := Initialize(test.Context(), home); err != nil {
		test.Fatal("repeat initialization failed")
	}
	second, err := LoadExisting(test.Context(), home)
	if err != nil || !bytes.Equal(first, second) {
		test.Fatal("repeat changed key")
	}
	clear(second)
	third, err := LoadExisting(test.Context(), home)
	defer clear(third)
	after, _ := os.Stat(keyPath)
	if err != nil || !bytes.Equal(first, third) || !os.SameFile(before, after) {
		test.Fatal("key identity or ownership changed")
	}
	entries, err := os.ReadDir(directory)
	if err != nil || len(entries) != 1 || entries[0].Name() != "client.db.install.lock" {
		test.Fatal("core created domain state")
	}
	data, err := os.ReadFile(marker)
	if err != nil || string(data) != "{\"version\":1,\"root\":\"default-client-state\"}\n" {
		test.Fatal("invalid fixed marker")
	}
}
