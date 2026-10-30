package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sio "github.com/secure-io/sio-go"
)

func independentObject(identity attemptData, purpose byte, ordinal uint32, plain, secret []byte, mutate ...func([]byte)) ([]byte, error) {
	header := make([]byte, 96)
	copy(header, "CGBKSE01")
	binary.BigEndian.PutUint16(header[8:], 1)
	binary.BigEndian.PutUint16(header[10:], 1)
	header[12] = purpose
	copy(header[16:], identity.instance[:])
	copy(header[32:], identity.backup[:])
	copy(header[48:], identity.attempt[:])
	binary.BigEndian.PutUint32(header[64:], ordinal)
	binary.BigEndian.PutUint32(header[68:], 1048576)
	binary.BigEndian.PutUint64(header[72:], uint64(len(plain)))
	copy(header[80:], []byte{7, 8, 9, 10, 11, 12, 13, 14})
	key, err := hkdf.Key(sha256.New, secret, identity.attempt[:], "cognate/f4/object-key/v1\x00"+string(header), 32)
	if err != nil {
		return nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	trailer := make([]byte, 80)
	copy(trailer, "CGBKEND1")
	binary.BigEndian.PutUint64(trailer[8:], uint64(len(plain)))
	binary.BigEndian.PutUint32(trailer[16:], uint32((len(plain)+1048575)/1048576))
	binary.BigEndian.PutUint32(trailer[20:], uint32((len(plain)+80+1048575)/1048576))
	digest := sha256.Sum256(plain)
	copy(trailer[24:], digest[:])
	trailer[56] = purpose
	for _, change := range mutate {
		change(trailer)
	}
	var output bytes.Buffer
	output.Write(header)
	_, err = io.Copy(&output, sio.NewStream(aead, 1048576).EncryptReader(io.MultiReader(bytes.NewReader(plain), bytes.NewReader(trailer)), header[80:88], append([]byte("cognate/f4/object-aad/v1\x00"), header...)))
	return output.Bytes(), err
}

func TestSealPhysicalBoundaries(test *testing.T) {
	for _, size := range []int64{0, 1, 1048496, 1048575, 1048576, 1048577} {
		test.Run(fmt.Sprint(size), func(test *testing.T) {
			engine, _, source := sealFixture(test)
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if err != nil {
				test.Fatal(err)
			}
			defer bootstrap.Close()
			if err = os.Truncate(source.Name(), size); err != nil {
				test.Fatal(err)
			}
			artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
			if size == 0 {
				if err == nil || artifact.get != nil {
					test.Fatal("empty physical source admitted")
				}
				return
			}
			if err != nil {
				test.Fatal(err)
			}
			if err = artifact.Close(); err != nil {
				test.Fatal(err)
			}
		})
	}
}

func TestSealBurnBeforeInput(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	if err = os.Truncate(source.Name(), 0); err != nil {
		test.Fatal(err)
	}
	if artifact, err := engine.SealExtent(test.Context(), attempt, 7, source); err == nil || artifact.get != nil {
		test.Fatal("empty source accepted")
	}
	if err = os.WriteFile(source.Name(), []byte("replacement"), 0600); err != nil {
		test.Fatal(err)
	}
	if artifact, err := engine.SealExtent(test.Context(), attempt, 7, source); err == nil || artifact.get != nil {
		test.Fatal("burned pre-input ordinal reused")
	}
	artifact, err := engine.SealExtent(test.Context(), attempt, 8, source)
	if err != nil {
		test.Fatal("unburned slot refused", err)
	}
	defer artifact.Close()
}

func TestSealPhysicalRefusals(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	alias := source.Name() + ".alias"
	if err = os.Link(source.Name(), alias); err != nil {
		test.Fatal(err)
	}
	if _, err = engine.SealExtent(test.Context(), attempt, 0, source); err == nil {
		test.Fatal("hardlinked source admitted")
	}
	if err = os.Remove(alias); err != nil {
		test.Fatal(err)
	}
	if err = os.Chmod(source.Name(), 0644); err != nil {
		test.Fatal(err)
	}
	if _, err = engine.SealExtent(test.Context(), attempt, 1, source); err == nil {
		test.Fatal("public source admitted")
	}
	if err = os.Chmod(source.Name(), 0600); err != nil {
		test.Fatal(err)
	}
	if err = os.Symlink(source.Name(), alias); err != nil {
		test.Fatal(err)
	}
	borrowed, err := os.Open(alias)
	if err != nil {
		test.Fatal(err)
	}
	defer borrowed.Close()
	if _, err = engine.SealExtent(test.Context(), attempt, 2, borrowed); err == nil {
		test.Fatal("symlink name admitted")
	}
}

func TestSealWithholdingTampering(test *testing.T) {
	engine, _, _ := sealFixture(test)
	state := engine.get()
	identity := attemptData{instance: [16]byte{1}, backup: [16]byte{2}, attempt: [16]byte{3}}
	secret := bytes.Repeat([]byte{31}, 32)
	plain := bytes.Repeat([]byte{37}, 2*1048576+9)
	wire, err := independentObject(identity, 1, 0, plain, secret)
	if err != nil {
		test.Fatal(err)
	}
	wrongTrailer, err := independentObject(identity, 1, 0, plain, secret, func(trailer []byte) { trailer[24] ^= 1 })
	if err != nil {
		test.Fatal(err)
	}
	cases := []struct {
		name string
		wire []byte
	}{{"valid", wire}, {"truncated", wire[:len(wire)-1]}, {"drop-final", wire[:96+2*(1048576+16)]}, {"trailing", append(bytes.Clone(wire), 0)}, {"concatenated", append(bytes.Clone(wire), wire...)}, {"wrong-tag", bytes.Clone(wire)}, {"reorder", bytes.Clone(wire)}, {"duplicate", bytes.Clone(wire)}, {"authenticated-bad-trailer", wrongTrailer}}
	cases[5].wire[len(wire)-1] ^= 1
	copy(cases[6].wire[96:96+1048592], wire[96+1048592:96+2*1048592])
	copy(cases[6].wire[96+1048592:96+2*1048592], wire[96:96+1048592])
	copy(cases[7].wire[96+1048592:96+2*1048592], wire[96:96+1048592])
	for index, example := range cases {
		test.Run(example.name, func(test *testing.T) {
			catalogName := fmt.Sprintf("test-index-%d", index)
			catalog, err := sealOpen(state.directory, catalogName, true)
			if err != nil {
				test.Fatal(err)
			}
			defer func() { _ = catalog.Close(); _ = sealUnlink(state.directory, catalogName) }()
			hash := sha256.Sum256(plain)
			record := sealRecord{ordinal: 0, length: int64(len(plain)), plainHash: hash}
			encoded := record.encode()
			if _, err = catalog.WriteAt(encoded[:], 0); err != nil {
				test.Fatal(err)
			}
			current := &sealOpenState{engine: state, ctx: test.Context(), identity: identity, prefix: fmt.Sprintf("test-open-%d-", index), catalog: catalog}
			copy(current.dek[:], secret)
			sourceName := filepath.Join(test.TempDir(), "ciphertext")
			if err = os.WriteFile(sourceName, example.wire, 0600); err != nil {
				test.Fatal(err)
			}
			source, err := os.Open(sourceName)
			if err != nil {
				test.Fatal(err)
			}
			defer source.Close()
			digest := sha256.Sum256(example.wire)
			name, charge, _, err := current.decryptPrivate(test.Context(), source, 1, 0, int64(len(example.wire)), digest)
			if example.name == "valid" {
				if err != nil || charge == nil {
					test.Fatal("positive seed failed", err)
				}
				charge.release()
				if err = sealUnlink(state.directory, name); err != nil {
					test.Fatal(err)
				}
				return
			}
			if err == nil || charge != nil {
				test.Fatal("invalid ciphertext exposed private artifact")
			}
			if _, err = os.Stat(filepath.Join(state.directory.root.Name(), name)); !os.IsNotExist(err) {
				test.Fatal("invalid plaintext partial retained without ownership")
			}
		})
	}
}

func TestSealAuthenticatedCompleteStrict(test *testing.T) {
	engine, _, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	secret := independentUnwrap(test, bootstrap)
	defer clear(secret)
	identity := attempt.get().identity
	bootstrapHash := bootstrap.Ref().read().digest
	valid := fmt.Sprintf(`{"format":"cognate-backup-complete","version":1,"suite":1,"instance_id":"%x","backup_id":"%x","attempt_id":"%x","bootstrap_bytes":156,"bootstrap_sha256":"%x","root_ciphertext_bytes":512,"root_ciphertext_sha256":"%s","object_count_without_complete":5,"ciphertext_bytes_without_complete":10000}`+"\n", identity.instance, identity.backup, identity.attempt, bootstrapHash, strings.Repeat("1", 64))
	for _, example := range []struct{ name, raw string }{{"valid", valid}, {"missing", strings.Replace(valid, `"suite":1,`, "", 1)}, {"unknown", strings.Replace(valid, `"suite":1,`, `"suite":1,"extra":false,`, 1)}, {"null", strings.Replace(valid, `"suite":1`, `"suite":null`, 1)}, {"exponent", strings.Replace(valid, `"suite":1`, `"suite":1e0`, 1)}, {"duplicate", strings.Replace(valid, `"suite":1,`, `"suite":1,"suite":1,`, 1)}, {"extra-LF", valid + "\n"}} {
		test.Run(example.name, func(test *testing.T) {
			wire, err := independentObject(identity, 4, 0, []byte(example.raw), secret)
			if err != nil {
				test.Fatal(err)
			}
			name := filepath.Join(test.TempDir(), "complete")
			if err = os.WriteFile(name, wire, 0600); err != nil {
				test.Fatal(err)
			}
			file, err := os.Open(name)
			if err != nil {
				test.Fatal(err)
			}
			defer file.Close()
			var opened OpenSession
			err = bootstrap.WithFile(test.Context(), func(source *os.File) error {
				var openErr error
				opened, openErr = engine.BeginOpen(test.Context(), source, file, Attempt{read: func() attemptData { return identity }})
				return openErr
			})
			if example.name == "valid" {
				if err != nil || opened.get == nil {
					test.Fatal("independent valid complete refused", err)
				}
				if _, err = opened.Finish(test.Context()); err == nil {
					test.Fatal("incomplete open minted bundle")
				}
				if err = opened.Close(); err != nil {
					test.Fatal(err)
				}
			} else if err == nil || opened.get != nil {
				test.Fatal("authenticated malformed complete admitted")
			}
		})
	}
}

func independentUnwrap(test *testing.T, bootstrap SealedFile) (secret []byte) {
	test.Helper()
	err := bootstrap.WithFile(test.Context(), func(file *os.File) error {
		encoded := make([]byte, 156)
		if count, err := file.ReadAt(encoded, 0); err != nil || count != 156 {
			return ErrInvalid
		}
		key, err := hkdf.Key(sha256.New, bytes.Repeat([]byte{13}, 32), encoded[48:64], "cognate/f4/bootstrap-key/v1\x00"+string(encoded[:96]), 32)
		if err != nil {
			return err
		}
		defer clear(key)
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		secret, err = aead.Open(nil, encoded[96:108], encoded[108:], append([]byte("cognate/f4/bootstrap-aad/v1\x00"), encoded[:96]...))
		return err
	})
	if err != nil {
		test.Fatal("independent bootstrap unwrap", err)
	}
	return secret
}

func TestSealIndependentLiteralStreams(test *testing.T) {
	identity := attemptData{instance: [16]byte{1}, backup: [16]byte{2}, attempt: [16]byte{3}}
	secret := bytes.Repeat([]byte{13}, 32)
	for purpose := byte(1); purpose <= 4; purpose++ {
		test.Run(fmt.Sprint(purpose), func(test *testing.T) {
			ordinal := uint32(0)
			if purpose == 2 {
				ordinal = 1
			}
			plain := []byte("independent literal payload")
			expected, err := independentObject(identity, purpose, ordinal, plain, secret)
			if err != nil {
				test.Fatal(err)
			}
			header := sealHeader{purpose: purpose, attempt: identity, ordinal: ordinal, length: int64(len(plain)), nonce: [8]byte{7, 8, 9, 10, 11, 12, 13, 14}}
			var actual bytes.Buffer
			if _, err = sealEncrypt(test.Context(), header, secret, bytes.NewReader(plain), &actual); err != nil || !bytes.Equal(expected, actual.Bytes()) {
				test.Fatal("independent complete wire mismatch", err)
			}
			digest := sha256.Sum256(expected)
			literals := []string{"603618cdfe1e6c74bbf9d53fa3e36b873ecb1aea81ca8b8f9d542ecc8eedf5c6", "17b51337afa61a2d7750dba1a17498dca109076730f02dd3ba3ab1b323674582", "7eb5bacc70dce0fd2fa657d1df37151c8de83229b05d19a69d1bfb9fe961b88d", "286daa0746f4516a3dc1864f3a1a16a28aaafb661b186c1f22f64aafa5fe27e7"}
			if hex.EncodeToString(digest[:]) != literals[purpose-1] {
				test.Fatal("independent frozen vector drift")
			}
			var opened bytes.Buffer
			if _, err = sealDecrypt(test.Context(), header, secret, bytes.NewReader(expected[96:]), &opened); err != nil || !bytes.Equal(opened.Bytes(), plain) {
				test.Fatal("independent writer to product reader", err)
			}
		})
	}
}

func TestSealMaximumPhysicalExtent(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	secret := independentUnwrap(test, bootstrap)
	defer clear(secret)
	if err = os.Truncate(source.Name(), 67108864); err != nil {
		test.Fatal(err)
	}
	beforeHash := sha256.New()
	if _, err = io.CopyBuffer(beforeHash, &sealOffsetReader{ctx: test.Context(), file: source}, make([]byte, 32768)); err != nil {
		test.Fatal(err)
	}
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	if artifact.Ref().read().size != 67110080 {
		test.Fatal("64MiB format changed")
	}
	err = artifact.WithFile(test.Context(), func(file *os.File) error {
		header := make([]byte, 96)
		if _, err := io.ReadFull(file, header); err != nil {
			return err
		}
		key, err := hkdf.Key(sha256.New, secret, header[48:64], "cognate/f4/object-key/v1\x00"+string(header), 32)
		if err != nil {
			return err
		}
		defer clear(key)
		block, err := aes.NewCipher(key)
		if err != nil {
			return err
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			return err
		}
		reader := sio.NewStream(aead, 1048576).DecryptReader(file, header[80:88], append([]byte("cognate/f4/object-aad/v1\x00"), header...))
		hash := sha256.New()
		count, err := io.CopyBuffer(hash, io.LimitReader(reader, 67108864), make([]byte, 32768))
		if err != nil || count != 67108864 || !bytes.Equal(hash.Sum(nil), beforeHash.Sum(nil)) {
			return ErrInvalid
		}
		var trailer [81]byte
		trailerCount, readErr := io.ReadFull(reader, trailer[:])
		if trailerCount != 80 || readErr != io.ErrUnexpectedEOF || string(trailer[:8]) != "CGBKEND1" {
			return ErrInvalid
		}
		return nil
	})
	if err != nil {
		test.Fatal("independent max-file consumer", err)
	}
}

func TestSealQuotaBurnAndCloseFault(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	state := engine.get()
	state.limits.TotalCipherBytes = state.cipher + 218
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if !errors.Is(err, ErrSealBudget) || artifact.get != nil {
		test.Fatal("minus-one quota exposed ciphertext")
	}
	state.limits.TotalCipherBytes = 1 << 30
	if _, err = engine.SealExtent(test.Context(), attempt, 0, source); err == nil {
		test.Fatal("failed one-shot reservation was reused")
	}
	if err = attempt.Close(); err != nil {
		test.Fatal(err)
	}
	attempt, nextBootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{3})
	if err != nil {
		test.Fatal(err)
	}
	defer nextBootstrap.Close()
	oldClose := state.ops.close
	state.ops.close = func(file *os.File) error {
		err := oldClose(file)
		if strings.Contains(filepath.Base(file.Name()), "-1-") {
			return ErrSealCleanup
		}
		return err
	}
	defer func() { state.ops.close = oldClose }()
	artifact, err = engine.SealExtent(test.Context(), attempt, 0, source)
	if !errors.Is(err, ErrSealUncertain) || artifact.get != nil {
		test.Fatal("failed checked Close exposed ciphertext")
	}
}

