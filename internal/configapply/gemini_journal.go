package configapply

import (
	"context"
	"crypto/sha256"
	"encoding/json/v2"
	"errors"
	"maps"
	"math"

	"github.com/ca-x/cognate/internal/geminiconfig"
)

const geminiAnchorName = "gemini-local-anchor.v1"
const geminiNextAnchorName = "gemini-local-anchor-next.v1"
const geminiMetadataLimit = 64 << 10
const geminiArtifactLimit = 768 << 10
const geminiVaultLimit = 96 << 20

type geminiOperation struct {
	ID, PlanID, Origin, Owner, Profile, Asset, Source                                  string
	AssetRevision                                                                      int64
	Binding                                                                            [32]byte
	Target                                                                             nativeIdentity
	Before, After, Stages                                                              [2]nativeImage
	Write                                                                              [2]bool
	StageNames                                                                         [2]string
	StageEpoch                                                                         uint8
	RetainedStages                                                                     []string
	FinalModes                                                                         [2]uint32
	Backup, Candidate, VaultReservation, TargetReservation                             nativeImage
	ReservationsReleased                                                               bool
	Base                                                                               geminiSelectionRecord
	State                                                                              GeminiLocalState
	Expires                                                                            int64
	ProfileIdentity                                                                    GeminiProfileIdentity
	Observation                                                                        geminiDescriptor
	Fields                                                                             [6]geminiconfig.FieldFact
	Actions                                                                            [6]geminiconfig.FieldAction
	Managed                                                                            [6]bool
	StatusTicket                                                                       []byte
	Authorized, ConsumeStarted, Consumed, RemoteUnknown, Restoring, PublicationStarted bool
}
type geminiInventory struct {
	Version    uint32
	Sequence   uint64
	Device     string
	Operations map[string]*geminiOperation
	Selections map[string]geminiSelectionRecord
}

func (inventory *geminiInventory) clearTickets() {
	for _, operation := range inventory.Operations {
		if operation != nil {
			clear(operation.StatusTicket)
		}
	}
}

type geminiArtifact struct {
	ID      string
	Binding [32]byte
	Bodies  [2][]byte
}
type geminiAAD struct {
	Domain                            string
	Roots                             [3]nativeIdentity
	ID, Owner, Origin, Profile, Asset string
	Binding                           [32]byte
	Target                            nativeIdentity
}

