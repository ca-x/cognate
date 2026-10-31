package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/maintenance"
	"github.com/spf13/afero"
)

func controlRoot(test *testing.T) (*os.Root, string) {
	test.Helper()
	if err := maintenance.CheckPlatform(test.Context()); err != nil {
		test.Skip("private control filesystem unsupported")
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = root.Close() })
	return root, directory
}

func controlTestRecord() controlJob {
	now := time.Now().UTC()
	return controlJob{Job: Job{ID: uuid.New().String(), Phase: "pending", CreatedAt: now, UpdatedAt: now}, Principal: uuid.New().String(), Request: uuid.New().String(), Generation: strings.Repeat("01", 32), Options: strings.Repeat("02", 32)}
}

func controlRunProcess(test *testing.T, directory, mode string) {
	test.Helper()
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestControlStoreProcessHelper$")
	command.Env = append(os.Environ(), "COGNATE_CONTROL_TEST_ROOT="+directory, "COGNATE_CONTROL_TEST_MODE="+mode)
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatal("control process boundary failed", mode, err, string(output))
	}
}

func TestControlStoreFinalGuardCloseFailureFencesNewProcess(test *testing.T) {
	root, directory := controlRoot(test)
	key := bytes.Repeat([]byte{45}, 32)
	store, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal(err)
	}
	physical := store.get()
	original := physical.ops.close
	calls := 0
	physical.ops.close = func(file afero.File) error {
		err := original(file)
		if file == physical.lockGuard {
			calls++
			return errors.New("private-last-guard-EIO-after-real-close")
		}
		return err
	}
	if err := store.Close(); !errors.Is(err, ErrControlUncertain) || calls != 1 || strings.Contains(err.Error(), "private") {
		test.Fatal("final actual close plus EIO did not quarantine release", err, calls)
	}
	if _, err := physical.root.Stat("."); err != nil {
		test.Fatal("unknown release discarded root", err)
	}
	if _, err := root.Lstat("control.fence"); err != nil {
		test.Fatal("unknown release lacks durable fence", err)
	}
	bareLock, err := controlLock(root)
	if err != nil {
		test.Fatal("fixture did not actually release the final physical flock", err)
	}
	if err := bareLock.Close(); err != nil {
		test.Fatal(err)
	}
	if reopened, err := OpenControlStore(test.Context(), root, key); reopened != nil || !errors.Is(err, ErrBusy) {
		test.Fatal("same-process root alias bypassed quarantine", err)
	}
	controlRunProcess(test, directory, "expect-busy")
	if err := store.Close(); !errors.Is(err, ErrControlUncertain) || calls != 1 {
		test.Fatal("unknown last descriptor was retried", err, calls)
	}
}

func TestControlStoreNormalReleaseAllowsNewProcess(test *testing.T) {
	root, directory := controlRoot(test)
	key := bytes.Repeat([]byte{45}, 32)
	store, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal(err)
	}
	identity := store.InstanceID()
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if _, err := root.Lstat("control.fence"); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("normal release stranded its fence", err)
	}
	controlRunProcess(test, directory, "expect-open")
	reopened, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal("normal release could not reopen", err)
	}
	defer reopened.Close()
	if reopened.InstanceID() != identity {
		test.Fatal("release replaced durable instance")
	}
}

func TestControlStoreReleaseCrashAndPartialMarkerFailClosed(test *testing.T) {
	for _, mode := range []string{"release-before-guard", "release-after-guard", "release-partial-marker"} {
		test.Run(mode, func(test *testing.T) {
			root, directory := controlRoot(test)
			controlRunProcess(test, directory, mode)
			if store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{45}, 32)); store != nil || !errors.Is(err, ErrBusy) {
				test.Fatal("crash/partial release intent was automatically cleared", err)
			}
			controlRunProcess(test, directory, "expect-busy")
		})
	}
}

