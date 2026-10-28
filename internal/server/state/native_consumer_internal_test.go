package state

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/nativeconsumerchallenge"
)

func nativePeer(test *testing.T, fixture nativeFixture) *Store {
	test.Helper()
	peer, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = peer.Close() })
	peer.state.now = fixture.store.state.now
	return peer
}

func nativeTestChild(test *testing.T, fixture nativeFixture) (NativeConsumerChallenge, NativeConsumerCredential) {
	test.Helper()
	start := nativeTestStart()
	if _, err := rand.Read(start.OperationID); err != nil {
		test.Fatal(err)
	}
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start)
	if err != nil {
		test.Fatal(err)
	}
	input := nativeTestBind(challenge, fixture.digest)
	var digest [32]byte
	if _, err := rand.Read(digest[:]); err != nil {
		test.Fatal(err)
	}
	input.TokenHash = NativeConsumerCredential{nativeWrap(digest)}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input); err != nil {
		test.Fatal(err)
	}
	return challenge, input.TokenHash
}

func nativeAssertUnauthorized(test *testing.T, err error) {
	test.Helper()
	if !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatalf("expected rejected authorization, got %v", err)
	}
}

func TestNativeConsumerOpaqueFormattingAndSlog(test *testing.T) {
	fixture := newNativeFixture(test)
	challenge, credential := nativeTestChild(test, fixture)
	identity, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", challenge.box.value.OperationID[:])
	if err != nil {
		test.Fatal(err)
	}
	values := []any{fixture.parent, credential, challenge, identity, nativeTestBind(challenge, fixture.digest).Proof, nativeTestBind(challenge, fixture.digest)}
	sentinel := []byte("synthetic-native-secret-0123456789")
	sentinel = sentinel[:32]
	copy(fixture.parent.box.value[:], sentinel)
	copy(challenge.box.value.Challenge[:], sentinel)
	for _, value := range values {
		for _, container := range []any{value, &value, struct{ hidden any }{value}, struct{ Value any }{value}, []any{value}, map[string]any{"value": value}} {
			for _, verb := range "vTtbcdoOqxXUeEfFgGspw" {
				for _, format := range []string{"%" + string(verb), "%+" + string(verb), "%#" + string(verb), "%020.10" + string(verb)} {
					rendered := fmt.Sprintf(format, container)
					for _, secret := range []string{string(sentinel), fmt.Sprintf("%x", sentinel), fmt.Sprint(sentinel)} {
						if strings.Contains(rendered, secret) {
							test.Fatal("fmt leaked synthetic wrapper content")
						}
					}
				}
			}
			for _, json := range []bool{false, true} {
				var output bytes.Buffer
				var handler slog.Handler = slog.NewTextHandler(&output, nil)
				if json {
					handler = slog.NewJSONHandler(&output, nil)
				}
				slog.New(handler).Info("synthetic", slog.Any("value", container))
				if strings.Contains(output.String(), string(sentinel)) || strings.Contains(output.String(), fmt.Sprintf("%x", sentinel)) {
					test.Fatal("slog leaked synthetic wrapper content")
				}
			}
		}
	}
}

func TestNativeConsumerFourPhysicalCascadeConstraints(test *testing.T) {
	fixture := newNativeFixture(test)
	nativeTestChild(test, fixture)
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	for _, table := range []string{"native_consumer_challenges", "native_consumer_sessions"} {
		rows, err := database.QueryContext(test.Context(), "PRAGMA foreign_key_list("+table+")")
		if err != nil {
			test.Fatal(err)
		}
		count := 0
		for rows.Next() {
			var id, sequence int
			var target, from, to, update, deletion, match string
			if err := rows.Scan(&id, &sequence, &target, &from, &to, &update, &deletion, &match); err != nil {
				test.Fatal(err)
			}
			if deletion != "CASCADE" || target != "users" && target != "sessions" {
				test.Fatal("native FK is not an expected CASCADE")
			}
			count++
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			test.Fatal(err)
		}
		if count != 2 {
			test.Fatalf("expected two native FKs, got %d", count)
		}
	}
	rows, err := database.QueryContext(test.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	if rows.Next() || rows.Err() != nil {
		test.Fatal("foreign key integrity failed")
	}
}

func TestNativeConsumerSixteenSequentialChildrenThenQuota(test *testing.T) {
	fixture := newNativeFixture(test)
	for range 16 {
		nativeTestChild(test, fixture)
	}
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest))
	if !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("child parent cap bypassed: %v", err)
	}
	row, err := fixture.store.state.client.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.PublicIDEQ(challenge.ID())).Only(test.Context())
	if err != nil || row.Used {
		test.Fatal("quota failure consumed the challenge")
	}
	if count := nativeTestCounts(test, fixture.store)[1]; count != 16 {
		test.Fatalf("wrong retained child count %d", count)
	}
}

