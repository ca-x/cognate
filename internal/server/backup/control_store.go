package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"slices"
	"sync"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"github.com/spf13/afero"
)

var (
	ErrControlConflict  = errors.New("backup control conflict")
	ErrControlNotFound  = errors.New("backup control not found")
	ErrControlBudget    = errors.New("backup control budget exceeded")
	ErrControlUncertain = errors.New("backup control uncertain")
)

const controlLimit = 16 << 20
const controlLogName = "control.log"
const controlNextName = "control.next"
const controlLockName = "control.lock"
const controlFenceName = "control.fence"

type ControlStore struct{ get func() *controlState }

type controlState struct {
	mutex                       sync.Mutex
	root                        *os.Root
	directory, lock, lockGuard  *os.File
	filesystem                  afero.Fs
	identity, journalIdentity   os.FileInfo
	key                         [32]byte
	data                        controlJournal
	ops                         controlOperations
	closed, uncertain, attached bool
}

type controlOperations struct {
	sync   func(afero.File) error
	close  func(afero.File) error
	rename func(string, string) error
}

type controlJournal struct {
	Version  uint32
	Instance string
	Jobs     []controlJob
}

type controlJob struct {
	Job                                     Job
	Principal, Request, Generation, Options string
	Catalog                                 []controlObject
	CompleteAttempted                       bool
}

type controlObject struct {
	Kind            ObjectKind
	Ordinal         uint32
	Size            int64
	SHA256, Attempt string
}

var controlRetained struct {
	sync.Mutex
	stores []*controlState
}

func (ControlStore) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private backup control store]")
}
func (ControlStore) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (ControlStore) LogValue() slog.Value         { return slog.StringValue("[private backup control store]") }