func TestControlStoreReleaseMarkerPreparationFaults(test *testing.T) {
	for _, mode := range []string{"file-sync", "file-close", "directory-sync"} {
		test.Run(mode, func(test *testing.T) {
			root, directory := controlRoot(test)
			store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{45}, 32))
			if err != nil {
				test.Fatal(err)
			}
			physical := store.get()
			original := physical.ops
			markerSynced := false
			physical.ops.sync = func(file afero.File) error {
				if file != physical.directory {
					markerSynced = true
					if mode == "file-sync" {
						return errors.New("private-marker-sync")
					}
				}
				if mode == "directory-sync" && file == physical.directory && markerSynced {
					return errors.New("private-marker-directory-sync")
				}
				return original.sync(file)
			}
			physical.ops.close = func(file afero.File) error {
				err := original.close(file)
				if mode == "file-close" && file != physical.directory && file != physical.lock && file != physical.lockGuard {
					return errors.New("private-marker-close-after-real-close")
				}
				return err
			}
			if err := store.Close(); !errors.Is(err, ErrControlUncertain) || strings.Contains(err.Error(), "private") {
				test.Fatal("release-marker preparation uncertainty was lost", err)
			}
			if _, err := physical.root.Stat("."); err != nil {
				test.Fatal("preparation failure discarded root", err)
			}
			if _, err := physical.lockGuard.Stat(); err != nil {
				test.Fatal("final guard released before marker durability", err)
			}
			bareLock, err := controlLock(root)
			if bareLock != nil {
				_ = bareLock.Close()
			}
			if !errors.Is(err, ErrBusy) {
				test.Fatal("preparation failure lost the physical lock", err)
			}
			controlRunProcess(test, directory, "expect-busy")
		})
	}
}

func TestControlStoreOrphanPartialReleaseDoesNotInitialize(test *testing.T) {
	root, directory := controlRoot(test)
	file, err := root.OpenFile("control.fence", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := file.Write([]byte("CG")); err != nil {
		test.Fatal(err)
	}
	if err := file.Close(); err != nil {
		test.Fatal(err)
	}
	controlRunProcess(test, directory, "expect-busy")
	if _, err := root.Lstat(controlLogName); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("orphan partial release intent initialized a fresh instance", err)
	}
}

func TestControlStorePersistFailuresRetainLock(test *testing.T) {
	for _, mode := range []string{"file-sync", "file-close", "rename", "directory-sync"} {
		test.Run(mode, func(test *testing.T) {
			root, _ := controlRoot(test)
			key := bytes.Repeat([]byte{31}, 32)
			store, err := OpenControlStore(test.Context(), root, key)
			if err != nil {
				test.Fatal(err)
			}
			physical := store.get()
			original := physical.ops
			defer func() { physical.ops = original }()
			physical.ops.sync = func(file afero.File) error {
				if mode == "file-sync" && file != physical.directory || mode == "directory-sync" && file == physical.directory {
					return fmt.Errorf("private-persist-secret")
				}
				return original.sync(file)
			}
			physical.ops.close = func(file afero.File) error {
				err := original.close(file)
				if mode == "file-close" {
					return fmt.Errorf("private-close-secret")
				}
				return err
			}
			physical.ops.rename = func(from, to string) error {
				if mode == "rename" {
					return fmt.Errorf("private-rename-secret")
				}
				return original.rename(from, to)
			}
			candidate := physical.clone()
			candidate.Jobs = append(candidate.Jobs, controlTestRecord())
			if err := physical.persist(candidate); !errors.Is(err, ErrControlUncertain) || strings.Contains(err.Error(), "secret") {
				test.Fatal("persist failure was downgraded or leaked", err)
			}
			if len(physical.data.Jobs) != 0 || !physical.uncertain {
				test.Fatal("uncommitted candidate replaced in-memory state")
			}
			physical.ops = original
			if err := store.Close(); !errors.Is(err, ErrControlUncertain) {
				test.Fatal("uncertain Close pretended cleanup succeeded", err)
			}
			if second, err := OpenControlStore(test.Context(), root, key); second != nil || !errors.Is(err, ErrBusy) {
				test.Fatal("uncertain persistence released the process lock", err)
			}
		})
	}
}