func TestNativeConsumerOwnerAndInstanceChallengeBudgets(test *testing.T) {
	fixture := newNativeFixture(test)
	for ownerIndex := range 4 {
		owner := fmt.Sprintf("owner-%d", ownerIndex)
		for range 4 {
			parent, _ := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
			start := nativeTestStart()
			start.ExpectedOwner = owner
			for range 8 {
				if _, err := fixture.store.BeginNativeConsumer(test.Context(), parent, start); err != nil {
					test.Fatal(err)
				}
			}
		}
		parent, _ := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
		start := nativeTestStart()
		start.ExpectedOwner = owner
		if _, err := fixture.store.BeginNativeConsumer(test.Context(), parent, start); !errors.Is(err, ErrNativeQuota) {
			test.Fatalf("owner cumulative cap bypassed: %v", err)
		}
	}
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("instance cap bypassed: %v", err)
	}
	if count := nativeTestCounts(test, fixture.store)[0]; count != 128 {
		test.Fatalf("instance rows %d", count)
	}
	fixture.clock.Add(int64(time.Minute))
	first, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || first.Rows != 64 {
		test.Fatalf("first bounded prune: %+v %v", first, err)
	}
	second, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || second.Rows != 64 {
		test.Fatalf("second bounded prune: %+v %v", second, err)
	}
	if first.Bytes > 1<<20 || second.Bytes > 1<<20 {
		test.Fatal("prune exceeded byte budget")
	}
}

func TestNativeConsumerQuotaBytesTotalsAndCorruption(test *testing.T) {
	for _, scope := range []string{"table-bytes", "total-rows", "total-bytes", "row-bytes"} {
		test.Run(scope, func(test *testing.T) {
			fixture := newNativeFixture(test)
			budget := nativeDefaultBudget()
			switch scope {
			case "table-bytes":
				budget.tables[0][0].bytes = 512
			case "total-rows":
				budget.totals[2].rows = 0
			case "total-bytes":
				budget.totals[1].bytes = 512
			case "row-bytes":
				budget.rowBytes[0] = 512
			}
			if _, err := fixture.store.beginNativeConsumer(test.Context(), fixture.parent, nativeTestStart(), budget); !errors.Is(err, ErrNativeQuota) {
				test.Fatalf("byte/total gate bypassed: %v", err)
			}
			if nativeTestCounts(test, fixture.store)[0] != 0 {
				test.Fatal("quota failure left a row")
			}
		})
	}
	fixture := newNativeFixture(test)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE native_consumer_challenges SET charge=charge-1 WHERE public_id=?", challenge.ID()); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); !errors.Is(err, ErrNativeUnavailable) {
		test.Fatalf("corrupt accounting accepted: %v", err)
	}
}

func TestNativeConsumerParentZoneAndNanosecondBoundary(test *testing.T) {
	for _, offset := range []time.Duration{-time.Nanosecond, 0, time.Nanosecond} {
		test.Run(offset.String(), func(test *testing.T) {
			fixture := newNativeFixture(test)
			expiry := fixture.store.state.now().Add(90 * time.Second).In(time.FixedZone("FST", 14*3600))
			fixture.parent, fixture.digest = nativeTestParent(test, fixture.store, "alice", expiry)
			challenge, credential := nativeTestChild(test, fixture)
			fixture.clock.Store(expiry.Add(offset).UnixNano())
			identity, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", challenge.box.value.OperationID[:])
			if offset < 0 {
				if err != nil || identity.Owner() != "alice" {
					test.Fatalf("instant comparison rejected valid timezone: %v", err)
				}
			} else {
				nativeAssertUnauthorized(test, err)
			}
		})
	}
}