func TestSealTwoSlotsAndCloseJoin(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	if err = os.Truncate(source.Name(), 67108864); err != nil {
		test.Fatal(err)
	}
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	state := engine.get()
	original := state.ops.sync
	entered := make(chan struct{}, 2)
	release := make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	state.ops.sync = func(file *os.File) error {
		if strings.HasSuffix(file.Name(), "-1-00000000") || strings.HasSuffix(file.Name(), "-1-00000001") {
			entered <- struct{}{}
			<-release
		}
		return original(file)
	}
	results := make(chan error, 2)
	for ordinal := uint32(0); ordinal < 2; ordinal++ {
		go func() {
			artifact, err := engine.SealExtent(test.Context(), attempt, ordinal, source)
			if artifact.get != nil {
				_ = artifact.Close()
			}
			results <- err
		}()
	}
	for range 2 {
		select {
		case <-entered:
		case <-time.After(5 * time.Second):
			test.Fatal("two stream barriers not reached")
		}
	}
	var active runtime.MemStats
	runtime.ReadMemStats(&active)
	test.Logf("sample_only: two_max_extents=2 bytes_each=67108864 heap_inuse_before=%d heap_inuse_active=%d alloc_delta=%d staging_charge=%d", before.HeapInuse, active.HeapInuse, active.TotalAlloc-before.TotalAlloc, state.disk)
	if artifact, err := engine.SealExtent(test.Context(), attempt, 2, source); !errors.Is(err, ErrBusy) || artifact.get != nil {
		test.Fatal("third stream admitted before slot release")
	}
	closed := make(chan error, 1)
	go func() { closed <- engine.Close() }()
	select {
	case <-state.ctx.Done():
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not cancel")
	}
	select {
	case <-closed:
		test.Fatal("Close returned with live writers")
	default:
	}
	unblock.Do(func() { close(release) })
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				test.Fatal("late canceled writer succeeded")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("writer not joined")
		}
	}
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("Close did not join")
	}
	state.ops.sync = original
}