func OpenControlStore(ctx context.Context, root *os.Root, key []byte) (*ControlStore, error) {
	if ctx == nil || root == nil || len(key) != 32 {
		return nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := controlPlatform(); err != nil {
		return nil, err
	}
	owned, err := root.OpenRoot(".")
	if err != nil {
		return nil, ErrInvalid
	}
	physical := &controlState{root: owned}
	physical.filesystem = skill.RootedFS(owned)
	physical.ops = controlOperations{sync: func(file afero.File) error { return file.Sync() }, close: func(file afero.File) error { return file.Close() }, rename: owned.Rename}
	copy(physical.key[:], key)
	fail := func(cause error) (*ControlStore, error) {
		if errors.Is(cause, ErrControlUncertain) {
			physical.uncertain = true
		}
		if physical.uncertain {
			physical.retain()
			return nil, ErrControlUncertain
		}
		if physical.directory != nil && physical.closeFile(physical.directory) != nil {
			physical.retain()
			return nil, ErrControlUncertain
		}
		if physical.lock != nil && physical.closeFile(physical.lock) != nil {
			physical.retain()
			return nil, ErrControlUncertain
		}
		if physical.lockGuard != nil && physical.closeFile(physical.lockGuard) != nil {
			physical.retain()
			return nil, ErrControlUncertain
		}
		if err := owned.Close(); err != nil {
			physical.retain()
			return nil, ErrControlUncertain
		}
		clear(physical.key[:])
		return nil, controlError(cause)
	}
	physical.identity, err = owned.Stat(".")
	if err != nil || !controlPrivate(physical.identity, true) {
		return fail(ErrInvalid)
	}
	if controlQuarantined(physical.identity) {
		return fail(ErrBusy)
	}
	physical.directory, err = owned.Open(".")
	if err != nil {
		return fail(ErrInvalid)
	}
	entries, err := physical.directory.ReadDir(5)
	if err != nil && !errors.Is(err, io.EOF) {
		return fail(ErrInvalid)
	}
	for _, entry := range entries {
		if entry.Name() != controlLogName && entry.Name() != controlLockName && entry.Name() != controlNextName && entry.Name() != controlFenceName {
			return fail(ErrInvalid)
		}
	}
	physical.lock, err = controlLock(owned)
	if err != nil {
		return fail(err)
	}
	physical.lockGuard, err = controlDuplicateLock(physical.lock)
	if err != nil {
		return fail(err)
	}
	if err := physical.check(); err != nil {
		return fail(err)
	}
	if marker, err := owned.Lstat(controlFenceName); err == nil {
		if !controlPrivate(marker, false) {
			return fail(ErrInvalid)
		}
		return fail(ErrBusy)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(ErrInvalid)
	}
	encoded, readError := physical.read(controlLogName)
	defer clear(encoded)
	if errors.Is(readError, os.ErrNotExist) {
		if len(entries) != 0 {
			return fail(ErrControlUncertain)
		}
		instance := uuid.New()
		physical.data = controlJournal{Version: 1, Instance: hex.EncodeToString(instance[:])}
		if err := physical.persist(physical.data); err != nil {
			return fail(err)
		}
	} else {
		if readError != nil {
			return fail(readError)
		}
		if err := physical.decode(encoded); err != nil {
			return fail(err)
		}
		if _, err := owned.Lstat(controlNextName); err == nil {
			if err := physical.discardCandidate(); err != nil {
				return fail(err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return fail(ErrInvalid)
		}
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	return &ControlStore{get: func() *controlState { return physical }}, nil
}

func (store *ControlStore) InstanceID() (identity [16]byte) {
	if store == nil || store.get == nil {
		return identity
	}
	physical := store.get()
	physical.mutex.Lock()
	defer physical.mutex.Unlock()
	_, _ = hex.Decode(identity[:], []byte(physical.data.Instance))
	return identity
}

func (store *ControlStore) Close() error {
	if store == nil || store.get == nil {
		return nil
	}
	physical := store.get()
	physical.mutex.Lock()
	defer physical.mutex.Unlock()
	if physical.uncertain {
		physical.retain()
		return ErrControlUncertain
	}
	if physical.closed {
		return nil
	}
	if physical.attached {
		return ErrBusy
	}
	if err := physical.check(); err != nil {
		return physical.poison()
	}
	if physical.ops.sync(physical.directory) != nil {
		physical.retain()
		return physical.poison()
	}
	marker, err := physical.armRelease()
	if err != nil {
		return err
	}
	if physical.closeFile(physical.directory) != nil {
		return physical.poison()
	}
	if physical.closeFile(physical.lock) != nil {
		physical.retain()
		return physical.poison()
	}
	if physical.closeFile(physical.lockGuard) != nil {
		return physical.poison()
	}
	physical.closed = true
	if err := physical.clearRelease(marker); err != nil {
		return err
	}
	if physical.root.Close() != nil {
		physical.retain()
		return physical.poison()
	}
	clear(physical.key[:])
	return nil
}

func controlQuarantined(identity os.FileInfo) bool {
	controlRetained.Lock()
	defer controlRetained.Unlock()
	for _, physical := range controlRetained.stores {
		if physical.identity != nil && os.SameFile(identity, physical.identity) {
			return true
		}
	}
	return false
}

func (physical *controlState) armRelease() (os.FileInfo, error) {
	file, err := physical.filesystem.OpenFile(controlFenceName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return nil, physical.poison()
	}
	count, writeError := file.Write([]byte("CGBKCF01"))
	if writeError == nil && count == 8 {
		writeError = physical.ops.sync(file)
	}
	identity, statError := file.Stat()
	pinError := physical.pin(controlFenceName, file)
	closeError := physical.closeFile(file)
	if writeError != nil || count != 8 || statError != nil || pinError != nil || closeError != nil || physical.ops.sync(physical.directory) != nil {
		return nil, physical.poison()
	}
	named, err := physical.root.Lstat(controlFenceName)
	if err != nil || !controlPrivate(named, false) || !os.SameFile(identity, named) || physical.check() != nil {
		return nil, physical.poison()
	}
	return named, nil
}

func (physical *controlState) clearRelease(identity os.FileInfo) error {
	directory, err := physical.root.Open(".")
	if err != nil {
		return physical.poison()
	}
	opened, statError := directory.Stat()
	named, markerError := physical.root.Lstat(controlFenceName)
	if statError != nil || markerError != nil || !controlPrivate(opened, true) || !os.SameFile(physical.identity, opened) || !controlPrivate(named, false) || !os.SameFile(identity, named) {
		_ = physical.closeFile(directory)
		return physical.poison()
	}
	removeError := physical.root.Remove(controlFenceName)
	var syncError error
	if removeError == nil {
		syncError = physical.ops.sync(directory)
	}
	closeError := physical.closeFile(directory)
	if removeError != nil || syncError != nil || closeError != nil {
		return physical.poison()
	}
	return nil
}

func (physical *controlState) retain() {
	controlRetained.Lock()
	defer controlRetained.Unlock()
	if !slices.Contains(controlRetained.stores, physical) {
		controlRetained.stores = append(controlRetained.stores, physical)
	}
}

func (physical *controlState) poison() error {
	physical.uncertain = true
	physical.retain()
	return ErrControlUncertain
}

func (physical *controlState) closeFile(file afero.File) error {
	if physical.ops.close(file) != nil {
		return physical.poison()
	}
	return nil
}

func (physical *controlState) check() error {
	if physical.uncertain {
		return ErrControlUncertain
	}
	if physical.closed {
		return ErrClosed
	}
	current, err := physical.root.Stat(".")
	if err != nil || !controlPrivate(current, true) || !os.SameFile(physical.identity, current) {
		return ErrInvalid
	}
	if err := physical.pin(controlLockName, physical.lock); err != nil {
		return err
	}
	if physical.journalIdentity != nil {
		current, err := physical.root.Lstat(controlLogName)
		if err != nil || !controlPrivate(current, false) || !os.SameFile(current, physical.journalIdentity) {
			return ErrInvalid
		}
	}
	return nil
}

func (physical *controlState) pin(name string, file afero.File) error {
	opened, err := file.Stat()
	if err != nil || !controlPrivate(opened, false) {
		return ErrInvalid
	}
	named, err := physical.root.Lstat(name)
	if err != nil || !controlPrivate(named, false) || !os.SameFile(opened, named) {
		return ErrInvalid
	}
	return nil
}

func (physical *controlState) read(name string) ([]byte, error) {
	info, err := physical.root.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return nil, os.ErrNotExist
	}
	if err == nil && info.Size() > controlLimit {
		return nil, ErrControlBudget
	}
	if err != nil || !controlPrivate(info, false) || info.Size() < 52 {
		return nil, ErrInvalid
	}
	file, err := physical.filesystem.Open(name)
	if err != nil {
		return nil, ErrInvalid
	}
	encoded, readError := io.ReadAll(io.LimitReader(file, controlLimit+1))
	pinError := physical.pin(name, file)
	closeError := physical.closeFile(file)
	if closeError != nil {
		clear(encoded)
		return nil, closeError
	}
	if readError != nil || pinError != nil || int64(len(encoded)) != info.Size() {
		clear(encoded)
		return nil, ErrInvalid
	}
	if name == controlLogName {
		physical.journalIdentity = info
	}
	return encoded, nil
}

func (physical *controlState) aead(instance []byte) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, physical.key[:], instance, "cognate/server-backup/control/v1", 32)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrInvalid
	}
	return cipher.NewGCM(block)
}