func TestNativeConsumerChildExpiryAndOwnerIsolation(test *testing.T) {
	fixture := newNativeFixture(test)
	challenge, credential := nativeTestChild(test, fixture)
	bob, bobHash := nativeTestParent(test, fixture.store, "bob", fixture.store.state.now().Add(time.Hour))
	input := nativeTestBind(challenge, fixture.digest)
	_, err := fixture.store.BindNativeConsumer(test.Context(), bob, input)
	nativeAssertUnauthorized(test, err)
	input.Proof.box.value.ParentHash = bobHash
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input)
	nativeAssertUnauthorized(test, err)
	if _, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://other.example", challenge.box.value.OperationID[:]); !errors.Is(err, ErrNativeUnauthorized) {
		test.Fatal("origin substitution accepted")
	}
	if err := fixture.store.RevokeSession(test.Context(), bobHash[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", challenge.box.value.OperationID[:]); err != nil {
		test.Fatal("other owner revoke affected child")
	}
	fixture.clock.Add(int64(5 * time.Minute))
	_, err = fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", challenge.box.value.OperationID[:])
	nativeAssertUnauthorized(test, err)
	pruned, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || pruned.Rows != 2 {
		test.Fatalf("expired child/challenge prune: %+v %v", pruned, err)
	}
}

func TestNativeConsumerCommitCancellationAndUnknown(test *testing.T) {
	for _, mode := range []string{"cancel-before", "expire-before", "error-after-commit"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			var commits atomic.Int64
			fixture.store.state.client.NativeConsumerChallenge.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					tx, err := mutation.(*ent.NativeConsumerChallengeMutation).Tx()
					if err != nil {
						return nil, err
					}
					tx.OnCommit(func(next ent.Committer) ent.Committer {
						return ent.CommitFunc(func(ctx context.Context, tx *ent.Tx) error {
							commits.Add(1)
							if mode == "cancel-before" {
								cancel()
							}
							if mode == "expire-before" {
								fixture.clock.Add(int64(time.Hour))
							}
							if err := next.Commit(ctx, tx); err != nil {
								return err
							}
							return errors.New("synthetic-commit-error-must-not-echo")
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			result, err := fixture.store.BeginNativeConsumer(ctx, fixture.parent, nativeTestStart())
			if result.ID() != "" || commits.Load() != 1 {
				test.Fatal("failed commit returned authority or retried")
			}
			switch mode {
			case "cancel-before":
				if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNativeOutcomeUnknown) {
					test.Fatalf("cancel result: %v", err)
				}
			case "expire-before":
				nativeAssertUnauthorized(test, err)
			default:
				if !errors.Is(err, ErrNativeOutcomeUnknown) || strings.Contains(err.Error(), "must-not-echo") {
					test.Fatalf("commit uncertainty was hidden or echoed: %v", err)
				}
			}
			want := 0
			if mode == "error-after-commit" {
				want = 1
			}
			if nativeTestCounts(test, fixture.store)[0] != want {
				test.Fatal("commit/rollback reality mismatched")
			}
		})
	}
}

func nativeQueryBarrier(arrived chan<- struct{}, resume <-chan struct{}) ent.Interceptor {
	var once atomic.Bool
	return ent.InterceptFunc(func(next ent.Querier) ent.Querier {
		return ent.QuerierFunc(func(ctx context.Context, query ent.Query) (ent.Value, error) {
			value, err := next.Query(ctx, query)
			if err == nil && once.CompareAndSwap(false, true) {
				arrived <- struct{}{}
				select {
				case <-resume:
				case <-ctx.Done():
					return nil, ctx.Err()
				}
			}
			return value, err
		})
	})
}

func TestNativeConsumerMultiStoreLastSlotAndSameChallenge(test *testing.T) {
	for _, mode := range []string{"last-slot", "same-challenge"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			peer := nativePeer(test, fixture)
			var challenge NativeConsumerChallenge
			if mode == "last-slot" {
				for range 7 {
					if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); err != nil {
						test.Fatal(err)
					}
				}
			} else {
				var err error
				challenge, err = fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
				if err != nil {
					test.Fatal(err)
				}
			}
			arrived, resume := make(chan struct{}, 2), make(chan struct{})
			for _, store := range []*Store{fixture.store, peer} {
				store.state.client.Session.Intercept(nativeQueryBarrier(arrived, resume))
			}
			ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
			defer cancel()
			results := make(chan error, 2)
			var workers sync.WaitGroup
			for _, store := range []*Store{fixture.store, peer} {
				workers.Go(func() {
					var err error
					if mode == "last-slot" {
						_, err = store.BeginNativeConsumer(ctx, fixture.parent, nativeTestStart())
					} else {
						_, err = store.BindNativeConsumer(ctx, fixture.parent, nativeTestBind(challenge, fixture.digest))
					}
					results <- err
				})
			}
			for range 2 {
				select {
				case <-arrived:
				case <-ctx.Done():
					test.Fatal("both real transactions did not reach barrier")
				}
			}
			close(resume)
			workers.Wait()
			first, second := <-results, <-results
			if first == nil {
				first, second = second, first
			}
			want := ErrNativeQuota
			if mode == "same-challenge" {
				want = ErrNativeUnauthorized
			}
			if second != nil || !errors.Is(first, want) {
				test.Fatalf("contending transactions: %v / %v", first, second)
			}
		})
	}
}

