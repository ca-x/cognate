package configapply

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"slices"
	"strings"
)

const nativeJournalLimit = 1 << 20
const nativeBackupLimit = 512 << 10

type nativeKeys struct {
	intent   []byte
	journal  []byte
	backup   []byte
	identity [32]byte
}

type operationRecord struct {
	Plan                   planData
	Commitment             [32]byte
	Sequence               uint64
	Phase                  string
	Transaction            string
	TransactionIdentity    nativeIdentity
	Stage                  nativeImage
	Capacity               nativeImage
	BackupID               string
	RestoreAbsent          bool
	Residue                bool
	VaultCapacityName      string
	VaultCapacity          nativeImage
	MaterializedCommitment [32]byte
}

type backupRecord struct {
	Image nativeImage
	Data  []byte
}

type nativeRecord struct {
	Version   uint32
	Type      string
	ID        string
	Home      nativeIdentity
	State     nativeIdentity
	Vault     nativeIdentity
	Selection selectionData
	Operation *operationRecord
	Backup    *backupRecord
	Count     uint64
	Digest    [32]byte
}

type encryptedRecord struct {
	Version    uint32
	Type       string
	ID         string
	Context    [32]byte
	KeyID      [32]byte
	Nonce      []byte
	Ciphertext []byte
}

type nativeInventory struct {
	selections map[string]selectionData
	operations map[string]*operationRecord
	backups    map[string]*nativeRecord
	opBytes    map[string]int
}