func TestSealStrictPresence(test *testing.T) {
	for _, raw := range []string{`{"bytes":1,"sha256":"x"}`, `{"bytes":1,"sha256":"x","extents":1,"extra":false}`, `{"bytes":1,"bytes":1,"sha256":"x","extents":1}`, `{"bytes":1e0,"sha256":"x","extents":1}`, `{"bytes":1,"sha256":null,"extents":1}`} {
		var value SealFileDescription
		if strictSealJSON([]byte(raw), &value) == nil {
			test.Fatal("noncanonical presence/value admitted")
		}
	}
}

func TestSealCarrierPrivacy(test *testing.T) {
	engine, key, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))
	metadata := SealMetadata{MasterKeyID: "private-manifest-key-identifier", Database: SealFileDescription{SHA256: "private-plaintext-digest"}, ExternalConfigNames: []string{"PRIVATE_CONFIG_NAME"}}
	for _, value := range []any{key, engine, attempt, bootstrap, OpenSession{}, CryptoVerifiedBundle{}, metadata} {
		for _, verb := range []string{"%v", "%+v", "%#v", "%s", "%x", "%q"} {
			fmt.Fprintf(&output, verb, struct{ Value any }{value})
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			test.Fatal(err)
		}
		output.Write(encoded)
		logger.Info("carrier", "value", value)
	}
	if strings.Contains(output.String(), "synthetic bounded plaintext") || strings.Contains(output.String(), engine.get().directory.root.Name()) || strings.Contains(output.String(), "13 13 13 13 13") || strings.Contains(output.String(), "private-manifest-key-identifier") || strings.Contains(output.String(), "private-plaintext-digest") || strings.Contains(output.String(), "PRIVATE_CONFIG_NAME") {
		test.Fatal("opaque carrier disclosed private data")
	}
}

func TestSealLateMetadataCancel(test *testing.T) {
	for _, point := range []string{"metadata-source", "page-final"} {
		test.Run(point, func(test *testing.T) { sealLateMetadataCancelCase(test, point) })
	}
}