func (physical *controlState) encode(candidate controlJournal) ([]byte, error) {
	plain, err := json.Marshal(candidate)
	if err != nil {
		return nil, ErrInvalid
	}
	defer clear(plain)
	if len(plain)+52 > controlLimit {
		return nil, ErrControlBudget
	}
	var header [36]byte
	copy(header[:8], "CGBKCT01")
	if _, err := hex.Decode(header[8:24], []byte(candidate.Instance)); err != nil {
		return nil, ErrInvalid
	}
	if _, err := rand.Read(header[24:]); err != nil {
		return nil, ErrUnavailable
	}
	aead, err := physical.aead(header[8:24])
	if err != nil {
		return nil, err
	}
	return aead.Seal(header[:], header[24:], plain, header[:24]), nil
}

func (physical *controlState) decode(encoded []byte) error {
	if len(encoded) < 52 || string(encoded[:8]) != "CGBKCT01" {
		return ErrInvalid
	}
	aead, err := physical.aead(encoded[8:24])
	if err != nil {
		return err
	}
	plain, err := aead.Open(nil, encoded[24:36], encoded[36:], encoded[:24])
	if err != nil {
		return ErrInvalid
	}
	defer clear(plain)
	if err := controlJSONBounds(plain); err != nil {
		return err
	}
	var candidate controlJournal
	if json.Unmarshal(plain, &candidate, json.RejectUnknownMembers(true)) != nil || candidate.Instance != hex.EncodeToString(encoded[8:24]) {
		return ErrInvalid
	}
	if err := candidate.valid(); err != nil {
		return err
	}
	physical.data = candidate
	return nil
}

