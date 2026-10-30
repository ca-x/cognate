package backup

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"time"
)

var ErrSealBudget = errors.New("backup crypto budget exceeded")
var ErrSealCleanup = errors.New("backup crypto cleanup failed")
var ErrSealUncertain = errors.New("backup crypto cleanup uncertain")

type SealLimits struct {
	TotalPlainBytes, TotalCipherBytes, StagingBytes, InputStagingBytes int64
	MaxObjects, MaxInventoryRecords                                    uint32
	OperationTimeout                                                   time.Duration
}

type RecoveryKey struct{ get func() *recoveryKey }
type SealEngine struct{ get func() *sealEngine }
type SealAttempt struct{ get func() *sealAttempt }
type SealedFile struct{ get func() *sealArtifact }
type recoveryKey struct {
	mutex    sync.Mutex
	id       [16]byte
	material [32]byte
	closed   bool
	engines  map[*sealEngine]struct{}
}

type sealEngine struct {
	mutex               sync.Mutex
	changed             chan struct{}
	ctx                 context.Context
	cancel              context.CancelFunc
	closed              bool
	uncertain           bool
	active              int
	key                 *recoveryKey
	directory           *sealDirectory
	limits              SealLimits
	plain, cipher, disk int64
	objects             uint32
	attempt             *sealAttempt
	sessions            map[*sealOpenState]struct{}
	ops                 sealPhysicalOps
	closeOnce           sync.Once
	closeError          error
}

type sealPhysicalOps struct {
	sync  func(*os.File) error
	close func(*os.File) error
}
type sealDirectory struct {
	mutex        sync.Mutex
	parent, root *os.File
	name         string
	refs         int
	failed       bool
}

type sealCharge struct {
	engine              *sealEngine
	plain, cipher, disk int64
	objects             uint32
	once                sync.Once
}

type sealAttempt struct {
	mutex                sync.Mutex
	changed              chan struct{}
	engine               *sealEngine
	ctx                  context.Context
	cancel               context.CancelFunc
	identity             attemptData
	dek                  [32]byte
	closed, poison       bool
	active               int
	phase                byte
	used                 [2][1563]uint64
	locations            [2][100000]uint32
	catalog              *os.File
	catalogName          string
	catalogCharge        *sealCharge
	entries              uint32
	dataCount, pageCount uint32
	bootstrap            ObjectRef
	root                 ObjectRef
	manifest             sealRoot
	order                sealOrder
	closeOnce            sync.Once
	closeError           error
}

type sealArtifact struct {
	mutex      sync.Mutex
	changed    chan struct{}
	directory  *sealDirectory
	engine     *sealEngine
	name       string
	identity   os.FileInfo
	ref        ObjectRef
	charge     *sealCharge
	closed     bool
	active     int
	closeOnce  sync.Once
	closeError error
}

func (RecoveryKey) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup key]")
}
func (RecoveryKey) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (RecoveryKey) LogValue() slog.Value         { return slog.StringValue("[private backup key]") }
func (SealEngine) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup crypto]")
}
func (SealEngine) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (SealEngine) LogValue() slog.Value         { return slog.StringValue("[private backup crypto]") }
func (SealAttempt) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private backup attempt]")
}
func (SealAttempt) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (SealAttempt) LogValue() slog.Value         { return slog.StringValue("[private backup attempt]") }
func (SealedFile) Format(state fmt.State, _ rune) {
	_, _ = io.WriteString(state, "[private sealed file]")
}
func (SealedFile) MarshalJSON() ([]byte, error) { return []byte(`{}`), nil }
func (SealedFile) LogValue() slog.Value         { return slog.StringValue("[private sealed file]") }

func ImportRecoveryKey(id [16]byte, material []byte) (RecoveryKey, error) {
	if id == [16]byte{} || len(material) != 32 {
		return RecoveryKey{}, ErrInvalid
	}
	state := &recoveryKey{id: id, engines: make(map[*sealEngine]struct{})}
	copy(state.material[:], material)
	return RecoveryKey{get: func() *recoveryKey { return state }}, nil
}