func sealLateMetadataCancelCase(test *testing.T, point string) {
	test.Helper()
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	ref := artifact.Ref().read()
	hash := sha256.Sum256([]byte("synthetic bounded plaintext"))
	record := SealExtentRecord{Kind: "extent", FileKind: "database", FileID: "database", PlaintextBytes: 27, PlaintextSHA256: hex.EncodeToString(hash[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	original := engine.get().ops.close
	count := 0
	engine.get().ops.close = func(file *os.File) error {
		err := original(file)
		if point == "metadata-source" && strings.HasPrefix(filepath.Base(file.Name()), "m-") || point == "page-final" && strings.HasSuffix(file.Name(), "-2-00000001") {
			count++
			if point == "metadata-source" && count == 2 || point == "page-final" && count == 3 {
				cancel()
			}
		}
		return err
	}
	defer func() { engine.get().ops.close = original }()
	page, err := engine.SealPage(ctx, attempt, 1, 1, func(yield func(SealExtentRecord, error) bool) { yield(record, nil) })
	if page.get != nil || !errors.Is(err, context.Canceled) {
		if page.get != nil {
			_ = page.Close()
		}
		test.Fatal("late input Close cancellation exposed metadata")
	}
}

func TestSealMixedSlotsMemoryAndJoin(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	if err = os.Truncate(source.Name(), 67108864); err != nil {
		test.Fatal(err)
	}
	secret := independentUnwrap(test, bootstrap)
	defer clear(secret)
	identity := attempt.get().identity
	bootstrapHash := bootstrap.Ref().read().digest
	plain := fmt.Sprintf(`{"format":"cognate-backup-complete","version":1,"suite":1,"instance_id":"%x","backup_id":"%x","attempt_id":"%x","bootstrap_bytes":156,"bootstrap_sha256":"%x","root_ciphertext_bytes":512,"root_ciphertext_sha256":"%s","object_count_without_complete":5,"ciphertext_bytes_without_complete":10000}`+"\n", identity.instance, identity.backup, identity.attempt, bootstrapHash, strings.Repeat("1", 64))
	wire, err := independentObject(identity, 4, 0, []byte(plain), secret)
	if err != nil {
		test.Fatal(err)
	}
	name := filepath.Join(test.TempDir(), "complete")
	if err = os.WriteFile(name, wire, 0600); err != nil {
		test.Fatal(err)
	}
	complete, err := os.Open(name)
	if err != nil {
		test.Fatal(err)
	}
	defer complete.Close()
	state := engine.get()
	original := state.ops.sync
	barriers := make(chan struct{}, 2)
	unblock := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(unblock) })
	state.ops.sync = func(file *os.File) error {
		if strings.HasSuffix(file.Name(), "-1-00000000") || strings.HasSuffix(file.Name(), "p-4-00000000") {
			barriers <- struct{}{}
			<-unblock
		}
		return original(file)
	}
	defer func() { state.ops.sync = original }()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	results := make(chan error, 2)
	go func() {
		artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
		if artifact.get != nil {
			_ = artifact.Close()
		}
		results <- err
	}()
	go func() {
		err := bootstrap.WithFile(test.Context(), func(file *os.File) error {
			session, err := engine.BeginOpen(test.Context(), file, complete, Attempt{read: func() attemptData { return identity }})
			if session.get != nil {
				_ = session.Close()
			}
			return err
		})
		results <- err
	}()
	for range 2 {
		select {
		case <-barriers:
		case <-time.After(5 * time.Second):
			test.Fatal("mixed Seal/Open slots did not reach real output Sync")
		}
	}
	var active runtime.MemStats
	runtime.ReadMemStats(&active)
	test.Logf("sample_only: two_active=2 max_extent=67108864 heap_inuse_before=%d heap_inuse_active=%d alloc_delta=%d staging_charge=%d", before.HeapInuse, active.HeapInuse, active.TotalAlloc-before.TotalAlloc, state.disk)
	if artifact, err := engine.SealExtent(test.Context(), attempt, 1, source); !errors.Is(err, ErrBusy) || artifact.get != nil {
		test.Fatal("mixed transforms did not share the two-slot cap")
	}
	closed := make(chan error, 1)
	go func() { closed <- engine.Close() }()
	select {
	case <-state.ctx.Done():
	case <-time.After(5 * time.Second):
		test.Fatal("mixed Close did not revoke workers")
	}
	select {
	case <-closed:
		test.Fatal("mixed Close returned before physical workers joined")
	default:
	}
	once.Do(func() { close(unblock) })
	for range 2 {
		select {
		case err := <-results:
			if err == nil {
				test.Fatal("mixed canceled worker exposed a capability")
			}
		case <-time.After(5 * time.Second):
			test.Fatal("mixed worker not joined")
		}
	}
	select {
	case err := <-closed:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("mixed Close not joined")
	}
}

func TestSealLateArtifactCancel(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	state := engine.get()
	original := state.ops.close
	closes := 0
	state.ops.close = func(file *os.File) error {
		err := original(file)
		if strings.HasSuffix(file.Name(), "-1-00000000") {
			closes++
			if closes == 2 {
				cancel()
			}
		}
		return err
	}
	defer func() { state.ops.close = original }()
	artifact, err := engine.SealExtent(ctx, attempt, 0, source)
	if artifact.get != nil {
		_ = artifact.Close()
		test.Fatal("late checked Close cancellation exposed ciphertext")
	}
	if !errors.Is(err, context.Canceled) || closes != 2 {
		test.Fatal("late cancellation was not preserved", err)
	}
}

func TestSealFinalSourcePin(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	state := engine.get()
	original := state.ops.sync
	mutated := false
	state.ops.sync = func(file *os.File) error {
		if !mutated && strings.HasSuffix(file.Name(), "-1-00000000") {
			mutated = true
			if err := os.Truncate(source.Name(), 28); err != nil {
				return err
			}
		}
		return original(file)
	}
	defer func() { state.ops.sync = original }()
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if artifact.get != nil {
		_ = artifact.Close()
		test.Fatal("source changed during output finalization was accepted")
	}
	if err == nil || !mutated {
		test.Fatal("final source pin was not checked")
	}
}

func TestSealCleanupChargeRetention(test *testing.T) {
	for _, failure := range []string{"unlink", "parent-sync"} {
		test.Run(failure, func(test *testing.T) {
			engine, _, source := sealFixture(test)
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if err != nil {
				test.Fatal(err)
			}
			defer bootstrap.Close()
			artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
			if err != nil {
				test.Fatal(err)
			}
			state := engine.get()
			diskBefore := state.disk
			original := state.ops.sync
			if failure == "unlink" {
				if err = state.directory.root.Chmod(0500); err != nil {
					test.Fatal(err)
				}
			} else {
				state.ops.sync = func(file *os.File) error {
					if file == state.directory.root {
						return ErrSealCleanup
					}
					return original(file)
				}
			}
			defer func() {
				state.ops.sync = original
				_ = state.directory.root.Chmod(0700)
			}()
			err = artifact.Close()
			if err == nil || state.disk != diskBefore {
				test.Fatal("unconfirmed cleanup returned quota")
			}
			if next, callErr := engine.SealExtent(test.Context(), attempt, 1, source); !errors.Is(callErr, ErrSealUncertain) || next.get != nil {
				test.Fatal("unconfirmed cleanup admitted another transform")
			}
		})
	}
}

func TestSealBeginAttemptCleanupPriority(test *testing.T) {
	for _, failure := range []string{"none", "catalog-close", "catalog-unlink"} {
		test.Run(failure, func(test *testing.T) {
			engine, _, _ := sealFixture(test)
			state := engine.get()
			state.limits.TotalCipherBytes = 155
			original := state.ops.close
			closes := 0
			state.ops.close = func(file *os.File) error {
				err := original(file)
				if strings.HasPrefix(filepath.Base(file.Name()), "i-") {
					closes++
					if failure == "catalog-close" {
						return ErrInvalid
					}
					if failure == "catalog-unlink" {
						if err := state.directory.root.Chmod(0500); err != nil {
							test.Fatal(err)
						}
					}
				}
				return err
			}
			defer func() {
				state.ops.close = original
				_ = state.directory.root.Chmod(0700)
			}()
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if attempt.get != nil || bootstrap.get != nil || closes != 1 || !state.attempt.closed {
				test.Fatal("failed begin exposed a capability or did not join catalog cleanup")
			}
			if failure == "none" {
				if err != ErrSealBudget || state.uncertain || state.disk != state.limits.InputStagingBytes {
					test.Fatal("clean budget rejection retained ownership", err)
				}
				return
			}
			wanted := ErrSealCleanup
			if failure == "catalog-close" {
				wanted = ErrSealUncertain
			}
			if err != wanted || !state.uncertain || state.disk <= state.limits.InputStagingBytes {
				test.Fatal("begin cleanup classification or charged ownership lost", err)
			}
			if next, file, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2}); err != ErrSealUncertain || next.get != nil || file.get != nil || closes != 1 {
				test.Fatal("uncertain engine retried cleanup or admitted work", err)
			}
		})
	}
}