func (physical *controlState) persist(candidate controlJournal) error {
	if err := physical.check(); err != nil {
		return err
	}
	if err := candidate.valid(); err != nil {
		return err
	}
	if err := candidate.reserve(); err != nil {
		return err
	}
	encoded, err := physical.encode(candidate)
	if err != nil {
		return err
	}
	defer clear(encoded)
	file, err := physical.filesystem.OpenFile(controlNextName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return physical.poison()
	}
	count, writeError := file.Write(encoded)
	if writeError == nil && count == len(encoded) {
		writeError = physical.ops.sync(file)
	}
	pinError := physical.pin(controlNextName, file)
	staged, statError := file.Stat()
	closeError := physical.closeFile(file)
	if writeError != nil || count != len(encoded) || pinError != nil || closeError != nil || statError != nil {
		return physical.poison()
	}
	if physical.check() != nil || physical.ops.rename(controlNextName, controlLogName) != nil {
		return physical.poison()
	}
	if physical.ops.sync(physical.directory) != nil {
		return physical.poison()
	}
	identity, err := physical.root.Lstat(controlLogName)
	if err != nil || !controlPrivate(identity, false) || !os.SameFile(identity, staged) {
		return physical.poison()
	}
	physical.journalIdentity = identity
	if physical.check() != nil {
		return physical.poison()
	}
	physical.data = candidate
	return nil
}

func (journal controlJournal) reserve() error {
	candidate := journal
	candidate.Jobs = slices.Clone(journal.Jobs)
	maximumTime := time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.FixedZone("", 86340))
	for index := range candidate.Jobs {
		record := &candidate.Jobs[index]
		record.Job.Phase, record.Job.Code = "remote_committed", "verify_unknown"
		record.Job.UpdatedAt = maximumTime
		record.Job.Objects, record.Job.CiphertextBytes = math.MaxUint32, math.MaxInt64
		record.Job.CancelRequested, record.CompleteAttempted = false, false
	}
	encoded, err := json.Marshal(candidate)
	if err != nil {
		return ErrInvalid
	}
	defer clear(encoded)
	if len(encoded)+52 > controlLimit {
		return ErrControlBudget
	}
	return nil
}

func (physical *controlState) discardCandidate() error {
	file, err := physical.filesystem.Open(controlNextName)
	if err != nil {
		return physical.poison()
	}
	pinError := physical.pin(controlNextName, file)
	closeError := physical.closeFile(file)
	if pinError != nil || closeError != nil {
		return physical.poison()
	}
	if physical.root.Remove(controlNextName) != nil || physical.ops.sync(physical.directory) != nil {
		return physical.poison()
	}
	return nil
}

func controlJSONBounds(plain []byte) error {
	decoder := jsontext.NewDecoder(bytes.NewReader(plain))
	depth, jobs, objects := 0, 0, 0
	for count := 0; count < 2000000; count++ {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return ErrInvalid
		}
		switch token.Kind() {
		case '{', '[':
			depth++
			if token.Kind() == '{' && depth == 3 {
				jobs++
			}
			if token.Kind() == '{' && depth == 5 {
				objects++
			}
		case '}', ']':
			depth--
		}
		if depth > 8 || jobs > 1000 || objects > controlLimit/64 {
			return ErrControlBudget
		}
	}
	return ErrControlBudget
}

func (journal controlJournal) valid() error {
	if len(journal.Jobs) > 1000 {
		return ErrControlBudget
	}
	if journal.Version != 1 || !sealHex(journal.Instance, 16) || journal.Instance == "00000000000000000000000000000000" {
		return ErrInvalid
	}
	identities, requests := make(map[string]bool), make(map[string]bool)
	for _, record := range journal.Jobs {
		request := record.Principal + record.Request
		if !controlUUID(record.Job.ID) || !controlUUID(record.Principal) || !controlUUID(record.Request) || !sealHex(record.Generation, 32) || !sealHex(record.Options, 32) || identities[record.Job.ID] || requests[request] || record.Job.CreatedAt.IsZero() || record.Job.UpdatedAt.Before(record.Job.CreatedAt) || !controlPhase(record.Job.Phase) || !controlCode(record.Job.Code) {
			return ErrInvalid
		}
		identities[record.Job.ID], requests[request] = true, true
		if err := record.validCatalog(journal.Instance); err != nil {
			return err
		}
	}
	return nil
}