func TestControlStoreCloseUncertaintyPinsRootAndLock(test *testing.T) {
	for _, mode := range []string{"directory", "lock"} {
		test.Run(mode, func(test *testing.T) {
			root, _ := controlRoot(test)
			key := bytes.Repeat([]byte{47}, 32)
			store, err := OpenControlStore(test.Context(), root, key)
			if err != nil {
				test.Fatal(err)
			}
			physical := store.get()
			original := physical.ops.close
			calls := 0
			physical.ops.close = func(file afero.File) error {
				err := original(file)
				if mode == "directory" && file == physical.directory || mode == "lock" && file == physical.lock {
					calls++
					return errors.New("private-close-uncertainty")
				}
				return err
			}
			if err := store.Close(); !errors.Is(err, ErrControlUncertain) {
				test.Fatal("close uncertainty lost", err)
			}
			if _, err := physical.root.Stat("."); err != nil {
				test.Fatal("uncertain close released root capability", err)
			}
			if second, err := OpenControlStore(test.Context(), root, key); second != nil || !errors.Is(err, ErrBusy) {
				test.Fatal("uncertain close released process fence", err)
			}
			if err := store.Close(); !errors.Is(err, ErrControlUncertain) || calls != 1 {
				test.Fatal("uncertain descriptor was retried", err)
			}
		})
	}
}

func TestControlStoreRejectsHostilePhysicalFiles(test *testing.T) {
	for _, mode := range []string{"directory-mode", "unknown-file", "symlink", "hardlink", "log-mode", "oversized", "unknown-format"} {
		test.Run(mode, func(test *testing.T) {
			root, directory := controlRoot(test)
			key := bytes.Repeat([]byte{61}, 32)
			store, err := OpenControlStore(test.Context(), root, key)
			if err != nil {
				test.Fatal(err)
			}
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
			logPath := filepath.Join(directory, controlLogName)
			switch mode {
			case "directory-mode":
				err = os.Chmod(directory, 0755)
			case "unknown-file":
				err = os.WriteFile(filepath.Join(directory, "unexpected"), []byte("private"), 0600)
			case "log-mode":
				err = os.Chmod(logPath, 0644)
			case "hardlink":
				err = os.Link(logPath, filepath.Join(directory, "alias"))
			case "symlink":
				err = os.Rename(logPath, filepath.Join(directory, "real"))
				if err == nil {
					err = os.Symlink("real", logPath)
				}
			case "oversized":
				err = os.Truncate(logPath, controlLimit+1)
			case "unknown-format":
				err = os.WriteFile(logPath, bytes.Repeat([]byte{42}, 80), 0600)
			}
			if err != nil {
				test.Fatal(err)
			}
			if bad, err := OpenControlStore(test.Context(), root, key); bad != nil || err == nil {
				test.Fatal("hostile control root/log accepted")
			}
		})
	}
}

func TestControlStoreStrictAuthenticatedState(test *testing.T) {
	root, _ := controlRoot(test)
	store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{93}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	physical := store.get()
	plain, err := json.Marshal(physical.data)
	if err != nil {
		test.Fatal(err)
	}
	for _, payload := range [][]byte{append([]byte(`{"Unknown":1,`), plain[1:]...), append([]byte(`{"Version":1,`), plain[1:]...), bytes.Replace(plain, []byte(`"Version":1`), []byte(`"Version":2`), 1)} {
		encoded, err := physical.encode(physical.data)
		if err != nil {
			test.Fatal(err)
		}
		aead, err := physical.aead(encoded[8:24])
		if err != nil {
			test.Fatal(err)
		}
		encoded = aead.Seal(encoded[:36], encoded[24:36], payload, encoded[:24])
		if err := physical.decode(encoded); err == nil {
			test.Fatal("authenticated unknown/duplicate/version state accepted")
		}
	}
	candidate := physical.clone()
	candidate.Jobs = []controlJob{controlTestRecord()}
	candidate.Jobs[0].Job.Phase = "remote_committed"
	encoded, _ := physical.encode(candidate)
	if err := physical.decode(encoded); err == nil {
		test.Fatal("illegal committed state without catalog accepted")
	}
}

func TestControlStoreCrashRestart(test *testing.T) {
	for _, mode := range []string{"before-rename", "after-rename", "complete-intent"} {
		test.Run(mode, func(test *testing.T) {
			root, directory := controlRoot(test)
			command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestControlStoreProcessHelper$")
			command.Env = append(os.Environ(), "COGNATE_CONTROL_TEST_ROOT="+directory, "COGNATE_CONTROL_TEST_MODE="+mode)
			if output, err := command.CombinedOutput(); err != nil {
				test.Fatal("crash writer failed", err, string(output))
			}
			store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{45}, 32))
			if err != nil {
				test.Fatal("valid prior journal could not recover after crash", err)
			}
			defer store.Close()
			jobs := store.get().data.Jobs
			if mode == "before-rename" {
				if len(jobs) != 0 {
					test.Fatal("unpublished candidate survived crash")
				}
				return
			}
			if len(jobs) != 1 || mode == "complete-intent" && !jobs[0].CompleteAttempted {
				test.Fatal("committed journal intent was lost at crash boundary")
			}
		})
	}
}