func TestNativeConsumerLogoutOrPruneWinsBeforeBind(test *testing.T) {
	for _, mode := range []string{"logout", "prune"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			peer := nativePeer(test, fixture)
			challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
			if err != nil {
				test.Fatal(err)
			}
			arrived, resume := make(chan struct{}, 1), make(chan struct{})
			fixture.store.state.client.NativeConsumerChallenge.Intercept(nativeQueryBarrier(arrived, resume))
			ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
			defer cancel()
			result := make(chan error, 1)
			go func() {
				_, err := fixture.store.BindNativeConsumer(ctx, fixture.parent, nativeTestBind(challenge, fixture.digest))
				result <- err
			}()
			select {
			case <-arrived:
			case <-ctx.Done():
				test.Fatal("bind did not reach real read barrier")
			}
			if mode == "logout" {
				err = peer.RevokeSession(ctx, fixture.digest[:])
			} else {
				fixture.clock.Add(int64(time.Minute))
				_, err = peer.PruneNativeConsumers(ctx)
			}
			close(resume)
			if err != nil {
				test.Fatal(err)
			}
			nativeAssertUnauthorized(test, <-result)
			if nativeTestCounts(test, fixture.store)[1] != 0 {
				test.Fatal("bind persisted after concurrent revoke/prune")
			}
		})
	}
}

func TestNativeConsumerBindProofAndWriteFailureRollback(test *testing.T) {
	fixture := newNativeFixture(test)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	input := nativeTestBind(challenge, fixture.digest)
	input.Proof.box.value.Challenge.PublicKey[0] ^= 1
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input)
	nativeAssertUnauthorized(test, err)
	input = nativeTestBind(challenge, fixture.digest)
	input.Proof = NativeConsumerBindingProof{}
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input)
	nativeAssertUnauthorized(test, err)
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest))
	if err != nil {
		test.Fatal("rejected proof consumed the challenge")
	}
	start := nativeTestStart()
	start.OperationID[0]++
	next, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, start)
	if err != nil {
		test.Fatal(err)
	}
	_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(next, fixture.digest))
	nativeAssertUnauthorized(test, err)
	row, err := fixture.store.state.client.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.PublicIDEQ(next.ID())).Only(test.Context())
	if err != nil || row.Used || nativeTestCounts(test, fixture.store)[1] != 1 {
		test.Fatal("child uniqueness failure did not rollback challenge CAS")
	}
}

func TestNativeConsumerChildOwnerInstanceAndByteQuota(test *testing.T) {
	for _, mode := range []string{"owner", "instance", "bytes"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			nativeTestChild(test, fixture)
			budget := nativeDefaultBudget()
			owner := "alice"
			switch mode {
			case "owner":
				budget.tables[1][1].rows = 1
			case "instance":
				budget.tables[1][2].rows = 1
				owner = "bob"
			case "bytes":
				budget.tables[1][0].bytes = 512
			}
			parent, digest := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
			start := nativeTestStart()
			start.ExpectedOwner = owner
			challenge, err := fixture.store.BeginNativeConsumer(test.Context(), parent, start)
			if err != nil {
				test.Fatal(err)
			}
			_, err = fixture.store.bindNativeConsumer(test.Context(), parent, nativeTestBind(challenge, digest), budget)
			if !errors.Is(err, ErrNativeQuota) {
				test.Fatalf("child cumulative quota bypassed: %v", err)
			}
			row, err := fixture.store.state.client.NativeConsumerChallenge.Query().Where(nativeconsumerchallenge.PublicIDEQ(challenge.ID())).Only(test.Context())
			if err != nil || row.Used {
				test.Fatal("quota consumed challenge")
			}
		})
	}
}

