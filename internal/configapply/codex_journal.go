package configapply

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"slices"
	"time"

	"github.com/ca-x/cognate/internal/codexconfig"
)

const codexMetadataLimit = 64 << 10
const codexBundleLimit = 1 << 20
const codexRoleLimit = 256 << 10
const codexOperationLimit = 32
const codexEntryLimit = 256
const codexVaultLimit = 96 << 20
const codexAnchorName = "codex-local-anchor.v1"
const codexNextAnchorName = "codex-local-anchor-next.v1"

type codexKeys struct{ anchor, backup, candidate []byte }

func deriveCodexKeys(key []byte) (codexKeys, error) {
	var result codexKeys
	var err error
	for index, domain := range []string{"codex-d-anchor-v1", "codex-d-backup-v1", "codex-d-candidate-v1"} {
		derived, failure := hkdf.Key(sha256.New, key, nil, domain, 32)
		if failure != nil {
			err = failure
			break
		}
		switch index {
		case 0:
			result.anchor = derived
		case 1:
			result.backup = derived
		case 2:
			result.candidate = derived
		}
	}
	if err != nil {
		result.destroy()
	}
	return result, err
}

func (keys *codexKeys) destroy() {
	clear(keys.anchor)
	clear(keys.backup)
	clear(keys.candidate)
	*keys = codexKeys{}
}

type codexOperation struct {
	ID                   string
	Owner                string
	Origin               string
	Profile              string
	Asset                string
	Source               string
	Binding              [32]byte
	Target               nativeIdentity
	Before               [3]nativeImage
	After                [3]nativeImage
	Write                [3]bool
	Stages               [3]nativeImage
	StageNames           [3]string
	Backup               nativeImage
	Candidate            nativeImage
	Base                 codexSelectionRecord
	State                CodexLocalState
	Expires              int64
	Restoring            bool
	VaultReservation     nativeImage
	TargetReservation    nativeImage
	ReservationsReleased bool
	ProfileIdentity      CodexProfileIdentity
	ManagedModels        []string
	ManagedModelsVersion uint32 `json:",omitzero"`
	Observation          *codexLaunchDescriptor
	LifecycleVersion     uint32                    `json:",omitzero"`
	Authorization        *codexAuthorizationRecord `json:",omitzero"`
}

func (state *codexEngineState) validateCodexOperationObservation(ctx context.Context, operation *codexOperation) error {
	_, err := state.codexOperationObservationExpiry(ctx, operation)
	return err
}

func (state *codexEngineState) codexOperationObservationExpiry(ctx context.Context, operation *codexOperation) (time.Time, error) {
	if operation == nil {
		return time.Time{}, ErrCodexInvalid
	}
	if operation.Observation == nil {
		if operation.ProfileIdentity.ID != "" {
			return time.Time{}, ErrCodexConflict
		}
		return state.now().Add(120 * time.Second), nil
	}
	if err := state.checkCodexContext(ctx); err != nil {
		return time.Time{}, err
	}
	if codexDescriptorShape(operation.Observation) != nil || operation.ProfileIdentity != codexNative0166Identity() || operation.Observation.Auth != operation.Before[1] || operation.Before[1] != operation.After[1] {
		return time.Time{}, ErrCodexConflict
	}
	actual, _, target, expires, err := state.captureCodexLaunch(ctx, operation.Observation.Launch)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, ErrCodexClosed) {
			return time.Time{}, err
		}
		return time.Time{}, ErrCodexConflict
	}
	if !reflect.DeepEqual(actual, *operation.Observation) || !sameDirectory(target, operation.Target) || !state.now().Before(expires) {
		return time.Time{}, ErrCodexConflict
	}
	return minTime(state.now().Add(120*time.Second), expires), nil
}