func TestControlStoreProcessHelper(test *testing.T) {
	directory := os.Getenv("COGNATE_CONTROL_TEST_ROOT")
	if directory == "" {
		return
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	store, err := OpenControlStore(context.Background(), root, bytes.Repeat([]byte{45}, 32))
	if os.Getenv("COGNATE_CONTROL_TEST_MODE") == "expect-busy" {
		if store != nil || !errors.Is(err, ErrBusy) {
			test.Fatal("interprocess lock did not exclude child", err)
		}
		os.Exit(0)
	}
	if err != nil {
		test.Fatal(err)
	}
	physical := store.get()
	mode := os.Getenv("COGNATE_CONTROL_TEST_MODE")
	if mode == "expect-open" {
		if err := store.Close(); err != nil {
			test.Fatal(err)
		}
		os.Exit(0)
	}
	if mode == "release-partial-marker" {
		file, err := root.OpenFile("control.fence", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			test.Fatal(err)
		}
		if _, err := file.Write([]byte("CG")); err != nil {
			test.Fatal(err)
		}
		os.Exit(0)
	}
	if mode == "release-before-guard" || mode == "release-after-guard" {
		original := physical.ops.close
		physical.ops.close = func(file afero.File) error {
			if file == physical.lockGuard {
				if mode == "release-after-guard" {
					if err := original(file); err != nil {
						test.Fatal(err)
					}
				}
				os.Exit(0)
			}
			return original(file)
		}
		_ = store.Close()
		test.Fatal("release did not reach guarded final close")
	}
	candidate := physical.clone()
	record := controlTestRecord()
	if mode == "complete-intent" {
		record.Job.Phase, record.CompleteAttempted = "committing", true
		for _, item := range []struct {
			kind    ObjectKind
			ordinal uint32
		}{{Bootstrap, 0}, {DataExtent, 0}, {ManifestPage, 1}, {ManifestPage, 0}, {Complete, 0}} {
			record.Catalog = append(record.Catalog, controlObject{Kind: item.kind, Ordinal: item.ordinal, Size: 100, SHA256: strings.Repeat("03", 32), Attempt: strings.Repeat("04", 16)})
		}
		record.Job.Objects, record.Job.CiphertextBytes = 5, 500
	}
	candidate.Jobs = []controlJob{record}
	original := physical.ops
	if mode == "before-rename" {
		physical.ops.rename = func(string, string) error { return ErrUnavailable }
	}
	if mode == "after-rename" {
		physical.ops.sync = func(file afero.File) error {
			if file == physical.directory {
				return ErrUnavailable
			}
			return original.sync(file)
		}
	}
	err = physical.persist(candidate)
	if mode == "complete-intent" && err != nil || mode != "complete-intent" && !errors.Is(err, ErrControlUncertain) {
		test.Fatal(err)
	}
	os.Exit(0)
}

func TestControlStoreInterprocessLock(test *testing.T) {
	root, directory := controlRoot(test)
	store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{45}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	command := exec.CommandContext(test.Context(), os.Args[0], "-test.run=^TestControlStoreProcessHelper$")
	command.Env = append(os.Environ(), "COGNATE_CONTROL_TEST_ROOT="+directory, "COGNATE_CONTROL_TEST_MODE=expect-busy")
	if output, err := command.CombinedOutput(); err != nil {
		test.Fatal("process lock probe failed", err, string(output))
	}
}

func TestControlStoreSixteenMiBBudgetBeforePublication(test *testing.T) {
	root, _ := controlRoot(test)
	store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{57}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer store.Close()
	physical := store.get()
	record := controlTestRecord()
	record.Job.Phase = "uploading"
	object := controlObject{Size: 100, SHA256: strings.Repeat("03", 32), Attempt: strings.Repeat("04", 16)}
	object.Kind = Bootstrap
	record.Catalog = append(record.Catalog, object)
	object.Kind = DataExtent
	for ordinal := range uint32(99998) {
		object.Ordinal = ordinal
		record.Catalog = append(record.Catalog, object)
	}
	object.Kind = ManifestPage
	object.Ordinal = 1
	record.Catalog = append(record.Catalog, object)
	object.Ordinal = 0
	record.Catalog = append(record.Catalog, object)
	object.Kind = Complete
	record.Catalog = append(record.Catalog, object)
	record.Job.Objects = uint32(len(record.Catalog))
	record.Job.CiphertextBytes = int64(len(record.Catalog)) * 100
	candidate := physical.clone()
	duplicate := record
	duplicate.Job.ID, duplicate.Principal, duplicate.Request = uuid.New().String(), uuid.New().String(), uuid.New().String()
	candidate.Jobs = []controlJob{record, duplicate}
	plain, err := json.Marshal(candidate)
	if err != nil || len(plain)+52 <= controlLimit {
		test.Fatal("byte-budget fixture was not actually oversized", err)
	}
	clear(plain)
	if err := physical.persist(candidate); !errors.Is(err, ErrControlBudget) || physical.uncertain || len(physical.data.Jobs) != 0 {
		test.Fatal("oversized encrypted journal did not reject cleanly", err)
	}
	if _, err := root.Lstat(controlNextName); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("byte budget was checked after physical side effect", err)
	}
}

