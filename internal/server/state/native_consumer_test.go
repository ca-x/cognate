package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
)

type nativeFixture struct {
	store  *Store
	path   string
	key    []byte
	clock  *atomic.Int64
	parent OwnerSessionCredential
	digest [32]byte
}

func newNativeFixture(test *testing.T) nativeFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "native.db")
	store, err := Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	clock := new(atomic.Int64)
	clock.Store(time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC).UnixNano())
	store.state.now = func() time.Time { return time.Unix(0, clock.Load()).UTC() }
	fixture := nativeFixture{store: store, path: path, key: key, clock: clock}
	fixture.parent, fixture.digest = nativeTestParent(test, store, "alice", store.state.now().Add(time.Hour))
	return fixture
}

func nativeTestParent(test *testing.T, store *Store, owner string, expiry time.Time) (OwnerSessionCredential, [32]byte) {
	test.Helper()
	entry, err := findUser(test.Context(), store.state.client, owner)
	if ent.IsNotFound(err) {
		if err := store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
		entry, err = findUser(test.Context(), store.state.client, owner)
	}
	if err != nil {
		test.Fatal(err)
	}
	var digest [32]byte
	if _, err := rand.Read(digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := store.state.client.Session.Create().SetUserID(entry.ID).SetTokenHash(digest[:]).SetIssuedAt(store.state.now().Add(-time.Second)).SetExpiresAt(expiry).Save(test.Context()); err != nil {
		test.Fatal(err)
	}
	return OwnerSessionCredential{nativeWrap(digest)}, digest
}

func nativeTestStart() NativeConsumerStart {
	return NativeConsumerStart{ExpectedOwner: "alice", Origin: "https://cognate.example", Purpose: NativeConfigurationApply, OperationID: bytes.Repeat([]byte{1}, 16), PublicKey: bytes.Repeat([]byte{2}, 32), ClientNonce: bytes.Repeat([]byte{3}, 32)}
}

func nativeTestBind(challenge NativeConsumerChallenge, digest [32]byte) NativeConsumerBind {
	return NativeConsumerBind{ChallengeID: challenge.ID(), TokenHash: NativeConsumerCredential{nativeWrap([32]byte(bytes.Repeat([]byte{4}, 32)))}, Proof: NativeConsumerBindingProof{nativeWrap(nativeBindEvidence{ParentHash: digest, Challenge: *challenge.box.value})}}
}

func TestNativeConsumerLifecycleAndRealRevoke(test *testing.T) {
	fixture := newNativeFixture(test)
	start := nativeTestStart()
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start)
	if err != nil {
		test.Fatal(err)
	}
	bound, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest))
	if err != nil {
		test.Fatal(err)
	}
	credential := NativeConsumerCredential{nativeWrap([32]byte(bytes.Repeat([]byte{4}, 32)))}
	identity, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, start.Origin, start.OperationID)
	if err != nil || identity.Owner() != "alice" || bound.ID() == "" {
		test.Fatalf("valid child rejected: %v", err)
	}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest)); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("challenge reused: %v", err)
	}
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, start.Origin, start.OperationID); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("revoked child accepted: %v", err)
	}
	for _, count := range nativeTestCounts(test, fixture.store) {
		if count != 0 {
			test.Fatal("revoke left a native descendant")
		}
	}
}

func nativeTestCounts(test *testing.T, store *Store) []int {
	test.Helper()
	challenges, err := store.state.client.NativeConsumerChallenge.Query().Count(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	children, err := store.state.client.NativeConsumerSession.Query().Count(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	return []int{challenges, children}
}

func TestNativeConsumerRejectsZeroAndOwnerSubstitution(test *testing.T) {
	fixture := newNativeFixture(test)
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), OwnerSessionCredential{}, nativeTestStart()); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("zero credential accepted: %v", err)
	}
	start := nativeTestStart()
	start.ExpectedOwner = "bob"
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("owner substitution: %v", err)
	}
	for _, count := range nativeTestCounts(test, fixture.store) {
		if count != 0 {
			test.Fatal("invalid identity wrote state")
		}
	}
}

func TestNativeConsumerDefaultChallengeQuota(test *testing.T) {
	fixture := newNativeFixture(test)
	for range 8 {
		if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); err != nil {
			test.Fatal(err)
		}
	}
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("parent quota bypassed: %v", err)
	}
	fixture.clock.Add(int64(time.Minute))
	pruned, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || pruned.Rows != 8 {
		test.Fatalf("expiry prune: rows=%d err=%v", pruned.Rows, err)
	}
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); err != nil {
		test.Fatal(err)
	}
}

func TestNativeConsumerCommitClockGate(test *testing.T) {
	fixture := newNativeFixture(test)
	expiry := fixture.store.state.now().Add(time.Second)
	fixture.parent, fixture.digest = nativeTestParent(test, fixture.store, "short", expiry)
	fixture.store.state.client.NativeConsumerChallenge.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			fixture.clock.Store(expiry.UnixNano())
			return value, err
		})
	})
	start := nativeTestStart()
	start.ExpectedOwner = "short"
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("expired at commit: %v", err)
	}
	for _, count := range nativeTestCounts(test, fixture.store) {
		if count != 0 {
			test.Fatal("commit gate did not rollback")
		}
	}
}