func (key RecoveryKey) Destroy() error {
	if key.get == nil {
		return nil
	}
	state := key.get()
	state.mutex.Lock()
	state.closed = true
	engines := make([]*sealEngine, 0, len(state.engines))
	for engine := range state.engines {
		engine.cancel()
		engines = append(engines, engine)
	}
	state.mutex.Unlock()
	var result error
	for _, engine := range engines {
		if err := (SealEngine{get: func() *sealEngine { return engine }}).Close(); err != nil {
			result = mergeSealCleanup(result, err)
		}
	}
	state.mutex.Lock()
	clear(state.material[:])
	state.mutex.Unlock()
	return result
}

func NewSealEngine(parent *os.File, key RecoveryKey, limits SealLimits) (SealEngine, error) {
	if key.get == nil || limits.TotalPlainBytes <= 0 || limits.TotalCipherBytes <= 0 || limits.StagingBytes <= 0 || limits.InputStagingBytes <= 0 || limits.InputStagingBytes > limits.StagingBytes || limits.MaxObjects == 0 || limits.MaxObjects > 100000 || limits.MaxInventoryRecords == 0 || limits.OperationTimeout <= 0 {
		return SealEngine{}, ErrInvalid
	}
	state := key.get()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return SealEngine{}, ErrClosed
	}
	directory, err := newSealDirectory(parent)
	if err != nil {
		return SealEngine{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	engine := &sealEngine{changed: make(chan struct{}), ctx: ctx, cancel: cancel, key: state, directory: directory, limits: limits, disk: limits.InputStagingBytes, sessions: make(map[*sealOpenState]struct{}), ops: sealPhysicalOps{sync: (*os.File).Sync, close: (*os.File).Close}}
	state.engines[engine] = struct{}{}
	return SealEngine{get: func() *sealEngine { return engine }}, nil
}

func (engine *sealEngine) enter(ctx context.Context) (context.Context, func(), error) {
	if ctx == nil {
		return nil, nil, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return nil, nil, err
	}
	engine.key.mutex.Lock()
	if engine.key.closed {
		engine.key.mutex.Unlock()
		return nil, nil, ErrClosed
	}
	engine.mutex.Lock()
	engine.key.mutex.Unlock()
	defer engine.mutex.Unlock()
	if engine.closed {
		return nil, nil, ErrClosed
	}
	if engine.uncertain {
		return nil, nil, ErrSealUncertain
	}
	if engine.active >= 2 {
		return nil, nil, ErrBusy
	}
	scoped, cancel := context.WithTimeout(ctx, engine.limits.OperationTimeout)
	stop := context.AfterFunc(engine.ctx, cancel)
	engine.active++
	return scoped, func() {
		stop()
		cancel()
		engine.mutex.Lock()
		engine.active--
		close(engine.changed)
		engine.changed = make(chan struct{})
		engine.mutex.Unlock()
	}, nil
}

func (engine *sealEngine) reserve(plain, cipher, disk int64, objects uint32) (*sealCharge, error) {
	engine.mutex.Lock()
	defer engine.mutex.Unlock()
	if plain < 0 || cipher < 0 || disk < 0 || plain > engine.limits.TotalPlainBytes-engine.plain || cipher > engine.limits.TotalCipherBytes-engine.cipher || disk > engine.limits.StagingBytes-engine.disk || objects > engine.limits.MaxObjects-engine.objects {
		return nil, ErrSealBudget
	}
	engine.plain += plain
	engine.cipher += cipher
	engine.disk += disk
	engine.objects += objects
	return &sealCharge{engine: engine, plain: plain, cipher: cipher, disk: disk, objects: objects}, nil
}
func (charge *sealCharge) release() {
	if charge == nil {
		return
	}
	charge.once.Do(func() {
		engine := charge.engine
		engine.mutex.Lock()
		engine.plain -= charge.plain
		engine.cipher -= charge.cipher
		engine.disk -= charge.disk
		engine.objects -= charge.objects
		engine.mutex.Unlock()
	})
}

func (engine SealEngine) Close() error {
	if engine.get == nil {
		return nil
	}
	state := engine.get()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.closed = true
		state.cancel()
		for state.active != 0 {
			changed := state.changed
			state.mutex.Unlock()
			<-changed
			state.mutex.Lock()
		}
		if state.uncertain {
			state.closeError = ErrSealUncertain
		}
		sessions := make([]*sealOpenState, 0, len(state.sessions))
		for session := range state.sessions {
			sessions = append(sessions, session)
		}
		state.mutex.Unlock()
		if state.attempt != nil {
			if err := (SealAttempt{get: func() *sealAttempt { return state.attempt }}).Close(); err != nil {
				state.closeError = mergeSealCleanup(state.closeError, err)
			}
		}
		for _, session := range sessions {
			if err := session.close(); err != nil {
				state.closeError = mergeSealCleanup(state.closeError, err)
			}
		}
		if err := releaseSealDirectory(state.directory); err != nil {
			state.closeError = mergeSealCleanup(state.closeError, err)
		}
		state.key.mutex.Lock()
		delete(state.key.engines, state)
		state.key.mutex.Unlock()
	})
	return state.closeError
}