func TestSealMetadataCleanupPriority(test *testing.T) {
	for _, failure := range []string{"cancel-only", "artifact-unlink", "scratch-unlink", "scratch-sync", "source-close", "source-close-and-unlink", "late-scratch-unlink", "late-scratch-sync"} {
		test.Run(failure, func(test *testing.T) {
			engine, _, source := sealFixture(test)
			attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
			if err != nil {
				test.Fatal(err)
			}
			defer bootstrap.Close()
			artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
			if err != nil {
				test.Fatal(err)
			}
			defer artifact.Close()
			ref := artifact.Ref().read()
			digest := sha256.Sum256([]byte("synthetic bounded plaintext"))
			record := SealExtentRecord{Kind: "extent", FileKind: "database", FileID: "database", PlaintextBytes: 27, PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])}
			state := engine.get()
			diskBefore := state.disk
			originalClose, originalSync := state.ops.close, state.ops.sync
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			boundary := 2
			if failure == "scratch-unlink" || failure == "scratch-sync" {
				boundary = 1
			}
			closes, faultReached := 0, false
			state.ops.close = func(file *os.File) error {
				err := originalClose(file)
				if !strings.HasPrefix(filepath.Base(file.Name()), "m-") {
					return err
				}
				closes++
				if closes != boundary {
					return err
				}
				if !strings.HasPrefix(failure, "late-") {
					cancel()
				}
				if strings.Contains(failure, "unlink") {
					if err := state.directory.root.Chmod(0500); err != nil {
						test.Fatal(err)
					}
				}
				if strings.HasPrefix(failure, "source-close") {
					return ErrInvalid
				}
				return err
			}
			state.ops.sync = func(file *os.File) error {
				if boundary == 1 && strings.HasPrefix(filepath.Base(file.Name()), "m-") {
					return ErrSealBudget
				}
				if file == state.directory.root && closes == boundary && !faultReached {
					faultReached = true
					if failure == "artifact-unlink" || failure == "source-close-and-unlink" {
						if err := file.Chmod(0700); err != nil {
							test.Fatal(err)
						}
					}
					if strings.HasSuffix(failure, "scratch-sync") {
						return ErrInvalid
					}
				}
				return originalSync(file)
			}
			defer func() {
				state.ops.close, state.ops.sync = originalClose, originalSync
				_ = state.directory.root.Chmod(0700)
			}()
			page, err := engine.SealPage(ctx, attempt, 1, 1, func(yield func(SealExtentRecord, error) bool) { yield(record, nil) })
			if page.get != nil || closes != boundary || !faultReached || state.active != 0 || attempt.get().active != 0 {
				test.Fatal("metadata failure did not withhold output and join cleanup")
			}
			if failure == "cancel-only" {
				if err != context.Canceled || state.uncertain || state.disk != diskBefore {
					test.Fatal("clean cancellation did not release scratch and output", err)
				}
				return
			}
			wanted := ErrSealCleanup
			if strings.HasPrefix(failure, "source-close") {
				wanted = ErrSealUncertain
			}
			if err != wanted || !state.uncertain || state.disk <= diskBefore {
				test.Fatal("metadata cleanup priority or charged ownership lost", err)
			}
			if next, err := engine.SealExtent(test.Context(), attempt, 1, source); err != ErrSealUncertain || next.get != nil {
				test.Fatal("uncertain metadata cleanup admitted another transform", err)
			}
		})
	}
}

func TestSealCallbackCleanupPriority(test *testing.T) {
	engine, _, _ := sealFixture(test)
	_, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	for _, scenario := range []struct {
		name     string
		callback error
		expected error
	}{
		{"cancel-only", ErrSealBudget, context.Canceled},
		{"cleanup", errors.Join(ErrSealBudget, ErrSealCleanup), ErrSealCleanup},
		{"uncertain", errors.Join(ErrSealBudget, ErrSealCleanup, ErrSealUncertain), ErrSealUncertain},
	} {
		test.Run(scenario.name, func(test *testing.T) {
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			err := bootstrap.WithFile(ctx, func(*os.File) error {
				cancel()
				return scenario.callback
			})
			if err != scenario.expected {
				test.Fatal("controlled cleanup outcome was overridden", err)
			}
		})
	}
}

func TestSealPageReopenCleanupPriority(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	ref := artifact.Ref().read()
	digest := sha256.Sum256([]byte("synthetic bounded plaintext"))
	record := SealExtentRecord{Kind: "extent", FileKind: "database", FileID: "database", PlaintextBytes: 27, PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])}
	state := engine.get()
	diskBefore := state.disk
	originalClose, originalSync := state.ops.close, state.ops.sync
	closes, removed := 0, false
	state.ops.close = func(file *os.File) error {
		err := originalClose(file)
		if strings.HasPrefix(filepath.Base(file.Name()), "m-") {
			closes++
		}
		return err
	}
	state.ops.sync = func(file *os.File) error {
		if file == state.directory.root && closes == 2 && !removed {
			removed = true
			if err := os.Remove(filepath.Join(file.Name(), sealObjectName(attempt.get().identity, 2, 1))); err != nil {
				test.Fatal(err)
			}
		}
		return originalSync(file)
	}
	defer func() { state.ops.close, state.ops.sync = originalClose, originalSync }()
	page, err := engine.SealPage(test.Context(), attempt, 1, 1, func(yield func(SealExtentRecord, error) bool) { yield(record, nil) })
	if page.get != nil || !removed || err != ErrSealCleanup || !state.uncertain || state.disk <= diskBefore || state.active != 0 || attempt.get().active != 0 {
		test.Fatal("page reopen hid failed artifact cleanup", err)
	}
}

