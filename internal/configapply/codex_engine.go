package configapply

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type CodexEngine struct{ codexOpaque }

type codexEngineState struct {
	mutex      sync.Mutex
	closing    atomic.Bool
	life       context.Context
	cancel     context.CancelFunc
	home       *nativeDirectory
	state      *nativeDirectory
	vault      *nativeDirectory
	keys       codexKeys
	anchor     nativeImage
	inventory  codexInventory
	sequence   uint64
	now        func() time.Time
	fault      func(string) error
	closeOnce  sync.Once
	closeError error
	guard      *codexGuard
	workers    sync.WaitGroup
}

func OpenCodex(ctx context.Context, home, stateRoot string, existingKey []byte) (_ *CodexEngine, result error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(existingKey) != 32 || home == stateRoot || !filepath.IsAbs(home) || filepath.Clean(home) != home || !filepath.IsAbs(stateRoot) || filepath.Clean(stateRoot) != stateRoot || strings.HasPrefix(stateRoot+"/", home+"/.codex/") {
		return nil, ErrCodexInvalid
	}
	if codexPlatform() != nil {
		return nil, ErrCodexUnsupported
	}
	keys, err := deriveCodexKeys(existingKey)
	if err != nil {
		return nil, ErrCodexInvalid
	}
	life, cancel := context.WithCancel(context.Background())
	state := &codexEngineState{keys: keys, life: life, cancel: cancel, now: time.Now}
	engine := &CodexEngine{value: new(any(state))}
	defer func() {
		if result != nil {
			_ = engine.Close()
			result = codexSafeError(result)
		}
	}()
	state.home, err = nativeOpenDirectory(home, false)
	if err != nil {
		return nil, err
	}
	state.state, err = nativeOpenDirectory(stateRoot, true)
	if err != nil {
		return nil, err
	}
	if sameDirectory(state.home.identity, state.state.identity) {
		return nil, ErrCodexInvalid
	}
	guard, err := acquireCodexGuard(ctx, state.state)
	if err != nil {
		return nil, err
	}
	defer func() { result = errors.Join(result, guard.close()) }()
	if err := state.state.validateSessionFiles(); err != nil {
		return nil, err
	}
	if err := state.openCodexVault(); err != nil {
		return nil, err
	}
	if err := guard.check(); err != nil {
		return nil, err
	}
	return engine, nil
}

func (engine *CodexEngine) private() *codexEngineState {
	if engine == nil {
		return nil
	}
	return engine.get[codexEngineState]()
}

func (state *codexEngineState) withLock(ctx context.Context, callback func(context.Context) error) (result error) {
	if state == nil || state.closing.Load() {
		return ErrCodexClosed
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(state.life, cancel)
	defer func() { stop(); cancel(); result = codexSafeError(result) }()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closing.Load() {
		return ErrCodexClosed
	}
	if err := operation.Err(); err != nil {
		return err
	}
	guard, err := acquireCodexGuard(operation, state.state)
	if err != nil {
		return err
	}
	state.guard = guard
	defer func() { state.guard = nil }()
	defer func() { result = errors.Join(result, guard.close()) }()
	if state.home.check() != nil || state.vault.check() != nil || state.state.validateSessionFiles() != nil {
		return ErrCodexAttention
	}
	if err := state.loadCodexInventory(); err != nil {
		return err
	}
	if err := state.checkCodexContext(operation); err != nil {
		return err
	}
	if err := callback(operation); err != nil {
		return err
	}
	return guard.check()
}

func (engine *CodexEngine) Close() error {
	state := engine.private()
	if state == nil {
		return nil
	}
	state.closing.Store(true)
	state.cancel()
	state.closeOnce.Do(func() {
		state.mutex.Lock()
		state.mutex.Unlock()
		state.workers.Wait()
		state.mutex.Lock()
		defer state.mutex.Unlock()
		for _, directory := range []*nativeDirectory{state.vault, state.state, state.home} {
			if directory != nil {
				state.closeError = errors.Join(state.closeError, directory.close())
			}
		}
		state.keys.destroy()
		state.inventory = codexInventory{}
		state.closeError = codexSafeError(state.closeError)
	})
	return state.closeError
}

func (engine *CodexEngine) Destroy() {
	if engine != nil && engine.Close() == nil {
		engine.codexOpaque.Destroy()
	}
}

func codexSelectionKey(origin, owner, profile string) string {
	return origin + "\x00" + owner + "\x00" + profile
}

func (engine *CodexEngine) CurrentCodexSelection(ctx context.Context, origin, owner, profile string) (CodexSelectionState, error) {
	state := engine.private()
	if len(origin) > 512 || !strings.HasPrefix(origin, "https://") || strings.ContainsAny(origin, "\x00\r\n\t ") || !codexIdentifier(owner) || !codexIdentifier(profile) {
		return CodexSelectionState{}, ErrCodexInvalid
	}
	var result CodexSelectionState
	err := state.withLock(ctx, func(ctx context.Context) error {
		key := codexSelectionKey(origin, owner, profile)
		record, found := state.inventory.Selections[key]
		if !found {
			record = codexSelectionRecord{Origin: origin, Owner: owner, Profile: profile, Device: state.inventory.Device}
		}
		result = CodexSelectionState{value: new(any(&codexCurrent{engine: state, record: record, anchor: state.anchor}))}
		return nil
	})
	return result, err
}

func (state *codexEngineState) checkpoint(label string) error {
	if state.fault != nil {
		if err := state.fault(label); err != nil {
			return ErrCodexUnknown
		}
	}
	return nil
}

func (state *codexEngineState) checkCodexContext(ctx context.Context) error {
	if state.closing.Load() {
		return ErrCodexClosed
	}
	return ctx.Err()
}

func (state *codexEngineState) checkpointContext(ctx context.Context, label string) error {
	if err := state.checkpoint(label); err != nil {
		return err
	}
	return state.checkCodexContext(ctx)
}

func codexRandomID() string {
	var value [16]byte
	_, _ = rand.Read(value[:])
	const digits = "0123456789abcdef"
	encoded := make([]byte, 32)
	for index, octet := range value {
		encoded[index*2], encoded[index*2+1] = digits[octet>>4], digits[octet&15]
	}
	return string(encoded)
}

func codexClearBodies(bodies *[3][]byte) {
	for index := range bodies {
		clear(bodies[index])
		bodies[index] = nil
	}
}

func codexCopyBodies(bodies [3][]byte) [3][]byte {
	var result [3][]byte
	for index := range bodies {
		result[index] = bytes.Clone(bodies[index])
	}
	return result
}
