package configapply

import (
	"context"
	"crypto/hkdf"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	ErrGeminiInvalid     = errors.New("invalid Gemini native operation")
	ErrGeminiUnsupported = errors.New("unsupported Gemini native profile or input")
	ErrGeminiConflict    = errors.New("Gemini native configuration conflict")
	ErrGeminiExpired     = errors.New("Gemini native review expired")
	ErrGeminiUnknown     = errors.New("Gemini native operation outcome unknown")
	ErrGeminiAttention   = errors.New("Gemini native operation needs attention")
	ErrGeminiCapacity    = errors.New("Gemini native capacity exhausted")
	ErrGeminiClosed      = errors.New("Gemini native engine closed")
	ErrGeminiDenied      = errors.New("Gemini native approval required")
)

type geminiOpaque struct{ value *any }

func (value geminiOpaque) get[Value any]() *Value {
	if value.value == nil {
		return nil
	}
	result, _ := (*value.value).(*Value)
	return result
}
func (value geminiOpaque) Destroy() {
	if value.value != nil {
		if resource, ok := (*value.value).(interface{ destroy() }); ok {
			resource.destroy()
		}
		*value.value = nil
	}
}
func (geminiOpaque) String() string   { return "[private Gemini native]" }
func (geminiOpaque) GoString() string { return "[private Gemini native]" }
func (geminiOpaque) Format(writer fmt.State, _ rune) {
	_, _ = io.WriteString(writer, "[private Gemini native]")
}
func (geminiOpaque) MarshalJSON() ([]byte, error) { return []byte(`"[private Gemini native]"`), nil }
func (geminiOpaque) MarshalText() ([]byte, error) { return []byte("[private Gemini native]"), nil }
func (geminiOpaque) LogValue() slog.Value         { return slog.StringValue("[private Gemini native]") }

type GeminiEngine struct{ geminiOpaque }
type GeminiSelectionState struct{ geminiOpaque }
type GeminiObservation struct{ geminiOpaque }
type GeminiPlan struct{ geminiOpaque }
type GeminiRecoveryPlan struct{ geminiOpaque }
type GeminiLaunch struct{ NodeExecutable, PackageRoot, Directory string }
type GeminiLocalState uint8

const (
	GeminiPreviewed GeminiLocalState = iota + 1
	GeminiAuthorizeAttempted
	GeminiAuthorizedNoSecret
	GeminiAuthorizedGrant
	GeminiConsumeAttempted
	GeminiCandidatesDurable
	GeminiPublishingEnv
	GeminiPublishingSettings
	GeminiCompleted
	GeminiRemoteOutcomeUnknown
	GeminiReconcileRequired
	GeminiNeedsAttention
	GeminiRestorePrepared
	GeminiRestoring
	GeminiRestored
	GeminiAbandonedBeforeWrite
	GeminiRetiredUnpublished
)

type GeminiResult struct {
	OperationID                             string
	State                                   GeminiLocalState
	RemoteOutcomeUnknown, Consumed, Applied bool
}
type geminiKeys struct{ anchor, backup, candidate []byte }

func (keys *geminiKeys) destroy() {
	clear(keys.anchor)
	clear(keys.backup)
	clear(keys.candidate)
	*keys = geminiKeys{}
}

type geminiEngineState struct {
	mutex              sync.Mutex
	closing            atomic.Bool
	life               context.Context
	cancel             context.CancelFunc
	home, state, vault *nativeDirectory
	guard              *codexGuard
	keys               geminiKeys
	anchor             nativeImage
	sequence           uint64
	inventory          geminiInventory
	now                func() time.Time
	fault              func(string) error
	closeOnce          sync.Once
	closeError         error
}
type geminiSelectionRecord struct {
	Origin, Owner, Profile, Device, Asset, Operation string
	Revision, Confirmation                           uint64
}
type geminiCurrent struct {
	engine *geminiEngineState
	record geminiSelectionRecord
}

func (value GeminiSelectionState) Revision() uint64 {
	if current := value.get[geminiCurrent](); current != nil {
		return current.record.Revision
	}
	return 0
}
func (value GeminiSelectionState) ConfirmationRevision() uint64 {
	if current := value.get[geminiCurrent](); current != nil {
		return current.record.Confirmation
	}
	return 0
}