func (record controlJob) validCatalog(instance string) error {
	if len(record.Catalog) == 0 {
		if record.Job.Objects != 0 || record.Job.CiphertextBytes != 0 || record.CompleteAttempted || record.Job.Phase == "remote_committed" || record.Job.Phase == "uploading" || record.Job.Phase == "committing" || record.Job.Phase == "commit_unknown" {
			return ErrInvalid
		}
		return nil
	}
	if len(record.Catalog) < 5 || len(record.Catalog) > 100002 || record.Job.Objects != uint32(len(record.Catalog)) || record.Catalog[0].Kind != Bootstrap || record.Catalog[len(record.Catalog)-1].Kind != Complete {
		return ErrInvalid
	}
	var total int64
	var stage, dataOrdinal, pageOrdinal uint32
	pageOrdinal = 1
	seen := make(map[[2]uint32]bool)
	previous := record.Catalog[0].Attempt
	for index, object := range record.Catalog {
		if object.Attempt != previous {
			return ErrInvalid
		}
		ref, err := object.ref(instance, record.Job.ID)
		if err != nil {
			return err
		}
		identity := [2]uint32{uint32(object.Kind), object.Ordinal}
		if seen[identity] || index > 0 && object.Kind == Bootstrap || index < len(record.Catalog)-1 && object.Kind == Complete {
			return ErrInvalid
		}
		seen[identity] = true
		if index > 0 && index < len(record.Catalog)-1 {
			switch object.Kind {
			case DataExtent:
				if stage != 0 || object.Ordinal != dataOrdinal {
					return ErrInvalid
				}
				dataOrdinal++
			case ManifestPage:
				if object.Ordinal == 0 {
					if stage != 1 {
						return ErrInvalid
					}
					stage = 2
				} else {
					if stage > 1 || object.Ordinal != pageOrdinal || dataOrdinal == 0 {
						return ErrInvalid
					}
					stage = 1
					pageOrdinal++
				}
			default:
				return ErrInvalid
			}
		}
		total += ref.read().size
	}
	if stage != 2 || total != record.Job.CiphertextBytes || record.Job.Phase == "pending" || record.Job.Phase == "capturing" || (record.Job.Phase == "remote_committed" || record.Job.Phase == "commit_unknown" || record.Job.Phase == "committing") != record.CompleteAttempted {
		return ErrInvalid
	}
	return nil
}

func (object controlObject) ref(instance, backup string) (ObjectRef, error) {
	var instanceID, attemptID [16]byte
	var digest [32]byte
	backupID, err := uuid.Parse(backup)
	if err != nil || !sealHex(instance, 16) || !sealHex(object.Attempt, 16) || !sealHex(object.SHA256, 32) {
		return ObjectRef{}, ErrInvalid
	}
	_, _ = hex.Decode(instanceID[:], []byte(instance))
	_, _ = hex.Decode(attemptID[:], []byte(object.Attempt))
	_, _ = hex.Decode(digest[:], []byte(object.SHA256))
	attempt, err := NewAttempt(instanceID, [16]byte(backupID), attemptID)
	if err != nil {
		return ObjectRef{}, err
	}
	return NewObjectRef(attempt, object.Kind, object.Ordinal, object.Size, digest)
}

func controlUUID(value string) bool {
	parsed, err := uuid.Parse(value)
	return err == nil && parsed != uuid.UUID{} && parsed.String() == value
}

func controlError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{ErrControlUncertain, ErrSealUncertain, ErrSealCleanup, library.ErrSnapshotArchiveUncertain, state.ErrLocalSnapshotUncertain, state.ErrLocalSnapshotCleanup} {
		if errors.Is(err, known) {
			return ErrControlUncertain
		}
	}
	if errors.Is(err, ErrSealBudget) || errors.Is(err, state.ErrLocalSnapshotBudget) {
		return ErrControlBudget
	}
	for _, known := range []error{ErrControlConflict, ErrControlNotFound, ErrControlBudget, ErrInvalid, ErrBusy, ErrClosed, ErrDenied, ErrUnsupported, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrUnavailable
}