func codexDescriptorShape(descriptor *codexLaunchDescriptor) error {
	if descriptor == nil || descriptor.Version != 1 || !descriptor.Executable.Present || descriptor.Executable.SHA256 == [32]byte{} || descriptor.Directory.Inode == 0 || descriptor.Layers == [32]byte{} || len(descriptor.Launch.Executable) > 4096 || len(descriptor.Launch.Directory) > 4096 || !filepath.IsAbs(descriptor.Launch.Executable) || filepath.Clean(descriptor.Launch.Executable) != descriptor.Launch.Executable || !filepath.IsAbs(descriptor.Launch.Directory) || filepath.Clean(descriptor.Launch.Directory) != descriptor.Launch.Directory || !slices.Equal(descriptor.Launch.Arguments, []string{"app-server"}) || len(descriptor.Launch.Environment) != 4 || len(descriptor.Sources) == 0 || len(descriptor.Sources) > 64 {
		return ErrCodexAttention
	}
	for _, source := range descriptor.Sources {
		if len(source.Path) > 4096 || !filepath.IsAbs(source.Path) || filepath.Clean(source.Path) != source.Path || source.Parent.Inode == 0 || source.MissingAt < 0 || source.MissingAt >= 64 {
			return ErrCodexAttention
		}
	}
	raw, err := json.Marshal(descriptor)
	defer clear(raw)
	if err != nil || len(raw) > 32<<10 {
		return ErrCodexCapacity
	}
	expected := codexconfig.Native0166ExecutableSHA256
	if hex.EncodeToString(descriptor.Executable.SHA256[:]) != expected {
		return ErrCodexAttention
	}
	return nil
}

type codexInventory struct {
	Version    int
	Sequence   uint64
	Device     string
	Selections map[string]codexSelectionRecord
	Operations map[string]*codexOperation
}

type codexBundle struct {
	ID     string
	Bodies [3][]byte
}

type codexEnvelope struct {
	Version    int
	Nonce      []byte
	Ciphertext []byte
}

type codexAAD struct {
	Domain    string
	Home      nativeIdentity
	State     nativeIdentity
	Vault     nativeIdentity
	Operation string
	Owner     string
	Origin    string
	Profile   string
	Binding   [32]byte
	Target    nativeIdentity
}

func codexRootID(identity nativeIdentity) nativeIdentity {
	identity.Links = 0
	identity.Size = 0
	return identity
}

func (state *codexEngineState) aad(domain string, operation *codexOperation) ([]byte, error) {
	context := codexAAD{Domain: domain, Home: codexRootID(state.home.identity), State: codexRootID(state.state.identity), Vault: codexRootID(state.vault.identity)}
	if operation != nil {
		context.Operation, context.Owner, context.Origin, context.Profile, context.Binding, context.Target = operation.ID, operation.Owner, operation.Origin, operation.Profile, operation.Binding, codexRootID(operation.Target)
	}
	return json.Marshal(context)
}

func codexAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrCodexAttention
	}
	return cipher.NewGCM(block)
}

func (state *codexEngineState) seal(domain string, operation *codexOperation, value any, limit int) ([]byte, error) {
	plain, err := json.Marshal(value)
	if err != nil || len(plain) > limit {
		clear(plain)
		return nil, ErrCodexCapacity
	}
	defer clear(plain)
	key := state.keys.anchor
	if domain == "backup" {
		key = state.keys.backup
	} else if domain == "candidate" {
		key = state.keys.candidate
	}
	aead, err := codexAEAD(key)
	if err != nil {
		return nil, err
	}
	aad, err := state.aad(domain, operation)
	if err != nil {
		return nil, ErrCodexAttention
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, ErrCodexAttention
	}
	return json.Marshal(codexEnvelope{Version: 1, Nonce: nonce, Ciphertext: aead.Seal(nil, nonce, plain, aad)})
}