func TestSealFailedDirectoryOwnership(test *testing.T) {
	engine, _, _ := sealFixture(test)
	directory, err := newSealDirectory(engine.get().directory.parent)
	if err != nil {
		test.Fatal(err)
	}
	if err = directory.root.Close(); err != nil {
		test.Fatal(err)
	}
	sealOrphans.Lock()
	before := len(sealOrphans.roots)
	sealOrphans.Unlock()
	err = discardSealDirectory(directory)
	sealOrphans.Lock()
	retained := len(sealOrphans.roots) == before+1 && sealOrphans.roots[before] == directory
	sealOrphans.Unlock()
	if err == nil || !retained || !directory.failed {
		test.Fatal("failed constructor cleanup lost physical ownership")
	}
	if _, err = directory.parent.Stat(); err != nil {
		test.Fatal("uncertain parent capability was not retained")
	}
}

func TestSealMetadataCleanupRetention(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	ref := artifact.Ref().read()
	digest := sha256.Sum256([]byte("synthetic bounded plaintext"))
	record := SealExtentRecord{Kind: "extent", FileKind: "database", FileID: "database", PlaintextBytes: 27, PlaintextSHA256: hex.EncodeToString(digest[:]), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])}
	state := engine.get()
	diskBefore := state.disk
	originalClose, originalSync := state.ops.close, state.ops.sync
	closes := 0
	state.ops.close = func(file *os.File) error {
		err := originalClose(file)
		if strings.HasPrefix(filepath.Base(file.Name()), "m-") {
			closes++
		}
		return err
	}
	state.ops.sync = func(file *os.File) error {
		if closes == 2 && file == state.directory.root {
			return ErrSealCleanup
		}
		return originalSync(file)
	}
	defer func() { state.ops.close, state.ops.sync = originalClose, originalSync }()
	page, err := engine.SealPage(test.Context(), attempt, 1, 1, func(yield func(SealExtentRecord, error) bool) { yield(record, nil) })
	if err == nil || page.get != nil || state.disk-diskBefore < 1048384 || !state.uncertain {
		if page.get != nil {
			_ = page.Close()
		}
		test.Fatal("unconfirmed metadata cleanup lost ownership or quota")
	}
}

func TestSealMetadataPanicCleanup(test *testing.T) {
	engine, _, source := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	artifact, err := engine.SealExtent(test.Context(), attempt, 0, source)
	if err != nil {
		test.Fatal(err)
	}
	defer artifact.Close()
	observed := false
	func() {
		defer func() { observed = recover() != nil }()
		_, _ = engine.SealPage(test.Context(), attempt, 1, 1, func(func(SealExtentRecord, error) bool) { panic("synthetic-iterator") })
	}()
	if !observed {
		test.Fatal("iterator panic swallowed")
	}
	files, err := os.ReadDir(engine.get().directory.root.Name())
	if err != nil {
		test.Fatal(err)
	}
	for _, file := range files {
		if strings.HasPrefix(file.Name(), "m-") {
			test.Fatal("panic leaked owned metadata partial")
		}
	}
	if engine.get().active != 0 || attempt.get().active != 0 {
		test.Fatal("panic leaked operation scope")
	}
	if !attempt.get().poison {
		test.Fatal("panic allowed reuse of incomplete metadata attempt")
	}
}

func TestSealFullLogicalClosure(test *testing.T) {
	engine, key, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = bootstrap.Close() })
	type input struct {
		kind, id string
		content  []byte
		size     int64
	}
	archiveInputs := []input{{kind: "archive", content: []byte("synthetic archive one")}, {kind: "archive", content: []byte("synthetic archive two")}}
	for index := range archiveInputs {
		hash := sha256.Sum256(archiveInputs[index].content)
		archiveInputs[index].id = hex.EncodeToString(hash[:])
		archiveInputs[index].size = int64(len(archiveInputs[index].content))
	}
	slices.SortFunc(archiveInputs, func(left, right input) int { return strings.Compare(left.id, right.id) })
	inputs := append([]input{{kind: "database", id: "database", size: 67108864}, {kind: "database", id: "database", size: 4, content: []byte("tail")}, {kind: "inventory", id: "inventory", size: 9, content: []byte("inventory")}}, archiveInputs...)
	databaseHash, inventoryHash := sha256.New(), sha256.New()
	var records []SealExtentRecord
	var dataFiles []SealedFile
	for ordinal, entry := range inputs {
		name := filepath.Join(test.TempDir(), "source")
		if err = os.WriteFile(name, entry.content, 0600); err != nil {
			test.Fatal(err)
		}
		if err = os.Truncate(name, entry.size); err != nil {
			test.Fatal(err)
		}
		source, err := os.Open(name)
		if err != nil {
			test.Fatal(err)
		}
		digest := sha256.New()
		sink := io.Writer(digest)
		if entry.kind == "database" {
			sink = io.MultiWriter(digest, databaseHash)
		} else if entry.kind == "inventory" {
			sink = io.MultiWriter(digest, inventoryHash)
		}
		if _, err = io.CopyBuffer(sink, source, make([]byte, 32768)); err != nil {
			test.Fatal(err)
		}
		artifact, err := engine.SealExtent(test.Context(), attempt, uint32(ordinal), source)
		closeErr := source.Close()
		if err != nil || closeErr != nil {
			test.Fatal("logical extent", err, closeErr)
		}
		test.Cleanup(func() { _ = artifact.Close() })
		dataFiles = append(dataFiles, artifact)
		ref := artifact.Ref().read()
		offset := int64(0)
		if ordinal == 1 {
			offset = 67108864
		}
		records = append(records, SealExtentRecord{Kind: "extent", Ordinal: uint32(ordinal), FileKind: entry.kind, FileID: entry.id, Offset: offset, PlaintextBytes: entry.size, PlaintextSHA256: hex.EncodeToString(digest.Sum(nil)), CiphertextBytes: ref.size, CiphertextSHA256: hex.EncodeToString(ref.digest[:])})
	}
	var pages []SealedFile
	for index, rows := range [][]SealExtentRecord{records[:3], records[3:]} {
		page, err := engine.SealPage(test.Context(), attempt, uint32(index+1), uint32(len(rows)), func(yield func(SealExtentRecord, error) bool) {
			for _, row := range rows {
				if !yield(row, nil) {
					return
				}
			}
		})
		if err != nil {
			test.Fatal(err)
		}
		test.Cleanup(func() { _ = page.Close() })
		pages = append(pages, page)
	}
	schema := sha256.Sum256([]byte("synthetic-schema"))
	root, err := engine.SealRoot(test.Context(), attempt, SealMetadata{AppVersion: "synthetic", SchemaSHA256: hex.EncodeToString(schema[:]), CutID: strings.Repeat("01", 16), MasterKeyID: strings.Repeat("A", 43), Database: SealFileDescription{Bytes: 67108868, SHA256: hex.EncodeToString(databaseHash.Sum(nil)), Extents: 2}, Inventory: SealInventoryDescription{Bytes: 9, SHA256: hex.EncodeToString(inventoryHash.Sum(nil)), Extents: 1, Records: 2, Owners: 2}, Archives: 2, ClosureSHA256: hex.EncodeToString(schema[:]), ExternalConfigNames: []string{"MASTER_KEY_FILE"}})
	if err != nil {
		test.Fatal("full root", err)
	}
	test.Cleanup(func() { _ = root.Close() })
	complete, err := engine.SealComplete(test.Context(), attempt)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = complete.Close() })
	identity := bootstrap.Ref().read().attempt
	expected, _ := NewAttempt(identity.instance, identity.backup, identity.attempt)
	var session OpenSession
	err = bootstrap.WithFile(test.Context(), func(first *os.File) error {
		return complete.WithFile(test.Context(), func(last *os.File) error {
			var openErr error
			session, openErr = engine.BeginOpen(test.Context(), first, last, expected)
			return openErr
		})
	})
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = session.Close() })
	all := append([]SealedFile{root}, pages...)
	all = append(all, dataFiles...)
	for _, file := range all {
		next, err := session.NextRef(test.Context())
		if err != nil || next.read == nil || next.read() != file.Ref().read() {
			test.Fatal("ordered pins differ")
		}
		if err = file.WithFile(test.Context(), func(source *os.File) error { return session.AcceptFile(test.Context(), source) }); err != nil {
			test.Fatal("closure open", err)
		}
	}
	bundle, err := session.Finish(test.Context())
	if err != nil {
		test.Fatal("full Finish", err)
	}
	digest := sha256.New()
	count, err := bundle.CopyFile(test.Context(), "database", "database", digest)
	if err != nil || count != 67108868 || !bytes.Equal(digest.Sum(nil), databaseHash.Sum(nil)) {
		test.Fatal("whole reconstructed multi-extent hash", err)
	}
	for _, entry := range archiveInputs {
		var output bytes.Buffer
		if _, err = bundle.CopyFile(test.Context(), "archive", entry.id, &output); err != nil || !bytes.Equal(output.Bytes(), entry.content) {
			test.Fatal("archive closure", err)
		}
	}
	metadata, err := bundle.Metadata(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	metadata.ExternalConfigNames[0] = "TAINT"
	again, err := bundle.Metadata(test.Context())
	if err != nil || again.ExternalConfigNames[0] != "MASTER_KEY_FILE" {
		test.Fatal("metadata clone alias")
	}
	if err = key.Destroy(); err != nil {
		test.Fatal("Destroy did not reclaim plaintext", err)
	}
	if _, err = bundle.Metadata(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("Destroy left bundle live")
	}
	if attempt.get().dek != [32]byte{} || key.get().material != [32]byte{} {
		test.Fatal("owned raw key bytes not cleared")
	}
	if err = bootstrap.WithFile(test.Context(), func(file *os.File) error {
		info, err := file.Stat()
		if err != nil || info.Size() != 156 {
			return ErrInvalid
		}
		return nil
	}); err != nil {
		test.Fatal("Destroy discarded transferred exact ciphertext", err)
	}
}