func TestNativeConsumerBindAndAuthenticateCommitGate(test *testing.T) {
	for _, mode := range []string{"bind", "authenticate"} {
		test.Run(mode, func(test *testing.T) {
			fixture := newNativeFixture(test)
			challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
			if err != nil {
				test.Fatal(err)
			}
			credential := nativeTestBind(challenge, fixture.digest).TokenHash
			if mode == "authenticate" {
				if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest)); err != nil {
					test.Fatal(err)
				}
			}
			fixture.store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					value, err := next.Mutate(ctx, mutation)
					fixture.clock.Add(int64(5 * time.Minute))
					return value, err
				})
			})
			if mode == "bind" {
				_, err = fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest))
			} else {
				_, err = fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", challenge.box.value.OperationID[:])
			}
			nativeAssertUnauthorized(test, err)
			wantChildren := 0
			if mode == "authenticate" {
				wantChildren = 1
			}
			if nativeTestCounts(test, fixture.store)[1] != wantChildren {
				test.Fatal("commit clock rollback failed")
			}
		})
	}
}

func TestNativeConsumerBindWinsBeforeConcurrentLogout(test *testing.T) {
	fixture := newNativeFixture(test)
	peer := nativePeer(test, fixture)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	written, release := make(chan struct{}), make(chan struct{})
	ctx, cancel := context.WithTimeout(test.Context(), 5*time.Second)
	defer cancel()
	fixture.store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			close(written)
			select {
			case <-release:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
			return value, err
		})
	})
	bound, revoked := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := fixture.store.BindNativeConsumer(ctx, fixture.parent, nativeTestBind(challenge, fixture.digest))
		bound <- err
	}()
	select {
	case <-written:
	case <-ctx.Done():
		test.Fatal("bind failed to acquire SQLite writer")
	}
	started := make(chan struct{})
	go func() { close(started); revoked <- peer.RevokeSession(ctx, fixture.digest[:]) }()
	<-started
	close(release)
	if err := <-bound; err != nil {
		test.Fatal(err)
	}
	if err := <-revoked; err != nil {
		test.Fatal(err)
	}
	for _, count := range nativeTestCounts(test, fixture.store) {
		if count != 0 {
			test.Fatal("logout did not cascade the previously committed child")
		}
	}
}

func TestNativeConsumerPruneCancellationAndNoReverseCascade(test *testing.T) {
	fixture := newNativeFixture(test)
	_, credential := nativeTestChild(test, fixture)
	if _, err := fixture.store.state.client.NativeConsumerSession.Delete().Exec(test.Context()); err != nil {
		test.Fatal(err)
	}
	if nativeTestCounts(test, fixture.store)[0] != 1 {
		test.Fatal("child deletion unexpectedly deleted its challenge")
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	var enabled atomic.Bool
	enabled.Store(true)
	fixture.store.state.client.NativeConsumerChallenge.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			if enabled.Load() {
				cancel()
			}
			return value, err
		})
	})
	if _, err := fixture.store.PruneNativeConsumers(ctx); !errors.Is(err, context.Canceled) {
		test.Fatalf("prune canceled without error: %v", err)
	}
	if nativeTestCounts(test, fixture.store)[0] != 1 {
		test.Fatal("canceled prune committed deletion")
	}
	enabled.Store(false)
	result, err := fixture.store.PruneNativeConsumers(test.Context())
	if err != nil || result.Rows != 1 {
		test.Fatalf("used challenge did not release quota: %+v %v", result, err)
	}
	_, err = fixture.store.AuthenticateNativeConsumer(test.Context(), credential, "https://cognate.example", nativeTestStart().OperationID)
	nativeAssertUnauthorized(test, err)
}