func (state *codexEngineState) unseal(domain string, operation *codexOperation, body []byte, destination any, limit int) error {
	if len(body) > 2*limit+1024 {
		return ErrCodexCapacity
	}
	var envelope codexEnvelope
	if json.Unmarshal(body, &envelope, json.RejectUnknownMembers(true)) != nil || envelope.Version != 1 {
		return ErrCodexAttention
	}
	defer clear(envelope.Ciphertext)
	key := state.keys.anchor
	if domain == "backup" {
		key = state.keys.backup
	} else if domain == "candidate" {
		key = state.keys.candidate
	}
	aead, err := codexAEAD(key)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() || len(envelope.Ciphertext) > limit+aead.Overhead() {
		return ErrCodexAttention
	}
	aad, err := state.aad(domain, operation)
	if err != nil {
		return ErrCodexAttention
	}
	plain, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, aad)
	if err != nil {
		return ErrCodexAttention
	}
	defer clear(plain)
	if json.Unmarshal(plain, destination, json.RejectUnknownMembers(true)) != nil {
		return ErrCodexAttention
	}
	return nil
}

func (state *codexEngineState) openCodexVault() error {
	anchor, body, err := state.state.read(codexAnchorName, codexMetadataLimit*2+1024)
	clear(body)
	if err != nil {
		return err
	}
	state.vault, err = state.state.child("codex-local-v1", false)
	if errors.Is(err, ErrTargetAbsent) {
		if anchor.Present {
			return ErrCodexAttention
		}
		state.vault, err = state.state.child("codex-local-v1", true)
	}
	if err != nil {
		return err
	}
	if !anchor.Present {
		entries, err := codexDirectoryEntries(state.vault)
		if err != nil || len(entries) != 0 {
			return ErrCodexAttention
		}
		next, data, err := state.state.read(codexNextAnchorName, codexMetadataLimit*2+1024)
		clear(data)
		if err != nil {
			return ErrCodexAttention
		}
		if next.Present {
			return state.loadCodexInventory()
		}
		state.inventory = codexInventory{Version: 1, Device: codexRandomID(), Operations: make(map[string]*codexOperation), Selections: make(map[string]codexSelectionRecord)}
		return state.saveCodexInventory()
	}
	return state.loadCodexInventory()
}

func (state *codexEngineState) loadCodexInventory() error {
	image, data, err := state.state.read(codexAnchorName, codexMetadataLimit*2+1024)
	if err != nil {
		return err
	}
	defer clear(data)
	next, body, err := state.state.read(codexNextAnchorName, codexMetadataLimit*2+1024)
	if err != nil {
		clear(body)
		return err
	}
	defer clear(body)
	if !image.Present {
		if state.anchor.Present || state.sequence != 0 || !next.Present || next.Identity.Mode&0o077 != 0 {
			return ErrCodexAttention
		}
		var initial codexInventory
		if state.unseal("anchor", nil, body, &initial, codexMetadataLimit) != nil || initial.Sequence != 1 || len(initial.Operations) != 0 || len(initial.Selections) != 0 {
			return ErrCodexAttention
		}
		if err := state.validateCodexInventory(&initial); err != nil {
			return err
		}
		if err := state.state.rename(codexNextAnchorName, state.state, codexAnchorName); err != nil {
			return err
		}
		if err := state.state.syncImage(codexAnchorName, next); err != nil {
			return err
		}
		if err := state.state.sync(); err != nil {
			return err
		}
		state.inventory, state.anchor, state.sequence = initial, next, initial.Sequence
		return nil
	}
	if image.Identity.Mode&0o077 != 0 {
		return ErrCodexAttention
	}
	var inventory codexInventory
	if err := state.unseal("anchor", nil, data, &inventory, codexMetadataLimit); err != nil {
		return err
	}
	if codexInventoryShape(&inventory) != nil || inventory.Sequence < state.sequence || inventory.Sequence == state.sequence && state.anchor.Present && image != state.anchor {
		return ErrCodexAttention
	}
	if next.Present {
		var sibling codexInventory
		if next.Identity.Mode&0o077 != 0 || state.unseal("anchor", nil, body, &sibling, codexMetadataLimit) != nil || codexInventoryShape(&sibling) != nil || sibling.Device != inventory.Device {
			return ErrCodexAttention
		}
		if inventory.Sequence < math.MaxUint64 && sibling.Sequence == inventory.Sequence+1 {
			if err := state.validateCodexInventory(&sibling); err != nil {
				return err
			}
			if err := state.state.exchange(codexNextAnchorName, codexAnchorName); err != nil {
				return err
			}
			displaced, data, err := state.state.read(codexNextAnchorName, codexMetadataLimit*2+1024)
			clear(data)
			if err != nil || displaced != image {
				return ErrCodexUnknown
			}
			image, next, inventory = next, image, sibling
			if err := state.checkpoint("anchor-recovery-exchanged"); err != nil {
				return err
			}
		} else if sibling.Sequence == math.MaxUint64 || sibling.Sequence+1 != inventory.Sequence {
			return ErrCodexAttention
		}
	}
	if err := state.validateCodexInventory(&inventory); err != nil {
		return err
	}
	if next.Present {
		if err := state.state.syncImage(codexAnchorName, image); err != nil {
			return err
		}
		if err := state.state.sync(); err != nil {
			return err
		}
		if err := state.state.removeOwn(codexNextAnchorName, next); err != nil {
			return err
		}
	}
	state.inventory, state.anchor, state.sequence = inventory, image, inventory.Sequence
	return nil
}