func TestControlStoreCandidateAndBudgets(test *testing.T) {
	root, directory := controlRoot(test)
	key := bytes.Repeat([]byte{53}, 32)
	store, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal(err)
	}
	physical := store.get()
	candidate := physical.clone()
	for range 1000 {
		candidate.Jobs = append(candidate.Jobs, controlTestRecord())
	}
	if err := physical.persist(candidate); err != nil {
		test.Fatal("1000-job journal refused", err)
	}
	candidate.Jobs = append(candidate.Jobs, controlTestRecord())
	if err := physical.persist(candidate); !errors.Is(err, ErrControlBudget) || physical.uncertain {
		test.Fatal("1001st job did not reject cleanly", err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, controlNextName), []byte("partial ciphertext"), 0600); err != nil {
		test.Fatal(err)
	}
	reopened, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal("partial unpublished candidate blocked durable journal", err)
	}
	defer reopened.Close()
	if len(reopened.get().data.Jobs) != 1000 {
		test.Fatal("candidate cleanup lost idempotency history")
	}
	instance := reopened.InstanceID()
	if hex.EncodeToString(instance[:]) != physical.data.Instance {
		test.Fatal("candidate cleanup replaced instance")
	}
}

func TestControlStoreEncryptedIdentityAndLock(test *testing.T) {
	root, directory := controlRoot(test)
	key := bytes.Repeat([]byte{81}, 32)
	store, err := OpenControlStore(test.Context(), root, key)
	if err != nil {
		test.Fatal("fresh control store refused", err)
	}
	identity := store.InstanceID()
	if identity == [16]byte{} {
		test.Fatal("zero instance identity")
	}
	if second, err := OpenControlStore(test.Context(), root, key); second != nil || !errors.Is(err, ErrBusy) {
		test.Fatal("second control process admitted", err)
	}
	if err := root.Close(); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); err != nil {
		test.Fatal(err)
	}
	reopenedRoot, err := os.OpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer reopenedRoot.Close()
	reopened, err := OpenControlStore(test.Context(), reopenedRoot, key)
	if err != nil {
		test.Fatal("durable store did not reopen", err)
	}
	if reopened.InstanceID() != identity {
		test.Fatal("instance was recreated")
	}
	if err := reopened.Close(); err != nil {
		test.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(directory, "control.log"))
	if err != nil || len(contents) == 0 || bytes.Contains(contents, []byte("Jobs")) || bytes.Contains(contents, key) {
		test.Fatal("journal is missing or plaintext", err)
	}
	if wrong, err := OpenControlStore(test.Context(), reopenedRoot, bytes.Repeat([]byte{82}, 32)); wrong != nil || err == nil {
		test.Fatal("wrong master key accepted")
	}
	contents[len(contents)-1] ^= 1
	if err := os.WriteFile(filepath.Join(directory, "control.log"), contents, 0600); err != nil {
		test.Fatal(err)
	}
	if corrupt, err := OpenControlStore(test.Context(), reopenedRoot, key); corrupt != nil || err == nil {
		test.Fatal("corrupt journal reset")
	}
}