func TestSealAuthenticatedLeafStrict(test *testing.T) {
	engine, _, _ := sealFixture(test)
	owner := engine.get()
	identity := attemptData{instance: [16]byte{1}, backup: [16]byte{2}, attempt: [16]byte{3}}
	secret := bytes.Repeat([]byte{31}, 32)
	hash := sha256.Sum256([]byte("x"))
	digest := hex.EncodeToString(hash[:])
	valid := fmt.Sprintf(`{"kind":"page","version":1,"ordinal":1,"records":2}`+"\n"+`{"kind":"extent","ordinal":0,"file_kind":"database","file_id":"database","offset":0,"plaintext_bytes":1,"plaintext_sha256":"%s","ciphertext_bytes":193,"ciphertext_sha256":"%s"}`+"\n"+`{"kind":"extent","ordinal":1,"file_kind":"inventory","file_id":"inventory","offset":0,"plaintext_bytes":1,"plaintext_sha256":"%s","ciphertext_bytes":193,"ciphertext_sha256":"%s"}`+"\n", digest, digest, digest, digest)
	cases := []struct{ name, raw string }{{"valid", valid}, {"missing-count", strings.Replace(valid, `,"records":2`, "", 1)}, {"null", strings.Replace(valid, `"offset":0`, `"offset":null`, 1)}, {"unknown", strings.Replace(valid, `"offset":0`, `"offset":0,"extra":false`, 1)}, {"duplicate", strings.Replace(valid, `"offset":0`, `"offset":0,"offset":0`, 1)}, {"ordinal-gap", strings.Replace(valid, `"ordinal":0`, `"ordinal":9`, 1)}, {"offset-gap", strings.Replace(valid, `"offset":0`, `"offset":1`, 1)}, {"unknown-kind", strings.Replace(valid, `"file_kind":"database"`, `"file_kind":"untrusted"`, 1)}, {"unknown-file", strings.Replace(valid, `"file_id":"database"`, `"file_id":"../../bad"`, 1)}, {"extra-line", valid + "{}\n"}, {"missing-LF", strings.TrimSuffix(valid, "\n")}, {"exponent", strings.Replace(valid, `"offset":0`, `"offset":0e0`, 1)}, {"count-short", strings.Replace(valid, `"records":2`, `"records":1`, 1)}}
	for index, example := range cases {
		test.Run(example.name, func(test *testing.T) {
			wire, err := independentObject(identity, 2, 1, []byte(example.raw), secret)
			if err != nil {
				test.Fatal(err)
			}
			sourceName := filepath.Join(test.TempDir(), "authenticated-page")
			if err = os.WriteFile(sourceName, wire, 0600); err != nil {
				test.Fatal(err)
			}
			source, err := os.Open(sourceName)
			if err != nil {
				test.Fatal(err)
			}
			defer source.Close()
			catalogName := fmt.Sprintf("leaf-index-%d", index)
			catalog, err := sealOpen(owner.directory, catalogName, true)
			if err != nil {
				test.Fatal(err)
			}
			defer func() { _ = catalog.Close(); _ = sealUnlink(owner.directory, catalogName) }()
			state := &sealOpenState{engine: owner, ctx: test.Context(), identity: identity, prefix: fmt.Sprintf("leaf-%d-", index), catalog: catalog, dataCount: 2}
			copy(state.dek[:], secret)
			cipherHash := sha256.Sum256(wire)
			name, charge, _, err := state.decryptPrivate(test.Context(), source, 2, 1, int64(len(wire)), cipherHash)
			if err != nil {
				test.Fatal("authenticated metadata seed did not reach structural checker", err)
			}
			defer charge.release()
			defer sealUnlink(owner.directory, name)
			input, err := sealOpen(owner.directory, name, false)
			if err != nil {
				test.Fatal(err)
			}
			defer input.Close()
			err = state.readPage(input, 1)
			if example.name == "valid" {
				if err != nil || state.order.next != 2 {
					test.Fatal("positive typed page", err)
				}
			} else if err == nil {
				test.Fatal("authenticated malformed leaf admitted")
			}
		})
	}
}