func (attempt SealAttempt) Close() error {
	if attempt.get == nil {
		return nil
	}
	state := attempt.get()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.closed = true
		state.cancel()
		for state.active != 0 {
			changed := state.changed
			state.mutex.Unlock()
			<-changed
			state.mutex.Lock()
		}
		clear(state.dek[:])
		state.mutex.Unlock()
		if state.catalog != nil {
			state.closeError = state.engine.removePrivate(state.catalogName, state.catalogCharge, state.engine.closeFile(state.catalog))
		}
	})
	return state.closeError
}

func (engine SealEngine) BeginAttempt(ctx context.Context, instance, backup [16]byte) (SealAttempt, SealedFile, error) {
	if engine.get == nil || instance == [16]byte{} || backup == [16]byte{} {
		return SealAttempt{}, SealedFile{}, ErrInvalid
	}
	state := engine.get()
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return SealAttempt{}, SealedFile{}, err
	}
	defer release()
	state.mutex.Lock()
	if state.attempt != nil {
		state.attempt.mutex.Lock()
		closed := state.attempt.closed
		state.attempt.mutex.Unlock()
		if !closed {
			state.mutex.Unlock()
			return SealAttempt{}, SealedFile{}, ErrBusy
		}
	}
	attemptContext, cancel := context.WithCancel(state.ctx)
	data := &sealAttempt{engine: state, changed: make(chan struct{}), ctx: attemptContext, cancel: cancel, identity: attemptData{instance: instance, backup: backup}}
	if _, err = rand.Read(data.identity.attempt[:]); err != nil {
		state.mutex.Unlock()
		cancel()
		return SealAttempt{}, SealedFile{}, ErrUnavailable
	}
	if _, err = rand.Read(data.dek[:]); err != nil {
		state.mutex.Unlock()
		cancel()
		return SealAttempt{}, SealedFile{}, ErrUnavailable
	}
	state.attempt = data
	state.mutex.Unlock()
	wrapped := SealAttempt{get: func() *sealAttempt { return data }}
	fail := func(err error) (SealAttempt, SealedFile, error) {
		return SealAttempt{}, SealedFile{}, mergeSealCleanup(err, wrapped.Close())
	}
	data.catalogCharge, err = state.reserve(0, 0, int64(state.limits.MaxObjects)*128, 0)
	if err != nil {
		return fail(err)
	}
	data.catalogName = fmt.Sprintf("i-%x", data.identity.attempt)
	data.catalog, err = sealOpen(state.directory, data.catalogName, true)
	if err != nil {
		data.catalogCharge.release()
		return fail(err)
	}
	var header [96]byte
	copy(header[:8], "CGBKBT01")
	binary.BigEndian.PutUint16(header[8:10], 1)
	binary.BigEndian.PutUint16(header[10:12], 1)
	copy(header[16:32], instance[:])
	copy(header[32:48], backup[:])
	copy(header[48:64], data.identity.attempt[:])
	copy(header[64:80], state.key.id[:])
	binary.BigEndian.PutUint32(header[80:84], sealFrame)
	binary.BigEndian.PutUint32(header[84:88], 60)
	state.key.mutex.Lock()
	key, deriveErr := sealDerive(state.key.material[:], data.identity.attempt, "cognate/f4/bootstrap-key/v1\x00", header[:])
	state.key.mutex.Unlock()
	if deriveErr != nil {
		return fail(deriveErr)
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return fail(ErrInvalid)
	}
	aead, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		return fail(ErrInvalid)
	}
	encoded := append(header[:], aead.Seal(nil, nil, data.dek[:], append([]byte("cognate/f4/bootstrap-aad/v1\x00"), header[:]...))...)
	artifact, err := state.writeArtifact(scoped, data.identity, 0, 0, 0, 156, func(output io.Writer) error { return sealWrite(scoped, output, encoded) })
	if err != nil {
		return fail(err)
	}
	data.bootstrap = artifact.Ref()
	return wrapped, artifact, nil
}