func codexInventoryShape(inventory *codexInventory) error {
	if inventory.Version != 1 || inventory.Sequence == 0 || !codexOperationID(inventory.Device) || len(inventory.Operations) > codexOperationLimit || len(inventory.Selections) > codexOperationLimit || inventory.Operations == nil || inventory.Selections == nil {
		return ErrCodexAttention
	}
	for id, operation := range inventory.Operations {
		if operation == nil || !codexOperationID(id) || operation.ID != id || !codexIdentifier(operation.Owner) || !codexIdentifier(operation.Profile) || operation.Write[1] || operation.State < CodexPreviewed || operation.State > CodexRetiredUnpublished || operation.Backup.Present != operation.Candidate.Present {
			return ErrCodexAttention
		}
		if err := codexLifecycleShape(operation); err != nil {
			return err
		}
		if operation.ManagedModelsVersion == 1 && operation.Restoring {
			source := inventory.Operations[operation.Source]
			if source == nil || source.LifecycleVersion != 1 || source.ManagedModelsVersion != 1 || source.Binding != operation.Binding || source.Owner != operation.Owner || source.Origin != operation.Origin || source.Profile != operation.Profile || source.ProfileIdentity != operation.ProfileIdentity || !slices.Equal(source.ManagedModels, operation.ManagedModels) {
				return ErrCodexAttention
			}
		}
		if operation.Observation != nil {
			if codexDescriptorShape(operation.Observation) != nil || operation.ProfileIdentity != codexNative0166Identity() || operation.Observation.Auth != operation.Before[1] || operation.Before[1] != operation.After[1] {
				return ErrCodexAttention
			}
		} else if operation.ProfileIdentity.ID != "" {
			return ErrCodexAttention
		}
	}
	for key, selection := range inventory.Selections {
		if key != codexSelectionKey(selection.Origin, selection.Owner, selection.Profile) || selection.Device != inventory.Device || selection.Revision == 0 || selection.Confirmation == 0 {
			return ErrCodexAttention
		}
	}
	return nil
}

func (state *codexEngineState) validateCodexInventory(inventory *codexInventory) error {
	if err := codexInventoryShape(inventory); err != nil {
		return err
	}
	entries, err := codexDirectoryEntries(state.vault)
	if err != nil || len(entries) > codexEntryLimit {
		return ErrCodexCapacity
	}
	expected := make(map[string]nativeImage)
	optional := make(map[string]bool)
	for id, operation := range inventory.Operations {
		if operation.Backup.Present {
			expected[id+".backup"] = operation.Backup
			expected[id+".candidate"] = operation.Candidate
		}
		if operation.VaultReservation.Present {
			expected[id+".reserve"] = operation.VaultReservation
			optional[id+".reserve"] = operation.ReservationsReleased
		}
	}
	found := make(map[string]bool)
	total := int64(0)
	for _, name := range entries {
		wanted, exists := expected[name]
		if !exists {
			return ErrCodexAttention
		}
		actual, body, err := state.vault.read(name, 4<<20)
		clear(body)
		if err != nil || !actual.Present || actual != wanted || actual.Identity.Mode&0o077 != 0 {
			return ErrCodexAttention
		}
		found[name] = true
		total += actual.Identity.Size
		if total > codexVaultLimit {
			return ErrCodexCapacity
		}
	}
	for name := range expected {
		if !found[name] && !optional[name] {
			return ErrCodexAttention
		}
	}
	return nil
}