func (state *geminiEngineState) aad(domain string, operation *geminiOperation) ([]byte, error) {
	value := geminiAAD{Domain: "gemini-local-" + domain + "-v1", Roots: [3]nativeIdentity{codexRootID(state.home.identity), codexRootID(state.state.identity), codexRootID(state.vault.identity)}}
	if operation != nil {
		value.ID, value.Owner, value.Origin, value.Profile, value.Asset = operation.ID, operation.Owner, operation.Origin, operation.Profile, operation.Asset
		value.Binding = operation.Binding
		value.Target = codexRootID(operation.Target)
	}
	return json.Marshal(value)
}
func (state *geminiEngineState) seal(domain string, operation *geminiOperation, value any, limit int) ([]byte, error) {
	key := state.keys.anchor
	if domain == "backup" {
		key = state.keys.backup
	} else if domain == "candidate" {
		key = state.keys.candidate
	}
	aad, err := state.aad(domain, operation)
	if err != nil {
		return nil, ErrGeminiAttention
	}
	return bundleSeal(key, aad, value, limit)
}
func (state *geminiEngineState) unseal(domain string, operation *geminiOperation, body []byte, value any, limit int) error {
	key := state.keys.anchor
	if domain == "backup" {
		key = state.keys.backup
	} else if domain == "candidate" {
		key = state.keys.candidate
	}
	aad, err := state.aad(domain, operation)
	if err != nil {
		return ErrGeminiAttention
	}
	return bundleUnseal(key, aad, body, value, limit)
}
func (state *geminiEngineState) openGeminiVault() error {
	anchor, body, err := state.state.read(geminiAnchorName, 2*geminiMetadataLimit+1024)
	clear(body)
	if err != nil {
		return err
	}
	state.vault, err = state.state.child("gemini-local-v1", false)
	if errors.Is(err, ErrTargetAbsent) {
		if anchor.Present {
			return ErrGeminiAttention
		}
		state.vault, err = state.state.child("gemini-local-v1", true)
	}
	if err != nil {
		return err
	}
	if state.vault.identity.Mode&0o077 != 0 {
		return ErrGeminiAttention
	}
	if !anchor.Present {
		next, body, err := state.state.read(geminiNextAnchorName, 2*geminiMetadataLimit+1024)
		clear(body)
		if err != nil {
			return err
		}
		if next.Present {
			return state.loadGeminiInventory()
		}
		entries, err := codexDirectoryEntries(state.vault)
		if err != nil || len(entries) != 0 {
			return ErrGeminiAttention
		}
		state.inventory = geminiInventory{Version: 1, Device: codexRandomID(), Operations: make(map[string]*geminiOperation), Selections: make(map[string]geminiSelectionRecord)}
		return state.saveGeminiInventory(context.Background())
	}
	return state.loadGeminiInventory()
}
func geminiInventoryShape(inventory *geminiInventory) error {
	if inventory.Version != 1 || inventory.Sequence == 0 || !codexOperationID(inventory.Device) || inventory.Operations == nil || inventory.Selections == nil || len(inventory.Operations) > 32 || len(inventory.Selections) > 256 {
		return ErrGeminiAttention
	}
	for id, operation := range inventory.Operations {
		if operation == nil || id != operation.ID || !codexOperationID(id) || !codexOperationID(operation.PlanID) || !codexIdentifier(operation.Owner) || !codexIdentifier(operation.Profile) || operation.Base.Device != inventory.Device || operation.Base.Owner != operation.Owner || operation.Base.Origin != operation.Origin || operation.Base.Profile != operation.Profile || operation.Binding == [32]byte{} || operation.State < GeminiPreviewed || operation.State > GeminiRetiredUnpublished || operation.Expires <= 0 || operation.ProfileIdentity != geminiNativeIdentity() || !sameDirectory(operation.Target, operation.Observation.Target) {
			return ErrGeminiAttention
		}
		if len(operation.StatusTicket) > 8192 || operation.ConsumeStarted && !operation.Authorized || operation.Consumed && !operation.ConsumeStarted || operation.Candidate.Present && !operation.Backup.Present {
			return ErrGeminiAttention
		}
		if operation.StageEpoch > 4 || len(operation.RetainedStages) > 8 {
			return ErrGeminiAttention
		}
		for index, name := range operation.StageNames {
			if operation.Write[index] && !geminiValidStageName(id, name, index, operation.StageEpoch) {
				return ErrGeminiAttention
			}
		}
		for _, name := range operation.RetainedStages {
			if !geminiValidStageName(id, name, 0, operation.StageEpoch) && !geminiValidStageName(id, name, 1, operation.StageEpoch) {
				return ErrGeminiAttention
			}
		}
	}
	for key, record := range inventory.Selections {
		if key != geminiSelectionKey(record.Origin, record.Owner, record.Profile) || record.Device != inventory.Device || record.Revision == 0 || record.Confirmation == 0 {
			return ErrGeminiAttention
		}
	}
	return nil
}
func (state *geminiEngineState) loadGeminiInventory() error {
	image, body, err := state.state.read(geminiAnchorName, 2*geminiMetadataLimit+1024)
	if err != nil {
		return err
	}
	defer clear(body)
	next, nextBody, err := state.state.read(geminiNextAnchorName, 2*geminiMetadataLimit+1024)
	if err != nil {
		clear(nextBody)
		return err
	}
	defer clear(nextBody)
	var inventory geminiInventory
	if !image.Present {
		if state.anchor.Present || state.sequence != 0 || !next.Present || next.Identity.Mode&0o077 != 0 || state.unseal("anchor", nil, nextBody, &inventory, geminiMetadataLimit) != nil || geminiInventoryShape(&inventory) != nil || inventory.Sequence != 1 || len(inventory.Operations) != 0 || len(inventory.Selections) != 0 {
			return ErrGeminiAttention
		}
		if err := state.state.rename(geminiNextAnchorName, state.state, geminiAnchorName); err != nil {
			return err
		}
		image = next
		next = nativeImage{}
	} else {
		if image.Identity.Mode&0o077 != 0 || state.unseal("anchor", nil, body, &inventory, geminiMetadataLimit) != nil || geminiInventoryShape(&inventory) != nil || inventory.Sequence < state.sequence || inventory.Sequence == state.sequence && state.anchor.Present && image != state.anchor {
			return ErrGeminiAttention
		}
		if next.Present {
			var sibling geminiInventory
			if next.Identity.Mode&0o077 != 0 || state.unseal("anchor", nil, nextBody, &sibling, geminiMetadataLimit) != nil || geminiInventoryShape(&sibling) != nil || sibling.Device != inventory.Device {
				return ErrGeminiAttention
			}
			if inventory.Sequence < math.MaxUint64 && sibling.Sequence == inventory.Sequence+1 {
				if err := state.validateGeminiVault(&sibling); err != nil {
					return err
				}
				if maps.Equal(inventory.Selections, sibling.Selections) {
					if err := state.state.exchange(geminiNextAnchorName, geminiAnchorName); err != nil {
						return err
					}
					displaced, body, err := state.state.read(geminiNextAnchorName, 2*geminiMetadataLimit+1024)
					clear(body)
					if err != nil || displaced != image {
						return ErrGeminiUnknown
					}
					image, next, inventory = next, image, sibling
				}
			} else if sibling.Sequence == math.MaxUint64 || sibling.Sequence+1 != inventory.Sequence {
				return ErrGeminiAttention
			}
		}
	}
	if err := state.validateGeminiVault(&inventory); err != nil {
		return err
	}
	if err := state.state.syncImage(geminiAnchorName, image); err != nil {
		return err
	}
	if err := state.state.sync(); err != nil {
		return err
	}
	if next.Present {
		if err := state.state.removeOwn(geminiNextAnchorName, next); err != nil {
			return err
		}
	}
	state.inventory.clearTickets()
	state.inventory, state.anchor, state.sequence = inventory, image, inventory.Sequence
	return nil
}
func (state *geminiEngineState) validateGeminiVault(inventory *geminiInventory) error {
	entries, err := codexDirectoryEntries(state.vault)
	if err != nil || len(entries) > 256 {
		return ErrGeminiCapacity
	}
	expected := make(map[string]nativeImage)
	planned := make(map[string]*geminiOperation)
	for _, operation := range inventory.Operations {
		for _, domain := range []string{"backup", "candidate", "reserve"} {
			name := operation.ID + "." + domain
			planned[name] = operation
			switch domain {
			case "backup":
				expected[name] = operation.Backup
			case "candidate":
				expected[name] = operation.Candidate
			case "reserve":
				expected[name] = operation.VaultReservation
			}
		}
	}
	var total int64
	found := make(map[string]bool)
	for _, name := range entries {
		operation := planned[name]
		if operation == nil {
			return ErrGeminiAttention
		}
		actual, body, err := state.vault.read(name, 2*geminiArtifactLimit+1024)
		if err != nil {
			clear(body)
			return err
		}
		total += actual.Identity.Size
		if total > geminiVaultLimit || actual.Identity.Mode&0o077 != 0 {
			clear(body)
			return ErrGeminiCapacity
		}
		if wanted := expected[name]; wanted.Present {
			if actual != wanted {
				clear(body)
				return ErrGeminiAttention
			}
		} else {
			domain := "backup"
			if name == operation.ID+".candidate" {
				domain = "candidate"
			}
			if name == operation.ID+".reserve" {
				clear(body)
				found[name] = true
				continue
			}
			var artifact geminiArtifact
			err = state.unseal(domain, operation, body, &artifact, geminiArtifactLimit)
			geminiClearBodies(&artifact.Bodies)
			if err != nil || artifact.ID != operation.ID || artifact.Binding != operation.Binding {
				clear(body)
				return ErrGeminiAttention
			}
		}
		clear(body)
		found[name] = true
	}
	for name, image := range expected {
		operation := planned[name]
		if image.Present && !found[name] && !(operation.ReservationsReleased && name == operation.ID+".reserve") {
			return ErrGeminiAttention
		}
	}
	return nil
}
func (state *geminiEngineState) saveGeminiInventory(ctx context.Context) error {
	return state.saveGeminiInventoryAdmitted(ctx, nil)
}
func (state *geminiEngineState) saveGeminiInventoryAdmitted(ctx context.Context, admit func() error) error {
	if state.inventory.Sequence == math.MaxUint64 || len(state.inventory.Operations) > 32 || len(state.inventory.Selections) > 256 {
		return ErrGeminiCapacity
	}
	previous := state.inventory.Sequence
	state.inventory.Sequence++
	encoded, err := state.seal("anchor", nil, state.inventory, geminiMetadataLimit)
	if err != nil {
		state.inventory.Sequence = previous
		return err
	}
	defer clear(encoded)
	actual, body, err := state.state.read(geminiAnchorName, 2*geminiMetadataLimit+1024)
	clear(body)
	if err != nil || actual != state.anchor {
		return ErrGeminiConflict
	}
	stage, err := state.state.write(geminiNextAnchorName, encoded)
	if err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "anchor-staged"); err != nil {
		return err
	}
	if admit != nil {
		if err := admit(); err != nil {
			return err
		}
	}
	if actual.Present {
		if err := state.state.exchange(geminiNextAnchorName, geminiAnchorName); err != nil {
			return err
		}
		displaced, body, err := state.state.read(geminiNextAnchorName, 2*geminiMetadataLimit+1024)
		clear(body)
		if err != nil || displaced != actual {
			return ErrGeminiUnknown
		}
	} else if err := state.state.rename(geminiNextAnchorName, state.state, geminiAnchorName); err != nil {
		return err
	}
	if err := state.checkpoint(ctx, "anchor-exchanged"); err != nil {
		return err
	}
	if err := state.state.syncImage(geminiAnchorName, stage); err != nil {
		return err
	}
	if err := state.state.sync(); err != nil {
		return err
	}
	state.anchor, state.sequence = stage, state.inventory.Sequence
	if err := state.checkpoint(ctx, "anchor-durable"); err != nil {
		return err
	}
	if actual.Present {
		return state.state.removeOwn(geminiNextAnchorName, actual)
	}
	return nil
}
func (state *geminiEngineState) writeArtifact(domain string, operation *geminiOperation, bodies [2][]byte) (nativeImage, error) {
	total := 0
	for _, body := range bodies {
		if len(body) > 256<<10 {
			return nativeImage{}, ErrGeminiCapacity
		}
		total += len(body)
	}
	if total > 384<<10 {
		return nativeImage{}, ErrGeminiCapacity
	}
	encoded, err := state.seal(domain, operation, geminiArtifact{ID: operation.ID, Binding: operation.Binding, Bodies: bodies}, geminiArtifactLimit)
	if err != nil {
		return nativeImage{}, err
	}
	defer clear(encoded)
	return state.vault.write(operation.ID+"."+domain, encoded)
}
func (state *geminiEngineState) readArtifact(domain string, operation *geminiOperation) ([2][]byte, error) {
	expected := operation.Backup
	if domain == "candidate" {
		expected = operation.Candidate
	}
	image, body, err := state.vault.read(operation.ID+"."+domain, 2*geminiArtifactLimit+1024)
	defer clear(body)
	if err != nil || !expected.Present || image != expected {
		return [2][]byte{}, ErrGeminiAttention
	}
	var artifact geminiArtifact
	if err := state.unseal(domain, operation, body, &artifact, geminiArtifactLimit); err != nil {
		return [2][]byte{}, err
	}
	if artifact.ID != operation.ID || artifact.Binding != operation.Binding {
		geminiClearBodies(&artifact.Bodies)
		return [2][]byte{}, ErrGeminiAttention
	}
	expectedRoles := operation.Before
	if domain == "candidate" {
		expectedRoles = operation.After
	}
	for index, body := range artifact.Bodies {
		if len(body) > 256<<10 || expectedRoles[index].Present && sha256.Sum256(body) != expectedRoles[index].SHA256 || !expectedRoles[index].Present && len(body) != 0 {
			geminiClearBodies(&artifact.Bodies)
			return [2][]byte{}, ErrGeminiAttention
		}
	}
	return artifact.Bodies, nil
}