func TestNativeConsumerExpiredRowsChargedUntilDelete(test *testing.T) {
	fixture := newNativeFixture(test)
	for range 8 {
		if _, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart()); err != nil {
			test.Fatal(err)
		}
	}
	fixture.clock.Add(int64(time.Minute))
	_, err := fixture.store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
		parent, err := nativeParent(test.Context(), tx, fixture.parent, fixture.store.state.now())
		if err != nil {
			return struct{}{}, nil, err
		}
		return struct{}{}, nil, nativeCheckQuota(test.Context(), tx, nativeUsageRow{0, parent.ID, parent.UserID, 1024}, nativeDefaultBudget())
	})
	if !errors.Is(err, ErrNativeQuota) || nativeTestCounts(test, fixture.store)[0] != 8 {
		test.Fatalf("expired rows stopped being charged before physical delete: %v", err)
	}
	if _, err := fixture.store.PruneNativeConsumers(test.Context()); err != nil {
		test.Fatal(err)
	}
	if nativeTestCounts(test, fixture.store)[0] != 0 {
		test.Fatal("prune failed to release expired rows")
	}
}

func TestNativeConsumerFreshChallengeRejectsOtherParentAndOwner(test *testing.T) {
	fixture := newNativeFixture(test)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	for _, owner := range []string{"alice", "bob"} {
		other, _ := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
		_, err := fixture.store.BindNativeConsumer(test.Context(), other, nativeTestBind(challenge, fixture.digest))
		nativeAssertUnauthorized(test, err)
	}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, nativeTestBind(challenge, fixture.digest)); err != nil {
		test.Fatal("other identity attempt damaged the original challenge")
	}
}

func TestNativeConsumerDefaultChildOwnerAndInstanceCaps(test *testing.T) {
	fixture := newNativeFixture(test)
	create := func(owner string, parent OwnerSessionCredential, digest [32]byte) error {
		start := nativeTestStart()
		start.ExpectedOwner = owner
		_, _ = rand.Read(start.OperationID)
		challenge, err := fixture.store.BeginNativeConsumer(test.Context(), parent, start)
		if err != nil {
			return err
		}
		input := nativeTestBind(challenge, digest)
		var tokenHash [32]byte
		_, _ = rand.Read(tokenHash[:])
		input.TokenHash = NativeConsumerCredential{nativeWrap(tokenHash)}
		_, err = fixture.store.BindNativeConsumer(test.Context(), parent, input)
		return err
	}
	for ownerIndex := range 4 {
		owner := fmt.Sprintf("child-owner-%d", ownerIndex)
		for range 2 {
			parent, digest := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
			for range 16 {
				if err := create(owner, parent, digest); err != nil {
					test.Fatal(err)
				}
			}
		}
		parent, digest := nativeTestParent(test, fixture.store, owner, fixture.store.state.now().Add(time.Hour))
		if err := create(owner, parent, digest); !errors.Is(err, ErrNativeQuota) {
			test.Fatalf("default owner child cap bypassed: %v", err)
		}
	}
	if err := create("alice", fixture.parent, fixture.digest); !errors.Is(err, ErrNativeQuota) {
		test.Fatalf("default instance child cap bypassed: %v", err)
	}
	if nativeTestCounts(test, fixture.store)[1] != 128 {
		test.Fatal("wrong instance child retention count")
	}
}

func TestNativeConsumerDamagedRecordFailsClosedWithoutPanic(test *testing.T) {
	fixture := newNativeFixture(test)
	challenge, err := fixture.store.BeginNativeConsumer(test.Context(), fixture.parent, nativeTestStart())
	if err != nil {
		test.Fatal(err)
	}
	input := nativeTestBind(challenge, fixture.digest)
	database, err := sql.Open("sqlite3", fixture.path)
	if err != nil {
		test.Fatal(err)
	}
	defer database.Close()
	if _, err := database.ExecContext(test.Context(), "UPDATE native_consumer_challenges SET public_key=x'01' WHERE public_id=?", challenge.ID()); err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.BindNativeConsumer(test.Context(), fixture.parent, input); !errors.Is(err, ErrNativeUnavailable) {
		test.Fatalf("damaged key accepted: %v", err)
	}
	if nativeTestCounts(test, fixture.store)[1] != 0 {
		test.Fatal("damaged record created authority")
	}
}