func (engine SealEngine) SealExtent(ctx context.Context, attempt SealAttempt, ordinal uint32, source *os.File) (SealedFile, error) {
	if engine.get == nil || attempt.get == nil || source == nil {
		return SealedFile{}, ErrInvalid
	}
	state, data := engine.get(), attempt.get()
	if data.engine != state {
		return SealedFile{}, ErrInvalid
	}
	scoped, release, err := state.enter(ctx)
	if err != nil {
		return SealedFile{}, err
	}
	defer release()
	data.mutex.Lock()
	if data.closed || data.poison || data.phase != 0 || ordinal >= state.limits.MaxObjects || data.used[0][ordinal/64]&(1<<(ordinal%64)) != 0 {
		data.mutex.Unlock()
		return SealedFile{}, ErrInvalid
	}
	data.used[0][ordinal/64] |= 1 << (ordinal % 64)
	data.active++
	secret := data.dek
	data.mutex.Unlock()
	defer clear(secret[:])
	scoped, attemptCancel := context.WithCancel(scoped)
	stop := context.AfterFunc(data.ctx, attemptCancel)
	defer stop()
	defer attemptCancel()
	defer func() {
		data.mutex.Lock()
		data.active--
		close(data.changed)
		data.changed = make(chan struct{})
		data.mutex.Unlock()
	}()
	before, err := sealBorrow(source)
	if err != nil {
		return SealedFile{}, err
	}
	if before.Size() <= 0 || before.Size() > sealExtentLimit || before.Size() > state.limits.InputStagingBytes {
		return SealedFile{}, ErrSealBudget
	}
	header := sealHeader{purpose: 1, attempt: data.identity, ordinal: ordinal, length: before.Size()}
	if _, err = rand.Read(header.nonce[:]); err != nil {
		return SealedFile{}, ErrUnavailable
	}
	length, _ := sealCipherSize(header.length)
	var plainHash [32]byte
	artifact, err := state.writeArtifact(scoped, data.identity, 1, ordinal, header.length, length, func(output io.Writer) error {
		var streamErr error
		plainHash, streamErr = sealEncrypt(scoped, header, secret[:], &sealOffsetReader{ctx: scoped, file: source}, output)
		if streamErr != nil {
			return streamErr
		}
		return sealRepin(source, before)
	})
	if err != nil {
		data.mutex.Lock()
		data.poison = true
		data.mutex.Unlock()
		return SealedFile{}, err
	}
	if err = sealRepin(source, before); err != nil {
		data.mutex.Lock()
		data.poison = true
		data.mutex.Unlock()
		return SealedFile{}, mergeSealCleanup(err, artifact.Close())
	}
	data.mutex.Lock()
	if data.closed || data.ctx.Err() != nil {
		err = context.Canceled
	} else {
		err = data.addRecord(sealRecord{ordinal: ordinal, length: header.length, plainHash: plainHash, cipherBytes: length, cipherHash: artifact.Ref().read().digest}, 0)
	}
	if err == nil {
		data.dataCount++
	} else {
		data.poison = true
	}
	data.mutex.Unlock()
	if err != nil {
		return SealedFile{}, mergeSealCleanup(err, artifact.Close())
	}
	return artifact, nil
}