func TestSealAuthenticatedRootStrict(test *testing.T) {
	engine, _, _ := sealFixture(test)
	attempt, bootstrap, err := engine.BeginAttempt(test.Context(), [16]byte{1}, [16]byte{2})
	if err != nil {
		test.Fatal(err)
	}
	defer bootstrap.Close()
	secret := independentUnwrap(test, bootstrap)
	defer clear(secret)
	identity := attempt.get().identity
	digest := strings.Repeat("1", 64)
	valid := fmt.Sprintf(`{"format":"cognate-backup-root","version":1,"suite":1,"app_version":"test","schema_sha256":"%s","instance_id":"%x","backup_id":"%x","attempt_id":"%x","scope":"instance","cut_id":"01000000000000000000000000000000","cut_mode":"managed-exclusive-staged","cache_policy":"authoritative-closure","master_key_id":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA","database":{"bytes":1,"sha256":"%s","extents":1},"inventory":{"bytes":1,"sha256":"%s","extents":1,"records":1,"owners":1},"archives":0,"closure_sha256":"%s","external_config_names":["MASTER_KEY_FILE"],"totals":{"data_objects":2,"manifest_objects":2,"plaintext_bytes":5000,"ciphertext_bytes_without_complete":10000},"pages":[{"ordinal":1,"ciphertext_bytes":256,"ciphertext_sha256":"%s"}]}`+"\n", digest, identity.instance, identity.backup, identity.attempt, digest, digest, digest, digest)
	cases := []struct{ name, raw string }{
		{"valid", valid},
		{"missing", strings.Replace(valid, `"suite":1,`, "", 1)},
		{"null", strings.Replace(valid, `"scope":"instance"`, `"scope":null`, 1)},
		{"unknown", strings.Replace(valid, `"suite":1,`, `"suite":1,"unknown":false,`, 1)},
		{"duplicate", strings.Replace(valid, `"suite":1,`, `"suite":1,"suite":1,`, 1)},
		{"exponent", strings.Replace(valid, `"version":1`, `"version":1e0`, 1)},
		{"overflow", strings.Replace(valid, `"data_objects":2`, `"data_objects":4294967296`, 1)},
		{"wrong-instance", strings.Replace(valid, `"instance_id":"01`, `"instance_id":"02`, 1)},
		{"wrong-mode", strings.Replace(valid, `managed-exclusive-staged`, `unmanaged`, 1)},
		{"noncanonical-master", strings.Replace(valid, `AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA`, strings.Repeat("a", 64), 1)},
		{"config-order", strings.Replace(valid, `["MASTER_KEY_FILE"]`, `["Z","A"]`, 1)},
		{"config-duplicate", strings.Replace(valid, `["MASTER_KEY_FILE"]`, `["A","A"]`, 1)},
		{"config-null", strings.Replace(valid, `["MASTER_KEY_FILE"]`, `null`, 1)},
		{"page-gap", strings.Replace(valid, `"ordinal":1`, `"ordinal":2`, 1)},
		{"page-presence", strings.Replace(valid, `"ordinal":1,`, "", 1)},
		{"manifest-count", strings.Replace(valid, `"manifest_objects":2`, `"manifest_objects":1`, 1)},
		{"cipher-count", strings.Replace(valid, `"ciphertext_bytes_without_complete":10000`, `"ciphertext_bytes_without_complete":9999`, 1)},
		{"extra-line", valid + "{}\n"},
		{"extra-LF", valid + "\n"},
	}
	for _, example := range cases {
		test.Run(example.name, func(test *testing.T) {
			wire, err := independentObject(identity, 3, 0, []byte(example.raw), secret)
			if err != nil {
				test.Fatal(err)
			}
			rootHash := sha256.Sum256(wire)
			bootstrapHash := bootstrap.Ref().read().digest
			completePlain := fmt.Sprintf(`{"format":"cognate-backup-complete","version":1,"suite":1,"instance_id":"%x","backup_id":"%x","attempt_id":"%x","bootstrap_bytes":156,"bootstrap_sha256":"%x","root_ciphertext_bytes":%d,"root_ciphertext_sha256":"%x","object_count_without_complete":5,"ciphertext_bytes_without_complete":10000}`+"\n", identity.instance, identity.backup, identity.attempt, bootstrapHash, len(wire), rootHash)
			completeWire, err := independentObject(identity, 4, 0, []byte(completePlain), secret)
			if err != nil {
				test.Fatal(err)
			}
			var inputs [2]*os.File
			for index, content := range [][]byte{wire, completeWire} {
				name := filepath.Join(test.TempDir(), "authenticated-input")
				if err = os.WriteFile(name, content, 0600); err != nil {
					test.Fatal(err)
				}
				inputs[index], err = os.Open(name)
				if err != nil {
					test.Fatal(err)
				}
				defer inputs[index].Close()
			}
			var session OpenSession
			err = bootstrap.WithFile(test.Context(), func(file *os.File) error {
				var openErr error
				session, openErr = engine.BeginOpen(test.Context(), file, inputs[1], Attempt{read: func() attemptData { return identity }})
				return openErr
			})
			if err != nil {
				test.Fatal("independent complete seed did not reach root", err)
			}
			defer session.Close()
			err = session.AcceptFile(test.Context(), inputs[0])
			if example.name == "valid" {
				if err != nil {
					test.Fatal("independently authenticated valid root refused", err)
				}
			} else if err == nil {
				test.Fatal("authenticated malformed root admitted")
			}
			if bundle, err := session.Finish(test.Context()); err == nil || bundle.get != nil {
				test.Fatal("root alone exposed incomplete bundle")
			}
		})
	}
}

func TestSealRootDirectoryBudget(test *testing.T) {
	engine, _, _ := sealFixture(test)
	owner := engine.get()
	catalog, err := sealOpen(owner.directory, "root-budget-index", true)
	if err != nil {
		test.Fatal(err)
	}
	defer func() { _ = catalog.Close(); _ = sealUnlink(owner.directory, "root-budget-index") }()
	attempt := &sealAttempt{engine: owner, catalog: catalog, pageCount: 10000}
	for ordinal := uint32(1); ordinal <= 10000; ordinal++ {
		if err = attempt.addRecord(sealRecord{ordinal: ordinal, cipherBytes: 256, cipherHash: [32]byte{1}}, 1); err != nil {
			test.Fatal(err)
		}
	}
	counter := &sealCountWriter{}
	err = writeSealRoot(test.Context(), &sealLimitWriter{ctx: test.Context(), writer: counter, remaining: 1048384}, sealRoot{ExternalConfigNames: []string{}}, attempt)
	if !errors.Is(err, ErrSealBudget) || counter.count > 1048384 {
		test.Fatal("natural root directory overflow was not bounded", err)
	}
}