func geminiSafeError(err error) error {
	if err == nil {
		return nil
	}
	for _, known := range []error{context.Canceled, context.DeadlineExceeded, ErrGeminiInvalid, ErrGeminiUnsupported, ErrGeminiConflict, ErrGeminiExpired, ErrGeminiUnknown, ErrGeminiAttention, ErrGeminiCapacity, ErrGeminiClosed, ErrGeminiDenied} {
		if errors.Is(err, known) {
			return known
		}
	}
	switch {
	case errors.Is(err, ErrCodexConflict), errors.Is(err, ErrConflict):
		return ErrGeminiConflict
	case errors.Is(err, ErrCodexUnknown), errors.Is(err, ErrOutcomeUnknown):
		return ErrGeminiUnknown
	case errors.Is(err, ErrCodexCapacity), errors.Is(err, ErrCapacity):
		return ErrGeminiCapacity
	default:
		return ErrGeminiAttention
	}
}
func (engine *GeminiEngine) private() *geminiEngineState {
	if engine == nil {
		return nil
	}
	return engine.get[geminiEngineState]()
}
func OpenGemini(ctx context.Context, home, stateRoot string, key []byte) (engine *GeminiEngine, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if _, err := QualifiedGeminiNativeProfile(); err != nil {
		return nil, err
	}
	if len(key) != 32 || !filepath.IsAbs(home) || filepath.Clean(home) != home || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot || home == stateRoot || strings.HasPrefix(stateRoot+"/", home+"/.gemini/") {
		return nil, ErrGeminiInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	state := &geminiEngineState{life: life, cancel: cancel, now: time.Now}
	engine = &GeminiEngine{geminiOpaque{new(any(state))}}
	defer func() {
		if result != nil {
			_ = engine.Close()
			engine = nil
			result = geminiSafeError(result)
		}
	}()
	var err error
	for index, domain := range []string{"gemini-local-anchor-v1", "gemini-local-backup-v1", "gemini-local-candidate-v1"} {
		derived, err := hkdf.Key(sha256.New, key, nil, domain, 32)
		if err != nil {
			return engine, ErrGeminiInvalid
		}
		switch index {
		case 0:
			state.keys.anchor = derived
		case 1:
			state.keys.backup = derived
		case 2:
			state.keys.candidate = derived
		}
	}
	state.home, err = nativeOpenDirectory(home, false)
	if err != nil {
		return engine, err
	}
	target, err := state.home.child(".gemini", false)
	if err != nil {
		return engine, ErrGeminiUnsupported
	}
	if target.close() != nil {
		return engine, ErrGeminiUnknown
	}
	state.state, err = nativeOpenDirectory(stateRoot, true)
	if err != nil {
		return engine, err
	}
	if sameDirectory(state.home.identity, state.state.identity) {
		return engine, ErrGeminiInvalid
	}
	guard, err := acquireCodexGuard(ctx, state.state)
	if err != nil {
		return engine, err
	}
	defer func() { result = errors.Join(result, guard.close()) }()
	if err = state.state.validateSessionFiles(); err != nil {
		return engine, err
	}
	if err = state.openGeminiVault(); err != nil {
		return engine, err
	}
	return engine, guard.check()
}
func (state *geminiEngineState) withLock(ctx context.Context, run func(context.Context) error) (result error) {
	if state == nil || state.closing.Load() {
		return ErrGeminiClosed
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(state.life, cancel)
	defer func() { stop(); cancel(); result = geminiSafeError(result) }()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closing.Load() {
		return ErrGeminiClosed
	}
	if err := operation.Err(); err != nil {
		return err
	}
	guard, err := acquireCodexGuard(operation, state.state)
	if err != nil {
		return err
	}
	state.guard = guard
	defer func() { state.guard = nil; result = errors.Join(result, guard.close()) }()
	if state.home.check() != nil || state.state.check() != nil || state.vault.check() != nil || state.state.validateSessionFiles() != nil {
		return ErrGeminiAttention
	}
	if err := state.loadGeminiInventory(); err != nil {
		return err
	}
	if err := run(operation); err != nil {
		return err
	}
	return guard.check()
}
func (engine *GeminiEngine) Close() error {
	state := engine.private()
	if state == nil {
		return nil
	}
	state.closing.Store(true)
	state.cancel()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		defer state.mutex.Unlock()
		for _, directory := range []*nativeDirectory{state.vault, state.state, state.home} {
			if directory != nil {
				state.closeError = errors.Join(state.closeError, directory.close())
			}
		}
		if state.closeError == nil {
			state.keys.destroy()
			state.inventory.clearTickets()
			state.inventory = geminiInventory{}
		}
		state.closeError = geminiSafeError(state.closeError)
	})
	return state.closeError
}
func (engine *GeminiEngine) Destroy() {
	if engine != nil && engine.Close() == nil {
		engine.geminiOpaque.Destroy()
	}
}
func (state *geminiEngineState) check(ctx context.Context) error {
	if state.closing.Load() {
		return ErrGeminiClosed
	}
	return ctx.Err()
}
func (state *geminiEngineState) checkpoint(ctx context.Context, label string) error {
	if state.fault != nil && state.fault(label) != nil {
		return ErrGeminiUnknown
	}
	return state.check(ctx)
}
func geminiSelectionKey(origin, owner, profile string) string {
	return origin + "\x00" + owner + "\x00" + profile
}
func (engine *GeminiEngine) CurrentGeminiSelection(ctx context.Context, origin, owner, profile string) (GeminiSelectionState, error) {
	state := engine.private()
	if !strings.HasPrefix(origin, "https://") || len(origin) > 2048 || strings.ContainsAny(origin, "\x00\r\n\t ") || !codexIdentifier(owner) || !codexIdentifier(profile) {
		return GeminiSelectionState{}, ErrGeminiInvalid
	}
	var result GeminiSelectionState
	err := state.withLock(ctx, func(ctx context.Context) error {
		record, found := state.inventory.Selections[geminiSelectionKey(origin, owner, profile)]
		if !found {
			record = geminiSelectionRecord{Origin: origin, Owner: owner, Profile: profile, Device: state.inventory.Device}
		}
		result = GeminiSelectionState{geminiOpaque{new(any(&geminiCurrent{engine: state, record: record}))}}
		return ctx.Err()
	})
	return result, err
}
func (state *geminiEngineState) validateSelection(base geminiSelectionRecord) error {
	current, exists := state.inventory.Selections[geminiSelectionKey(base.Origin, base.Owner, base.Profile)]
	if !exists {
		current = geminiSelectionRecord{Origin: base.Origin, Owner: base.Owner, Profile: base.Profile, Device: state.inventory.Device}
	}
	if current != base {
		return ErrGeminiConflict
	}
	return nil
}
func (engine *GeminiEngine) CheckGeminiSelection(ctx context.Context, current GeminiSelectionState) error {
	state, selected := engine.private(), current.get[geminiCurrent]()
	if state == nil || selected == nil || selected.engine != state {
		return ErrGeminiInvalid
	}
	return state.withLock(ctx, func(ctx context.Context) error {
		if err := state.validateSelection(selected.record); err != nil {
			return err
		}
		for _, operation := range state.inventory.Operations {
			if !geminiTerminal(operation.State) {
				return ErrGeminiConflict
			}
		}
		return ctx.Err()
	})
}
func (state *geminiEngineState) observePair() (target *nativeDirectory, images [2]nativeImage, bodies [2][]byte, result error) {
	target, result = state.home.child(".gemini", false)
	if result != nil {
		return nil, images, bodies, ErrGeminiUnsupported
	}
	defer func() {
		if result != nil {
			_ = target.close()
			target = nil
			geminiClearBodies(&bodies)
		}
	}()
	total := 0
	for index, name := range geminiRoleNames {
		images[index], bodies[index], result = target.read(name, 256<<10)
		if result != nil {
			return
		}
		total += len(bodies[index])
		if total > 384<<10 {
			result = ErrGeminiCapacity
			return
		}
		if index == 0 && images[index].Present && images[index].Identity.Mode&0o777 != 0o600 {
			result = ErrGeminiUnsupported
			return
		}
	}
	return
}
func geminiClearBodies(bodies *[2][]byte) {
	for index := range bodies {
		clear(bodies[index])
		bodies[index] = nil
	}
}
func geminiTerminal(state GeminiLocalState) bool {
	return state == GeminiCompleted || state == GeminiRestored || state == GeminiAbandonedBeforeWrite || state == GeminiRetiredUnpublished
}