func (engine *sealEngine) writeArtifact(ctx context.Context, identity attemptData, purpose byte, ordinal uint32, plain, length int64, produce func(io.Writer) error) (SealedFile, error) {
	objects := uint32(1)
	if purpose == 0 || purpose == 4 {
		objects = 0
	}
	charge, err := engine.reserve(plain, length, length, objects)
	if err != nil {
		return SealedFile{}, err
	}
	name := sealObjectName(identity, purpose, ordinal)
	file, err := sealOpen(engine.directory, name, true)
	if err != nil {
		charge.release()
		return SealedFile{}, err
	}
	hash := sha256.New()
	writeError := produce(io.MultiWriter(file, hash))
	if writeError == nil {
		writeError = engine.ops.sync(file)
	}
	closeError := engine.closeFile(file)
	if writeError == nil && closeError != nil {
		writeError = ErrSealUncertain
	}
	if writeError == nil {
		writeError = engine.ops.sync(engine.directory.root)
	}
	if writeError == nil {
		writeError = ctx.Err()
	}
	if writeError != nil {
		if cleanupErr := engine.removePrivate(name, charge, closeError); cleanupErr != nil {
			return SealedFile{}, cleanupErr
		}
		return SealedFile{}, sealError(ctx, writeError)
	}
	readOnly, err := sealOpen(engine.directory, name, false)
	if err != nil {
		if cleanupErr := engine.removePrivate(name, charge, nil); cleanupErr != nil {
			return SealedFile{}, cleanupErr
		}
		return SealedFile{}, err
	}
	info, statErr := sealBorrow(readOnly)
	readClose := engine.closeFile(readOnly)
	if statErr != nil || readClose != nil || info.Size() != length || ctx.Err() != nil {
		if cleanupErr := engine.removePrivate(name, charge, readClose); cleanupErr != nil {
			return SealedFile{}, cleanupErr
		}
		return SealedFile{}, sealError(ctx, ErrInvalid)
	}
	kind := DataExtent
	if purpose == 0 {
		kind = Bootstrap
	} else if purpose == 2 || purpose == 3 {
		kind = ManifestPage
	} else if purpose == 4 {
		kind = Complete
	}
	attempt, _ := NewAttempt(identity.instance, identity.backup, identity.attempt)
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	ref, err := NewObjectRef(attempt, kind, ordinal, length, digest)
	if err != nil {
		if cleanupErr := engine.removePrivate(name, charge, nil); cleanupErr != nil {
			return SealedFile{}, cleanupErr
		}
		return SealedFile{}, err
	}
	engine.directory.mutex.Lock()
	engine.directory.refs++
	engine.directory.mutex.Unlock()
	artifact := &sealArtifact{changed: make(chan struct{}), directory: engine.directory, engine: engine, name: name, identity: info, ref: ref, charge: charge}
	return SealedFile{get: func() *sealArtifact { return artifact }}, nil
}

func sealObjectName(identity attemptData, purpose byte, ordinal uint32) string {
	return fmt.Sprintf("s-%x-%d-%08x", identity.attempt, purpose, ordinal)
}
func sealError(ctx context.Context, err error) error {
	for _, known := range []error{ErrSealUncertain, ErrSealCleanup} {
		if errors.Is(err, known) {
			return known
		}
	}
	if ctx != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	for _, known := range []error{ErrInvalid, ErrClosed, ErrBusy, ErrUnsupported, ErrSealBudget} {
		if errors.Is(err, known) {
			return known
		}
	}
	return ErrInvalid
}