func (engine *Engine) openVault() error {
	rootName := "native-configuration-root.v1"
	rootImage, rootData, err := engine.private().state.read(rootName, nativeJournalLimit)
	if err != nil {
		return err
	}
	defer clear(rootData)
	engine.private().vault, err = engine.private().state.child("native-configuration-v1", false)
	if errors.Is(err, ErrTargetAbsent) {
		if rootImage.Present {
			return ErrStateInvalid
		}
		engine.private().vault, err = engine.private().state.child("native-configuration-v1", true)
		if err != nil {
			return err
		}
		if err := engine.appendRecord(nativeRecord{Type: "vault", ID: "v1"}, nil); err != nil {
			return err
		}
		root := nativeRecord{Version: 1, Type: "root", ID: "v1", Home: directoryBinding(engine.private().home.identity), State: directoryBinding(engine.private().state.identity), Vault: directoryBinding(engine.private().vault.identity)}
		rootData, err = engine.sealRecord(root)
		if err != nil {
			return err
		}
		defer clear(rootData)
		rootImage, err = engine.private().state.write(rootName, rootData)
		if err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if engine.private().vault.identity.Mode&0o077 != 0 {
		return ErrUnsafeTarget
	}
	if !rootImage.Present || rootImage.Identity.Mode&0o077 != 0 {
		return ErrStateInvalid
	}
	root, err := engine.openRecord(rootName, rootData)
	if err != nil || root.Type != "root" || root.ID != "v1" || root.Backup != nil || root.Operation != nil {
		return ErrStateInvalid
	}
	return nil
}

func deriveNativeKeys(key []byte) (nativeKeys, error) {
	if len(key) != 32 {
		return nativeKeys{}, ErrStateInvalid
	}
	var result nativeKeys
	var err error
	result.intent, err = hkdf.Key(sha256.New, key, nil, "cognate/native-configuration/intent/v1", 32)
	if err != nil {
		return nativeKeys{}, ErrStateInvalid
	}
	result.journal, err = hkdf.Key(sha256.New, key, nil, "cognate/native-configuration/journal/v1", 32)
	if err != nil {
		result.destroy()
		return nativeKeys{}, ErrStateInvalid
	}
	result.backup, err = hkdf.Key(sha256.New, key, nil, "cognate/native-configuration/backup/v1", 32)
	if err != nil {
		result.destroy()
		return nativeKeys{}, ErrStateInvalid
	}
	result.identity = nativeMAC(result.journal, []byte("cognate/native-configuration/key-id/v1"))
	return result, nil
}

func (keys *nativeKeys) destroy() {
	clear(keys.intent)
	clear(keys.journal)
	clear(keys.backup)
	clear(keys.identity[:])
}

func directoryBinding(identity nativeIdentity) nativeIdentity {
	identity.Links = 0
	identity.Size = 0
	return identity
}

func selectionKey(selection selectionData) string {
	var encoding nativeEncoding
	encoding.text(selection.Origin)
	encoding.text(selection.Owner)
	encoding.text(selection.Profile)
	encoding.text("claude")
	encoding.text("claude.settings")
	encoding.text(selection.Scope)
	digest := sha256.Sum256(encoding.Bytes())
	return hex.EncodeToString(digest[:])
}

func recordName(record nativeRecord) string {
	if record.Type == "vault" {
		return "vault.v1"
	}
	if record.Type == "root" {
		return "native-configuration-root.v1"
	}
	digest := sha256.Sum256([]byte(record.ID))
	suffix := uint64(0)
	if record.Type == "selection" {
		suffix = record.Selection.SelectionRevision
	}
	if record.Type == "operation" && record.Operation != nil {
		suffix = record.Operation.Sequence
	}
	return fmt.Sprintf("%s-%x-%020d.v1", record.Type, digest, suffix)
}

func (engine *Engine) recordContext(record nativeRecord) [32]byte {
	var encoding nativeEncoding
	encoding.text("cognate/native-configuration/record-context/v1")
	encoding.text(record.Type)
	encoding.text(record.ID)
	encoding.number(uint64(record.Version))
	encoding.identity(record.Home)
	encoding.identity(record.State)
	encoding.identity(record.Vault)
	if record.Type != "vault" && record.Type != "root" {
		encoding.selection(record.Selection, true)
	}
	return nativeMAC(engine.private().keys.journal, encoding.Bytes())
}

func recordAAD(envelope encryptedRecord) []byte {
	var encoding nativeEncoding
	encoding.text("cognate/native-configuration/envelope/v1")
	encoding.number(uint64(envelope.Version))
	encoding.text(envelope.Type)
	encoding.text(envelope.ID)
	encoding.blob(envelope.KeyID[:])
	encoding.blob(envelope.Context[:])
	return encoding.Bytes()
}

func (engine *Engine) recordCipher(kind string) (cipher.AEAD, error) {
	key := engine.private().keys.journal
	if kind == "backup" {
		key = engine.private().keys.backup
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, ErrStateInvalid
	}
	return cipher.NewGCM(block)
}

func (engine *Engine) sealRecord(record nativeRecord) ([]byte, error) {
	plaintext, err := json.Marshal(record)
	if err != nil {
		return nil, ErrStateInvalid
	}
	defer clear(plaintext)
	aead, err := engine.recordCipher(record.Type)
	if err != nil {
		return nil, err
	}
	envelope := encryptedRecord{Version: 1, Type: record.Type, ID: record.ID, Context: engine.recordContext(record), KeyID: engine.private().keys.identity, Nonce: make([]byte, aead.NonceSize())}
	if _, err := rand.Read(envelope.Nonce); err != nil {
		return nil, ErrStateInvalid
	}
	envelope.Ciphertext = aead.Seal(nil, envelope.Nonce, plaintext, recordAAD(envelope))
	data, err := json.Marshal(envelope)
	clear(envelope.Ciphertext)
	if err != nil {
		return nil, ErrStateInvalid
	}
	limit := nativeJournalLimit
	if record.Type == "backup" {
		limit = nativeBackupLimit
	}
	if len(data) > limit {
		clear(data)
		return nil, ErrCapacity
	}
	return data, nil
}

func (engine *Engine) openRecord(name string, data []byte) (nativeRecord, error) {
	var envelope encryptedRecord
	if json.Unmarshal(data, &envelope, json.RejectUnknownMembers(true)) != nil || envelope.Version != 1 || !hmac.Equal(envelope.KeyID[:], engine.private().keys.identity[:]) {
		return nativeRecord{}, ErrStateInvalid
	}
	if envelope.Type != "vault" && envelope.Type != "root" && envelope.Type != "selection" && envelope.Type != "operation" && envelope.Type != "backup" {
		return nativeRecord{}, ErrStateInvalid
	}
	aead, err := engine.recordCipher(envelope.Type)
	if err != nil || len(envelope.Nonce) != aead.NonceSize() {
		return nativeRecord{}, ErrStateInvalid
	}
	plaintext, err := aead.Open(nil, envelope.Nonce, envelope.Ciphertext, recordAAD(envelope))
	clear(envelope.Ciphertext)
	if err != nil {
		return nativeRecord{}, ErrStateInvalid
	}
	defer clear(plaintext)
	var record nativeRecord
	if json.Unmarshal(plaintext, &record, json.RejectUnknownMembers(true)) != nil {
		return nativeRecord{}, ErrStateInvalid
	}
	context := engine.recordContext(record)
	validName := recordName(record) == name || strings.HasPrefix(name, "pending-") && record.Type != "vault" || strings.HasPrefix(name, "marker-") && record.Type == "vault"
	if record.Version != 1 || record.Type != envelope.Type || record.ID != envelope.ID || !hmac.Equal(context[:], envelope.Context[:]) || !validName || record.Home != directoryBinding(engine.private().home.identity) || record.State != directoryBinding(engine.private().state.identity) || record.Vault != directoryBinding(engine.private().vault.identity) {
		clearBackup(record)
		return nativeRecord{}, ErrStateInvalid
	}
	return record, nil
}

func clearBackup(record nativeRecord) {
	if record.Backup != nil {
		clear(record.Backup.Data)
	}
}

func (engine *Engine) appendRecord(record nativeRecord, inventory *nativeInventory) error {
	record.Version = 1
	record.Home, record.State = directoryBinding(engine.private().home.identity), directoryBinding(engine.private().state.identity)
	record.Vault = directoryBinding(engine.private().vault.identity)
	data, err := engine.sealRecord(record)
	if err != nil {
		return err
	}
	defer clear(data)
	if record.Type == "operation" && inventory != nil && inventory.opBytes[record.ID]+len(data) > nativeJournalLimit {
		return ErrCapacity
	}
	names, err := engine.private().vault.entries()
	if err != nil || len(names) >= 8190 {
		return ErrCapacity
	}
	temporary := "pending-" + rand.Text()
	if _, err := engine.private().vault.write(temporary, data); err != nil {
		return err
	}
	boundary := "journal-" + record.Type
	if record.Operation != nil {
		boundary += "-" + record.Operation.Phase
	}
	if err := engine.checkpointNative(boundary + "-file-synced"); err != nil {
		return err
	}
	if record.Type != "vault" {
		next := engine.private().anchor
		next.Count++
		xorRecordDigest(&next.Digest, recordName(record), data)
		if err := engine.replaceAnchor(next, boundary); err != nil {
			return err
		}
	}
	if err := engine.private().vault.rename(temporary, engine.private().vault, recordName(record)); err != nil {
		return err
	}
	if err := engine.checkpointNative(boundary + "-renamed"); err != nil {
		return err
	}
	if err := engine.private().vault.sync(); err != nil {
		return err
	}
	if err := engine.checkpointNative(boundary + "-directory-synced"); err != nil {
		return err
	}
	if record.Type == "operation" && inventory != nil {
		inventory.opBytes[record.ID] += len(data)
	}
	return nil
}

func (engine *Engine) checkpointNative(name string) error {
	if engine.private().fault != nil {
		return engine.private().fault(name)
	}
	return nil
}

func xorRecordDigest(digest *[32]byte, name string, data []byte) {
	var encoding nativeEncoding
	encoding.text(name)
	encoding.blob(data)
	sum := sha256.Sum256(encoding.Bytes())
	for index := range digest {
		digest[index] ^= sum[index]
	}
}

func (engine *Engine) replaceAnchor(next nativeRecord, boundary string) error {
	data, err := engine.sealRecord(next)
	if err != nil {
		return err
	}
	defer clear(data)
	temporary := "marker-" + rand.Text()
	image, err := engine.private().vault.write(temporary, data)
	if err != nil {
		return err
	}
	if err := engine.checkpointNative(boundary + "-marker-file-synced"); err != nil {
		return err
	}
	if err := engine.private().vault.exchange(temporary, "vault.v1"); err != nil {
		return err
	}
	if err := engine.checkpointNative(boundary + "-marker-exchanged"); err != nil {
		return err
	}
	old, oldData, err := engine.private().vault.read(temporary, nativeJournalLimit)
	clear(oldData)
	if err != nil || old != engine.private().anchorImage {
		return ErrStateInvalid
	}
	if err := engine.private().vault.sync(); err != nil {
		return err
	}
	engine.private().anchor, engine.private().anchorImage = next, image
	if err := engine.checkpointNative(boundary + "-marker-directory-synced"); err != nil {
		return err
	}
	return engine.private().vault.removeOwn(temporary, old)
}

func (engine *Engine) recoverRecords() error {
	anchorImage, anchorData, err := engine.private().vault.read("vault.v1", nativeJournalLimit)
	if err != nil || !anchorImage.Present {
		clear(anchorData)
		return ErrStateInvalid
	}
	anchor, err := engine.openRecord("vault.v1", anchorData)
	clear(anchorData)
	if err != nil || anchor.Type != "vault" || anchor.Operation != nil || anchor.Backup != nil {
		return ErrStateInvalid
	}
	engine.private().anchor, engine.private().anchorImage = anchor, anchorImage
	names, err := engine.private().vault.entries()
	if err != nil {
		return err
	}
	var count uint64
	var digest [32]byte
	type pendingRecord struct {
		name, destination string
		image             nativeImage
		data              []byte
	}
	var pending []pendingRecord
	defer func() {
		for _, record := range pending {
			clear(record.data)
		}
	}()
	for _, name := range names {
		if name == "vault.v1" {
			continue
		}
		if strings.HasPrefix(name, "capacity-") {
			continue
		}
		image, data, err := engine.private().vault.read(name, nativeJournalLimit)
		if err != nil || !image.Present || image.Identity.Mode&0o077 != 0 {
			clear(data)
			return ErrStateInvalid
		}
		record, err := engine.openRecord(name, data)
		if err != nil {
			clear(data)
			return err
		}
		clearBackup(record)
		if strings.HasPrefix(name, "marker-") {
			clear(data)
			if record.ID != "v1" {
				return ErrStateInvalid
			}
			if err := engine.private().vault.removeOwn(name, image); err != nil {
				return err
			}
			continue
		}
		if strings.HasPrefix(name, "pending-") {
			pending = append(pending, pendingRecord{name, recordName(record), image, data})
			continue
		}
		count++
		xorRecordDigest(&digest, name, data)
		clear(data)
	}
	committed := count == anchor.Count && digest == anchor.Digest
	for _, record := range pending {
		count++
		xorRecordDigest(&digest, record.destination, record.data)
	}
	if !committed && (count != anchor.Count || digest != anchor.Digest) {
		return ErrStateInvalid
	}
	if committed && len(pending) != 0 {
		next := anchor
		next.Count, next.Digest = count, digest
		if err := engine.replaceAnchor(next, "recovery"); err != nil {
			return err
		}
	}
	for _, record := range pending {
		if err := engine.private().vault.rename(record.name, engine.private().vault, record.destination); err != nil {
			return err
		}
	}
	return engine.private().vault.sync()
}

func (engine *Engine) readInventory() (*nativeInventory, error) {
	if err := engine.recoverRecords(); err != nil {
		return nil, err
	}
	inventory := &nativeInventory{selections: make(map[string]selectionData), operations: make(map[string]*operationRecord), backups: make(map[string]*nativeRecord), opBytes: make(map[string]int)}
	names, err := engine.private().vault.entries()
	if err != nil {
		return nil, err
	}
	slices.Sort(names)
	sequences := make(map[string][]uint64)
	selectionRevisions := make(map[string][]uint64)
	vault := false
	failed := true
	defer func() {
		if failed {
			inventory.destroy()
		}
	}()
	for _, name := range names {
		if strings.HasPrefix(name, "pending-") {
			return nil, ErrStateInvalid
		}
		if strings.HasPrefix(name, "capacity-") {
			continue
		}
		image, data, err := engine.private().vault.read(name, nativeJournalLimit)
		if err != nil || !image.Present || image.Identity.Mode&0o077 != 0 {
			clear(data)
			return nil, ErrStateInvalid
		}
		record, err := engine.openRecord(name, data)
		clear(data)
		if err != nil {
			return nil, err
		}
		switch record.Type {
		case "vault":
			if record.ID != "v1" || vault || record.Backup != nil || record.Operation != nil {
				return nil, ErrStateInvalid
			}
			vault = true
		case "selection":
			if _, err := NewSelection(record.Selection.SelectionInput); err != nil || record.ID != selectionKey(record.Selection) {
				return nil, ErrStateInvalid
			}
			selectionRevisions[record.ID] = append(selectionRevisions[record.ID], record.Selection.SelectionRevision)
			if current, exists := inventory.selections[record.ID]; !exists || current.SelectionRevision < record.Selection.SelectionRevision {
				inventory.selections[record.ID] = record.Selection
			}
		case "operation":
			operation := record.Operation
			if operation == nil {
				return nil, ErrStateInvalid
			}
			binding := operation.Plan
			binding.Selection = record.Selection
			if operation.Plan.Selection.OperationID != record.ID || record.Selection.OperationID != record.ID || !bytes.Equal(nativePlanBytes(operation.Plan), nativePlanBytes(binding)) {
				return nil, ErrStateInvalid
			}
			if nativeMAC(engine.private().keys.intent, nativePlanBytes(operation.Plan)) != operation.Commitment {
				return nil, ErrStateInvalid
			}
			if operation.MaterializedCommitment != [32]byte{} && nativeMaterializedMAC(engine.private().keys.intent, operation) != operation.MaterializedCommitment {
				return nil, ErrStateInvalid
			}
			sequences[record.ID] = append(sequences[record.ID], operation.Sequence)
			inventory.opBytes[record.ID] += int(image.Identity.Size)
			if inventory.opBytes[record.ID] > nativeJournalLimit {
				return nil, ErrCapacity
			}
			if current := inventory.operations[record.ID]; current == nil || current.Sequence < operation.Sequence {
				inventory.operations[record.ID] = operation
			}
		case "backup":
			if record.Backup == nil || record.Operation != nil || len(record.Backup.Data) > 256<<10 || !record.Backup.Image.Present && len(record.Backup.Data) != 0 || record.Backup.Image.Present && sha256.Sum256(record.Backup.Data) != record.Backup.Image.SHA256 {
				clearBackup(record)
				return nil, ErrStateInvalid
			}
			inventory.backups[record.ID] = &record
		default:
			return nil, ErrStateInvalid
		}
	}
	if !vault {
		return nil, ErrStateInvalid
	}
	for _, generations := range sequences {
		slices.Sort(generations)
		for index, generation := range generations {
			if generation != uint64(index) {
				return nil, ErrStateInvalid
			}
		}
	}
	for _, revisions := range selectionRevisions {
		slices.Sort(revisions)
		for index, revision := range revisions {
			if revision != uint64(index+1) {
				return nil, ErrStateInvalid
			}
		}
	}
	for _, operation := range inventory.operations {
		current, exists := inventory.selections[selectionKey(operation.Plan.Selection)]
		if !exists || current.SelectionRevision < operation.Plan.Selection.SelectionRevision {
			return nil, ErrStateInvalid
		}
	}
	for _, name := range names {
		if !strings.HasPrefix(name, "capacity-") {
			continue
		}
		known := false
		for _, operation := range inventory.operations {
			if operation.VaultCapacityName == name {
				known = true
				break
			}
		}
		if !known {
			return nil, ErrStateInvalid
		}
	}
	failed = false
	return inventory, nil
}

func nativeMaterializedMAC(key []byte, operation *operationRecord) [32]byte {
	var encoding nativeEncoding
	encoding.text("cognate/native-configuration/materialized/v1")
	encoding.blob(operation.Commitment[:])
	encoding.text(operation.Transaction)
	encoding.identity(operation.TransactionIdentity)
	encoding.image(operation.Stage)
	encoding.present(operation.RestoreAbsent)
	return nativeMAC(key, encoding.Bytes())
}

func (inventory *nativeInventory) destroy() {
	if inventory != nil {
		for _, record := range inventory.backups {
			clearBackup(*record)
		}
	}
}

func (engine *Engine) saveOperation(operation *operationRecord, inventory *nativeInventory, phase string) error {
	next := *operation
	next.Sequence++
	next.Phase = phase
	if err := engine.appendRecord(nativeRecord{Type: "operation", ID: next.Plan.Selection.OperationID, Selection: next.Plan.Selection, Operation: &next}, inventory); err != nil {
		return err
	}
	*operation = next
	inventory.operations[next.Plan.Selection.OperationID] = operation
	return nil
}