func (state *codexEngineState) saveCodexInventory() error {
	if state.inventory.Sequence == math.MaxUint64 || len(state.inventory.Operations) > codexOperationLimit || len(state.inventory.Selections) > codexOperationLimit {
		return ErrCodexCapacity
	}
	previous := state.inventory.Sequence
	state.inventory.Sequence++
	encoded, err := state.seal("anchor", nil, state.inventory, codexMetadataLimit)
	if err != nil {
		state.inventory.Sequence = previous
		return err
	}
	defer clear(encoded)
	actual, data, err := state.state.read(codexAnchorName, codexMetadataLimit*2+1024)
	clear(data)
	if err != nil || actual != state.anchor {
		return ErrCodexConflict
	}
	stage, err := state.state.write(codexNextAnchorName, encoded)
	if err != nil {
		return err
	}
	if err := state.checkpoint("anchor-staged"); err != nil {
		return err
	}
	if actual.Present {
		if err := state.state.exchange(codexNextAnchorName, codexAnchorName); err != nil {
			return err
		}
		displaced, data, err := state.state.read(codexNextAnchorName, codexMetadataLimit*2+1024)
		clear(data)
		if err != nil || displaced != actual {
			return ErrCodexUnknown
		}
	} else if err := state.state.rename(codexNextAnchorName, state.state, codexAnchorName); err != nil {
		return err
	}
	if err := state.checkpoint("anchor-exchanged"); err != nil {
		return err
	}
	if err := state.state.syncImage(codexAnchorName, stage); err != nil {
		return err
	}
	if err := state.state.sync(); err != nil {
		return err
	}
	state.anchor, state.sequence = stage, state.inventory.Sequence
	if err := state.checkpoint("anchor-durable"); err != nil {
		return err
	}
	if actual.Present {
		if err := state.state.removeOwn(codexNextAnchorName, actual); err != nil {
			return err
		}
	}
	return nil
}

func (state *codexEngineState) writeCodexBundle(domain string, operation *codexOperation, bodies [3][]byte) (nativeImage, error) {
	for _, body := range bodies {
		if len(body) > codexRoleLimit {
			return nativeImage{}, ErrCodexCapacity
		}
	}
	encoded, err := state.seal(domain, operation, codexBundle{ID: operation.ID, Bodies: bodies}, codexBundleLimit)
	if err != nil {
		return nativeImage{}, err
	}
	defer clear(encoded)
	return state.vault.write(operation.ID+"."+domain, encoded)
}

func (state *codexEngineState) readCodexBundle(domain string, operation *codexOperation) ([3][]byte, error) {
	expected := operation.Backup
	if domain == "candidate" {
		expected = operation.Candidate
	}
	image, encoded, err := state.vault.read(operation.ID+"."+domain, codexBundleLimit*2+1024)
	defer clear(encoded)
	if err != nil || !image.Present || image != expected {
		return [3][]byte{}, ErrCodexAttention
	}
	var bundle codexBundle
	if err := state.unseal(domain, operation, encoded, &bundle, codexBundleLimit); err != nil {
		return [3][]byte{}, err
	}
	if bundle.ID != operation.ID {
		codexClearBodies(&bundle.Bodies)
		return [3][]byte{}, ErrCodexAttention
	}
	for _, body := range bundle.Bodies {
		if len(body) > codexRoleLimit {
			codexClearBodies(&bundle.Bodies)
			return [3][]byte{}, ErrCodexCapacity
		}
	}
	return bundle.Bodies, nil
}