func (file SealedFile) WithFile(ctx context.Context, consume func(*os.File) error) (returnedError error) {
	if file.get == nil || ctx == nil || consume == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	state := file.get()
	state.mutex.Lock()
	if state.closed {
		state.mutex.Unlock()
		return ErrClosed
	}
	state.active++
	state.mutex.Unlock()
	defer func() {
		state.mutex.Lock()
		state.active--
		close(state.changed)
		state.changed = make(chan struct{})
		state.mutex.Unlock()
	}()
	input, err := sealOpen(state.directory, state.name, false)
	if err != nil {
		return err
	}
	defer func() {
		if state.engine.closeFile(input) != nil {
			returnedError = ErrSealUncertain
		} else {
			returnedError = mergeSealCleanup(returnedError, ctx.Err())
		}
	}()
	err = sealRepin(input, state.identity)
	if err == nil {
		err = consume(input)
	}
	if err == nil {
		err = sealRepin(input, state.identity)
	}
	return sealErrorOrNil(ctx, err)
}
func sealErrorOrNil(ctx context.Context, err error) error {
	if err == nil {
		return ctx.Err()
	}
	return sealError(ctx, err)
}
func (file SealedFile) Ref() ObjectRef {
	if file.get == nil {
		return ObjectRef{}
	}
	state := file.get()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed {
		return ObjectRef{}
	}
	return state.ref
}
func (file SealedFile) Close() error {
	if file.get == nil {
		return nil
	}
	state := file.get()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.closed = true
		for state.active != 0 {
			changed := state.changed
			state.mutex.Unlock()
			<-changed
			state.mutex.Lock()
		}
		state.mutex.Unlock()
		state.closeError = state.engine.removePrivate(state.name, state.charge, nil)
		if err := releaseSealDirectory(state.directory); err != nil {
			state.closeError = mergeSealCleanup(state.closeError, err)
		}
	})
	return state.closeError
}

type sealOffsetReader struct {
	ctx    context.Context
	file   *os.File
	offset int64
}

func (reader *sealOffsetReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	count, err := reader.file.ReadAt(buffer, reader.offset)
	reader.offset += int64(count)
	return count, err
}

var sealOrphans struct {
	sync.Mutex
	roots []*sealDirectory
}

var sealUnresolved struct {
	sync.Mutex
	files   []*os.File
	charges []*sealCharge
}

func (engine *sealEngine) closeFile(file *os.File) error {
	err := engine.ops.close(file)
	if err != nil {
		engine.mutex.Lock()
		engine.uncertain = true
		engine.mutex.Unlock()
		engine.directory.mutex.Lock()
		engine.directory.refs++
		engine.directory.mutex.Unlock()
		retainSealDirectory(engine.directory)
		sealUnresolved.Lock()
		sealUnresolved.files = append(sealUnresolved.files, file)
		sealUnresolved.Unlock()
	}
	return err
}
func mergeSealCleanup(previous, next error) error {
	if errors.Is(previous, ErrSealUncertain) || errors.Is(next, ErrSealUncertain) {
		return ErrSealUncertain
	}
	if errors.Is(previous, ErrSealCleanup) || errors.Is(next, ErrSealCleanup) {
		return ErrSealCleanup
	}
	if next != nil {
		return next
	}
	return previous
}
func retainSealCharge(charge *sealCharge) {
	if charge == nil {
		return
	}
	sealUnresolved.Lock()
	sealUnresolved.charges = append(sealUnresolved.charges, charge)
	sealUnresolved.Unlock()
}

func (engine *sealEngine) finishCharge(charge *sealCharge, cleanupError error) {
	if cleanupError == nil {
		charge.release()
		return
	}
	engine.mutex.Lock()
	engine.uncertain = true
	engine.mutex.Unlock()
	engine.directory.mutex.Lock()
	engine.directory.refs++
	engine.directory.mutex.Unlock()
	retainSealDirectory(engine.directory)
	retainSealCharge(charge)
}

func (engine *sealEngine) removePrivate(name string, charge *sealCharge, closeError error) error {
	var cleanupError error
	if sealUnlink(engine.directory, name) != nil {
		cleanupError = ErrSealCleanup
	}
	if engine.ops.sync(engine.directory.root) != nil {
		cleanupError = ErrSealCleanup
	}
	if closeError != nil {
		cleanupError = ErrSealUncertain
	}
	engine.finishCharge(charge, cleanupError)
	return cleanupError
}

func retainSealDirectory(directory *sealDirectory) {
	sealOrphans.Lock()
	sealOrphans.roots = append(sealOrphans.roots, directory)
	sealOrphans.Unlock()
}
func releaseSealDirectory(directory *sealDirectory) error {
	directory.mutex.Lock()
	defer directory.mutex.Unlock()
	directory.refs--
	if directory.refs != 0 {
		return nil
	}
	err := sealCloseDirectory(directory)
	if err != nil {
		directory.failed = true
		retainSealDirectory(directory)
	}
	return err
}
