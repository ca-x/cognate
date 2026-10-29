package state

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/configclient"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/nativesessionauth"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/maintenance"
)

func TestManagedNativeCallbacksReceiveLeasedContext(test *testing.T) {
	for _, stage := range []string{"prepare", "authorize", "consume"} {
		test.Run(stage, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			store := flow.fixture.store
			prepareProof := managedPrepareProof(test, flow)
			authorizeRequest, authorizeProof := flow.authorizeEvidence(test)
			if stage == "prepare" {
				if _, err := store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
					test.Fatal(err)
				}
			}
			if stage == "consume" {
				flow.authorize(test)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			var captured context.Context
			nested := func(ctx context.Context) error {
				captured = ctx
				close(entered)
				<-release
				_, err := store.nativeTransaction(ctx, func(tx *ent.Tx) (int, func(time.Time) error, error) {
					count, err := tx.User.Query().Count(ctx)
					return count, nil, err
				})
				return err
			}
			validate := func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload) (NativePreparedData, error) {
				if err := nested(ctx); err != nil {
					return NativePreparedData{}, err
				}
				return flow.validator(ctx, metadata, payload)
			}
			token := mustManagedGrantToken(test)
			finished := make(chan error, 1)
			go func() {
				var err error
				switch stage {
				case "prepare":
					_, err = store.PrepareNativeConfigurationApply(test.Context(), flow.credential, flow.requestOrigin(), flow.request, prepareProof, validate)
				case "authorize":
					_, err = store.AuthorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), authorizeRequest, authorizeProof, token, validate)
				case "consume":
					var value NativeMaterialization
					value, err = store.ConsumeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof, func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload, apply nativesessionauth.ApplyFields, id string, instant int64) (NativeMaterialization, error) {
						if err := nested(ctx); err != nil {
							return NativeMaterialization{}, err
						}
						return nativeStateMaterializer(ctx, metadata, payload, apply, id, instant)
					})
					value.Destroy()
				}
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				test.Fatal("native callback not reached")
			}
			acquired := make(chan *maintenance.Exclusive, 1)
			go func() {
				exclusive, err := instance.Quiesce(test.Context())
				if err == nil {
					acquired <- exclusive
				}
			}()
			awaitManagedAdmissionClosed(test, store)
			close(release)
			err := <-finished
			select {
			case exclusive := <-acquired:
				exclusive.Release()
			case <-time.After(time.Second):
				test.Fatal("native callback did not drain")
			}
			if err != nil {
				test.Fatalf("native callback lost leased context during quiesce: %v", err)
			}
			if _, releaseChild, err := store.enterWriter(captured); !errors.Is(err, maintenance.ErrLease) {
				if releaseChild != nil {
					releaseChild()
				}
				test.Fatal("completed native callback context retained admission")
			}
		})
	}
}

func TestManagedNativeQuotaRetryMustRetainLogicalAdmission(test *testing.T) {
	for _, stage := range []string{"prepare", "authorize"} {
		test.Run(stage, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			store := flow.fixture.store
			request, proof := flow.authorizeEvidence(test)
			prepareProof := managedPrepareProof(test, flow)
			budget := nativeDefaultBudget()
			budget.tables[3][0].rows = 0
			if stage == "prepare" {
				if _, err := store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
					test.Fatal(err)
				}
				seedBudget := nativeDefaultBudget()
				seedBudget.tables[0][0].rows = 16
				for index := range 10 {
					start := nativeTestStart()
					start.OperationID = bytes.Repeat([]byte{byte(80 + index)}, 16)
					if _, err := store.beginNativeConsumer(test.Context(), flow.fixture.parent, start, seedBudget); err != nil {
						test.Fatal("synthetic quota seed failed")
					}
				}
			}
			rolledBack, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			attempts := 0
			store.state.client.NativeConsumerSession.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					attempts++
					if attempts == 1 {
						tx, err := mutation.(*ent.NativeConsumerSessionMutation).Tx()
						if err != nil {
							return nil, err
						}
						tx.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
							return ent.RollbackFunc(func(ctx context.Context, transaction *ent.Tx) error {
								err := next.Rollback(ctx, transaction)
								close(rolledBack)
								<-release
								return err
							})
						})
					}
					return next.Mutate(ctx, mutation)
				})
			})
			token := mustManagedGrantToken(test)
			finished := make(chan error, 1)
			go func() {
				var err error
				if stage == "prepare" {
					_, err = store.PrepareNativeConfigurationApply(test.Context(), flow.credential, flow.requestOrigin(), flow.request, prepareProof, flow.validator)
				} else {
					_, err = store.authorizeNativeConfigurationGrant(test.Context(), flow.credential, flow.requestOrigin(), request, proof, token, flow.validator, budget)
				}
				finished <- err
			}()
			select {
			case <-rolledBack:
			case <-time.After(time.Second):
				test.Fatal("quota rollback not reached")
			}
			acquired := make(chan *maintenance.Exclusive, 1)
			go func() {
				exclusive, err := instance.Quiesce(test.Context())
				if err == nil {
					acquired <- exclusive
				}
			}()
			awaitManagedAdmissionClosed(test, store)
			close(release)
			err := <-finished
			select {
			case exclusive := <-acquired:
				exclusive.Release()
			case <-time.After(time.Second):
				test.Fatal("quota retry did not drain")
			}
			if !errors.Is(err, ErrNativeQuota) || attempts != 2 {
				test.Fatalf("logical quota→prune→run lost admission: %v; attempts=%d", err, attempts)
			}
		})
	}
}

type managedNativePublicationContext struct {
	context.Context
	armed   chan struct{}
	reached chan struct{}
	release chan struct{}
	blocked atomic.Bool
}

func (ctx *managedNativePublicationContext) Err() error {
	select {
	case <-ctx.armed:
		var programCounters [8]uintptr
		frames := runtime.CallersFrames(programCounters[:runtime.Callers(2, programCounters[:])])
		for {
			frame, more := frames.Next()
			if frame.Function == "github.com/ca-x/cognate/internal/server/state.(*Store).ConsumeNativeConfigurationGrant" {
				if ctx.blocked.CompareAndSwap(false, true) {
					close(ctx.reached)
					<-ctx.release
				}
				break
			}
			if !strings.HasPrefix(frame.Function, "context.") || !more {
				break
			}
		}
	default:
	}
	return ctx.Context.Err()
}

func TestManagedNativePublicationBarrierIgnoresUnrelatedErrCalls(test *testing.T) {
	ctx := &managedNativePublicationContext{Context: test.Context(), armed: make(chan struct{}), reached: make(chan struct{}), release: make(chan struct{})}
	close(ctx.armed)
	defer close(ctx.release)
	returned := make(chan error, 1)
	go func() { returned <- ctx.Err() }()
	select {
	case <-ctx.reached:
		test.Fatal("unrelated Err caller seized the Consume publication barrier")
	case err := <-returned:
		if err != nil {
			test.Fatal(err)
		}
	case <-time.After(time.Second):
		test.Fatal("unrelated Err caller did not return")
	}
}

func TestManagedNativeConsumePublicationTailCloseDrain(test *testing.T) {
	for _, canceled := range []bool{false, true} {
		name := "confirmed"
		if canceled {
			name = "cancel-after-commit"
		}
		test.Run(name, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			flow.authorize(test)
			store := flow.fixture.store
			if err := instance.Close(); err != nil {
				test.Fatal(err)
			}
			parent, cancel := context.WithCancel(test.Context())
			defer cancel()
			ctx := &managedNativePublicationContext{Context: parent, armed: make(chan struct{}), reached: make(chan struct{}), release: make(chan struct{})}
			defer func() {
				select {
				case <-ctx.release:
				default:
					close(ctx.release)
				}
			}()
			store.state.client.NativeMaterializationGrant.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(mutationContext context.Context, mutation ent.Mutation) (ent.Value, error) {
					transaction, err := mutation.(*ent.NativeMaterializationGrantMutation).Tx()
					if err != nil {
						return nil, err
					}
					transaction.OnCommit(func(next ent.Committer) ent.Committer {
						return ent.CommitFunc(func(commitContext context.Context, transaction *ent.Tx) error {
							if err := next.Commit(commitContext, transaction); err != nil {
								return err
							}
							close(ctx.armed)
							return nil
						})
					})
					return next.Mutate(mutationContext, mutation)
				})
			})
			var captured context.Context
			finished := make(chan error, 1)
			go func() {
				value, err := store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload, apply nativesessionauth.ApplyFields, id string, instant int64) (NativeMaterialization, error) {
					captured = ctx
					return nativeStateMaterializer(ctx, metadata, payload, apply, id, instant)
				})
				if (err == nil) != value.present() {
					test.Error("native publication tail leaked or lost material")
				}
				value.Destroy()
				finished <- err
			}()
			select {
			case <-ctx.reached:
			case <-time.After(time.Second):
				test.Fatal("post-COMMIT context publication check not reached")
			}
			managed := store.state.maintenance
			managed.mutex.Lock()
			active := managed.active
			changed := managed.changed
			managed.mutex.Unlock()
			closed := make(chan error, 1)
			go func() { closed <- store.Close() }()
			select {
			case <-changed:
			case <-time.After(time.Second):
				test.Fatal("publication-tail Close did not start")
			}
			_, nestedErr := store.nativeTransaction(captured, func(tx *ent.Tx) (int, func(time.Time) error, error) {
				count, err := tx.User.Query().Count(captured)
				return count, nil, err
			})
			select {
			case <-closed:
				test.Fatal("Close escaped while native publication tail was blocked")
			default:
			}
			if canceled {
				cancel()
			}
			close(ctx.release)
			err := <-finished
			if canceled && !errors.Is(err, context.Canceled) || !canceled && err != nil {
				test.Fatal("native publication tail changed cancellation semantics")
			}
			if err := <-closed; err != nil {
				test.Fatal(err)
			}
			if nestedErr != nil {
				test.Fatalf("callback context expired before native publication completed: %v", nestedErr)
			}
			if active != 1 {
				test.Fatal("publication barrier did not isolate the outer lease after helper exit")
			}
		})
	}
}

func managedNativeFlow(test *testing.T) (nativeConfigurationFlow, *maintenance.Instance) {
	test.Helper()
	flow := newNativeConfigurationFlow(test)
	clock := flow.fixture.store.state.now
	if err := flow.fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	store, err := OpenWithOptions(test.Context(), flow.fixture.path, flow.fixture.key, Options{MaintenanceManaged: true})
	if errors.Is(err, maintenance.ErrUnsupported) {
		test.Skip("managed instance unsupported")
	}
	if err != nil {
		test.Fatal(err)
	}
	store.state.now = clock
	flow.fixture.store = store
	test.Cleanup(func() { _ = store.Close() })
	instance, err := maintenance.Join(test.Context(), filepath.Dir(flow.fixture.path))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = instance.Close() })
	return flow, instance
}

func managedPrepareProof(test *testing.T, flow nativeConfigurationFlow) nativesessionauth.VerifiedApplyProof {
	test.Helper()
	consumer := flow.prepared.Binding().Consumer
	keyHash := sha256.Sum256(consumer.PublicKey[:])
	request := flow.request
	transcript, err := configclient.PrepareTranscript(configclient.ConsumerBinding{OwnerID: consumer.OwnerID, ServerOrigin: consumer.ServerOrigin, NativeSessionBinding: consumer.SessionBinding[:], OperationID: consumer.OperationID[:], ConsumerKeySHA256: keyHash[:]}, &nativev1.PrepareNativeApplyRequest{ExpectedOwnerId: request.OwnerID, AssetId: request.AssetID, ExpectedAssetRevision: request.AssetRevision, AgentId: request.AgentID, SchemaVersion: request.SchemaVersion, Adapter: nativeFlowWire(flow.prepared.Binding()).Adapter, Target: 1, Scope: 1, ProfileId: request.ProfileID, SelectionRevision: request.SelectionRevision, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]})
	if err != nil {
		test.Fatal("synthetic prepare transcript failed")
	}
	proof, err := flow.verifier.VerifyPrepare(flow.bearer, nativesessionauth.PrepareFields{Consumer: consumer, Request: request}, ed25519.Sign(flow.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("synthetic prepare proof failed")
	}
	return proof
}

func managedStatusEvidence(test *testing.T, flow nativeConfigurationFlow) (nativesessionauth.StatusRequest, nativesessionauth.VerifiedApplyProof) {
	test.Helper()
	request := nativesessionauth.StatusRequest{PrepareID: flow.prepared.Binding().PrepareID, GrantID: flow.authorization.ID(), OperationID: flow.request.OperationID}
	challenge := flow.prepared.Challenge()
	transcript, err := configclient.StatusTranscript(nativeFlowWire(flow.prepared.Binding()), challenge[:], &nativev1.GetNativeMaterializationStatusRequest{PrepareId: request.PrepareID, GrantId: request.GrantID, OperationId: request.OperationID[:], ClientNonce: request.Nonce[:]})
	if err != nil {
		test.Fatal("synthetic status transcript failed")
	}
	proof, err := flow.verifier.VerifyStatus(flow.bearer, nativesessionauth.StatusFields{Preparation: flow.prepared.Binding(), Challenge: challenge, Request: request}, ed25519.Sign(flow.key, transcript.CopyBytes()))
	if err != nil {
		test.Fatal("synthetic status proof failed")
	}
	return request, proof
}

func managedNativeRows(test *testing.T, store *Store) [32]byte {
	test.Helper()
	challenges, err := store.state.client.NativeConsumerChallenge.Query().Order(ent.Asc("id")).All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	children, err := store.state.client.NativeConsumerSession.Query().Order(ent.Asc("id")).All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	preparations, err := store.state.client.NativeApplyPreparation.Query().Order(ent.Asc("id")).All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	grants, err := store.state.client.NativeMaterializationGrant.Query().Order(ent.Asc("id")).All(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	encoded, err := json.Marshal([]any{challenges, children, preparations, grants})
	if err != nil {
		test.Fatal("native fixture snapshot failed")
	}
	defer clear(encoded)
	return sha256.Sum256(encoded)
}

func TestManagedStoreNativeStagesAndPreflightAdmission(test *testing.T) {
	for _, stage := range []string{"prepare", "authorize", "consume", "status", "snapshot", "preflight-prepare", "preflight-authorize", "preflight-consume", "expired-status", "expired-preflight", "expired-consume", "gc", "logout", "authenticate", "begin", "bind-snapshot", "bind"} {
		test.Run(stage, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			store := flow.fixture.store
			prepareProof := managedPrepareProof(test, flow)
			authorizeRequest, authorizeProof := flow.authorizeEvidence(test)
			if stage == "consume" || stage == "status" || stage == "preflight-consume" || strings.HasPrefix(stage, "expired-") || stage == "gc" || stage == "logout" {
				flow.authorize(test)
			}
			if stage == "prepare" {
				if _, err := store.state.client.NativeApplyPreparation.Delete().Exec(test.Context()); err != nil {
					test.Fatal(err)
				}
			}
			if stage == "gc" {
				flow.fixture.clock.Add(int64(10 * time.Minute))
			}
			if strings.HasPrefix(stage, "expired-") {
				flow.fixture.clock.Store(time.UnixMilli(flow.authorization.ExpiresMS()).UnixNano())
			}
			statusRequest, statusProof := managedStatusEvidence(test, flow)
			var challenge NativeConsumerChallenge
			if stage == "bind" || stage == "bind-snapshot" {
				var err error
				start := nativeTestStart()
				start.OperationID = bytes.Repeat([]byte{12}, 16)
				challenge, err = store.BeginNativeConsumer(test.Context(), flow.fixture.parent, start)
				if err != nil {
					test.Fatal(err)
				}
			}
			invoke := func(ctx context.Context) error {
				switch stage {
				case "prepare":
					value, err := store.PrepareNativeConfigurationApply(ctx, flow.credential, flow.requestOrigin(), flow.request, prepareProof, flow.validator)
					if err != nil && value.present() {
						test.Error("denied prepare published metadata")
					}
					return err
				case "authorize":
					value, err := store.AuthorizeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), authorizeRequest, authorizeProof, mustManagedGrantToken(test), flow.validator)
					if err != nil && value.present() {
						test.Error("denied authorize published grant")
					}
					return err
				case "consume", "expired-consume":
					value, err := store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
					defer value.Destroy()
					if err != nil && value.present() {
						test.Error("denied consume published material")
					}
					return err
				case "status", "expired-status":
					value, err := store.NativeConfigurationGrantStatus(ctx, flow.credential, flow.requestOrigin(), statusRequest, statusProof)
					if err != nil && value.State != 0 {
						test.Error("denied status published state")
					}
					return err
				case "snapshot":
					value, err := store.NativeConfigurationSnapshot(ctx, flow.credential, flow.requestOrigin(), flow.request.OperationID, flow.prepared.Binding().PrepareID)
					if err != nil && value.present() {
						test.Error("denied snapshot published metadata")
					}
					return err
				case "preflight-prepare":
					value, err := store.PreflightNativeConfigurationPrepare(ctx, flow.credential, flow.requestOrigin(), flow.request, prepareProof)
					if err != nil && value.present() {
						test.Error("denied preflight published snapshot")
					}
					return err
				case "preflight-authorize":
					_, err := store.PreflightNativeConfigurationAuthorize(ctx, flow.credential, flow.requestOrigin(), authorizeRequest, authorizeProof)
					return err
				case "preflight-consume", "expired-preflight":
					_, err := store.PreflightNativeConfigurationConsume(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof)
					return err
				case "gc":
					_, err := store.PruneNativeConsumers(ctx)
					return err
				case "logout":
					return store.RevokeSession(ctx, flow.fixture.digest[:])
				case "authenticate":
					_, err := store.AuthenticateNativeConsumer(ctx, flow.credential, flow.requestOrigin(), flow.request.OperationID[:])
					return err
				case "begin":
					_, err := store.BeginNativeConsumer(ctx, flow.fixture.parent, nativeTestStart())
					return err
				case "bind-snapshot":
					_, err := store.NativeBindChallenge(ctx, flow.fixture.parent, challenge.ID())
					return err
				default:
					_, err := store.BindNativeConsumer(ctx, flow.fixture.parent, nativeTestBind(challenge, flow.fixture.digest))
					return err
				}
			}
			exclusive, err := instance.Quiesce(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			defer exclusive.Release()
			before := managedNativeRows(test, store)
			if err := invoke(test.Context()); !errors.Is(err, maintenance.ErrQuiescing) {
				test.Fatalf("native writer bypassed maintenance: %v", err)
			}
			if before != managedNativeRows(test, store) {
				test.Fatal("denied native route mutated four-table state")
			}
			exclusive.Release()
			err = invoke(test.Context())
			if stage == "expired-consume" || stage == "expired-preflight" {
				if !errors.Is(err, ErrNativePrecondition) {
					test.Fatal("expired native grant did not retain its rejection")
				}
			} else if err != nil {
				test.Fatalf("native stage did not recover after release: %v", err)
			}
			if strings.HasPrefix(stage, "expired-") {
				grant, err := store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
				if err != nil || !grant.Expired || grant.ConsumedMs != 0 {
					test.Fatal("expired route did not commit its terminal flag")
				}
			}
			if stage == "gc" || stage == "logout" {
				counts := nativeTestCounts(test, store)
				preparations, prepErr := store.state.client.NativeApplyPreparation.Query().Count(test.Context())
				grants, grantErr := store.state.client.NativeMaterializationGrant.Query().Count(test.Context())
				if counts[0] != 0 || counts[1] != 0 || preparations != 0 || grants != 0 || prepErr != nil || grantErr != nil {
					test.Fatal("native GC/logout left a four-table cascade behind")
				}
			}
		})
	}
}

func mustManagedGrantToken(test *testing.T) nativesessionauth.ParsedGrantToken {
	test.Helper()
	token, err := nativesessionauth.ParseGrantToken(bytes.Repeat([]byte{45}, 32))
	if err != nil {
		test.Fatal("synthetic grant token failed")
	}
	return token
}

func TestManagedStoreNativeLogicalRetryRetainsAdmission(test *testing.T) {
	store, instance, directory := managedStoreFixture(test)
	if err := store.CreateUser(test.Context(), "native-owner"); err != nil {
		test.Fatal(err)
	}
	peer, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	read, proceed, retried, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		for _, signal := range []chan struct{}{proceed, finish} {
			select {
			case <-signal:
			default:
				close(signal)
			}
		}
	}()
	completed := make(chan error, 1)
	attempts := 0
	go func() {
		_, err := store.nativeTransaction(test.Context(), func(tx *ent.Tx) (struct{}, func(time.Time) error, error) {
			if _, err := tx.User.Query().All(test.Context()); err != nil {
				return struct{}{}, nil, err
			}
			attempts++
			if attempts == 1 {
				close(read)
				<-proceed
			} else {
				close(retried)
				<-finish
			}
			return struct{}{}, nil, tx.User.Create().SetUserKey("native-retry").Exec(test.Context())
		})
		completed <- err
	}()
	select {
	case <-read:
	case <-time.After(time.Second):
		test.Fatal("native read not reached")
	}
	if err := peer.CreateUser(test.Context(), "native-competitor"); err != nil {
		test.Fatal(err)
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitManagedAdmissionClosed(test, store)
	close(proceed)
	select {
	case <-retried:
	case err := <-completed:
		test.Fatalf("native retry lost outer admission: %v", err)
	case <-time.After(time.Second):
		test.Fatal("native BUSY snapshot did not retry")
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("exclusive escaped between native attempts")
	default:
	}
	close(finish)
	if err := <-completed; err != nil {
		test.Fatal(err)
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("native retry did not drain")
	}
	if attempts != 2 {
		test.Fatal("native fixture did not exercise exactly one SQLite retry")
	}
}

func TestManagedStoreNativeConsumeCloseDrainAndRollback(test *testing.T) {
	for _, mode := range []string{"commit", "cancel"} {
		test.Run(mode, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			flow.authorize(test)
			store := flow.fixture.store
			if err := instance.Close(); err != nil {
				test.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			finished := make(chan error, 1)
			go func() {
				value, err := store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, func(ctx context.Context, metadata ConfigurationAsset, payload ConfigurationPayload, apply nativesessionauth.ApplyFields, id string, instant int64) (NativeMaterialization, error) {
					close(entered)
					<-release
					return nativeStateMaterializer(ctx, metadata, payload, apply, id, instant)
				})
				if mode == "cancel" && value.present() {
					test.Error("canceled native write published material")
				}
				if mode == "commit" && !value.present() {
					test.Error("completed native write withheld confirmed material")
				}
				value.Destroy()
				finished <- err
			}()
			select {
			case <-entered:
			case <-time.After(time.Second):
				test.Fatal("consume not inside native transaction")
			}
			managed := store.state.maintenance
			managed.mutex.Lock()
			changed := managed.changed
			managed.mutex.Unlock()
			closed := make(chan error, 1)
			go func() { closed <- store.Close() }()
			select {
			case <-changed:
			case <-time.After(time.Second):
				test.Fatal("native Close did not reject new admission")
			}
			if _, err := store.PreflightNativeConfigurationConsume(test.Context(), flow.credential, flow.requestOrigin(), flow.consume, flow.proof); !errors.Is(err, maintenance.ErrClosed) {
				test.Fatal("native preflight entered closing Store")
			}
			select {
			case <-closed:
				test.Fatal("SQL closed while native consume callback was active")
			default:
			}
			managedProcessProbe(test, filepath.Dir(flow.fixture.path), "blocked")
			if mode == "cancel" {
				cancel()
			}
			close(release)
			err := <-finished
			if mode == "cancel" && !errors.Is(err, context.Canceled) || mode == "commit" && err != nil {
				test.Fatal("native Close altered transaction result")
			}
			if err := <-closed; err != nil {
				test.Fatal(err)
			}
			peer, err := OpenWithOptions(test.Context(), flow.fixture.path, flow.fixture.key, Options{MaintenanceManaged: true})
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			grant, err := peer.state.client.NativeMaterializationGrant.Query().Only(test.Context())
			if err != nil || (grant.ConsumedMs != 0) != (mode == "commit") {
				test.Fatal("native commit/rollback was not complete before SQL Close")
			}
		})
	}
}

func TestManagedStoreNativeOnCommitAndContextPublication(test *testing.T) {
	for _, mode := range []string{"commit", "cancel-after", "unknown"} {
		test.Run(mode, func(test *testing.T) {
			flow, instance := managedNativeFlow(test)
			flow.authorize(test)
			store := flow.fixture.store
			ctx, cancel := context.WithCancel(test.Context())
			defer cancel()
			committed, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			store.state.client.NativeMaterializationGrant.Use(func(next ent.Mutator) ent.Mutator {
				return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
					tx, err := mutation.(*ent.NativeMaterializationGrantMutation).Tx()
					if err != nil {
						return nil, err
					}
					tx.OnCommit(func(next ent.Committer) ent.Committer {
						return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
							if err := next.Commit(ctx, transaction); err != nil {
								return err
							}
							close(committed)
							<-release
							_, releaseNested, err := store.enterWriter(ctx)
							if err != nil {
								return err
							}
							releaseNested()
							if mode == "cancel-after" {
								cancel()
							}
							if mode == "unknown" {
								return errors.New("synthetic native commit acknowledgment loss")
							}
							return nil
						})
					})
					return next.Mutate(ctx, mutation)
				})
			})
			finished := make(chan error, 1)
			go func() {
				value, err := store.ConsumeNativeConfigurationGrant(ctx, flow.credential, flow.requestOrigin(), flow.consume, flow.proof, nativeStateMaterializer)
				if (err == nil) != value.present() {
					test.Error("native result publication did not match confirmed outcome")
				}
				value.Destroy()
				finished <- err
			}()
			select {
			case <-committed:
			case <-time.After(time.Second):
				test.Fatal("actual native COMMIT not reached")
			}
			acquired := make(chan *maintenance.Exclusive, 1)
			go func() {
				exclusive, err := instance.Quiesce(test.Context())
				if err == nil {
					acquired <- exclusive
				}
			}()
			awaitManagedAdmissionClosed(test, store)
			select {
			case exclusive := <-acquired:
				exclusive.Release()
				test.Fatal("native lease ended before OnCommit returned")
			default:
			}
			close(release)
			err := <-finished
			if mode == "commit" && err != nil || mode == "cancel-after" && !errors.Is(err, context.Canceled) || mode == "unknown" && !errors.Is(err, ErrNativeOutcomeUnknown) {
				test.Fatal("native outcome/ctx classification changed")
			}
			select {
			case exclusive := <-acquired:
				exclusive.Release()
			case <-time.After(time.Second):
				test.Fatal("native OnCommit did not drain")
			}
			grant, err := store.state.client.NativeMaterializationGrant.Query().Only(test.Context())
			if err != nil || grant.ConsumedMs == 0 {
				test.Fatal("native fixture did not actually commit")
			}
		})
	}
}

func managedStoreFixture(test *testing.T) (*Store, *maintenance.Instance, string) {
	test.Helper()
	if runtime.GOOS == "windows" {
		test.Skip("managed instance requires Unix")
	}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if errors.Is(err, maintenance.ErrUnsupported) {
		test.Skip("managed instance unsupported")
	}
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	instance, err := maintenance.Join(test.Context(), directory)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = instance.Close() })
	return store, instance, directory
}

type managedTokenWaitContext struct {
	context.Context
	waiting chan struct{}
	once    sync.Once
}

func (ctx *managedTokenWaitContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.waiting) })
	return ctx.Context.Done()
}

func TestManagedStoreAdmissionPrecedesTokenGates(test *testing.T) {
	for _, action := range []string{"credential", "sync-commit", "current-list"} {
		test.Run(action, func(test *testing.T) {
			store, instance, _ := managedStoreFixture(test)
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			held, release := make(chan struct{}), make(chan struct{})
			defer func() {
				select {
				case <-release:
				default:
					close(release)
				}
			}()
			var hold func() error
			var write func(context.Context) error
			if action == "credential" {
				token, err := github.NewToken("synthetic-token-order", nil)
				if err != nil {
					test.Fatal("synthetic token failed")
				}
				hold = func() error {
					return token.WithStorageMaterial(test.Context(), func([]byte) error { close(held); <-release; return nil })
				}
				write = func(ctx context.Context) error {
					_, err := store.PutCredential(ctx, "alice", "viewer", "", token)
					return err
				}
			} else {
				result, generation := managedListFixture(test, store)
				if action == "sync-commit" {
					ticket, err := store.BeginListSync(test.Context(), "alice", generation)
					if err != nil {
						test.Fatal(err)
					}
					result, err = store.ReadListSync(test.Context(), ticket, result.client)
					if err != nil {
						test.Fatal(err)
					}
				}
				hold = func() error {
					return result.client.CommitSnapshot(test.Context(), result.snapshot, func(context.Context, github.ListsSnapshot) error { close(held); <-release; return nil })
				}
				write = func(ctx context.Context) error {
					if action == "sync-commit" {
						return store.ApplyListSync(ctx, result)
					}
					_, err := store.ListCollectionBatch(ctx, result, 1)
					return err
				}
			}
			holderDone := make(chan error, 1)
			go func() { holderDone <- hold() }()
			select {
			case <-held:
			case <-time.After(time.Second):
				test.Fatal("token gate not held")
			}
			observed := &managedTokenWaitContext{Context: test.Context(), waiting: make(chan struct{})}
			written := make(chan error, 1)
			go func() { written <- write(observed) }()
			select {
			case <-observed.waiting:
			case <-time.After(time.Second):
				test.Fatal("write did not wait for token gate")
			}
			ctx, cancel := context.WithTimeout(test.Context(), 20*time.Millisecond)
			exclusive, err := instance.Quiesce(ctx)
			cancel()
			if exclusive != nil {
				exclusive.Release()
			}
			if exclusive != nil || !errors.Is(err, context.DeadlineExceeded) {
				test.Fatal("token-waiting writer was not admitted before validity lock")
			}
			if err := store.CreateUser(test.Context(), "after-canceled-drain"); err != nil {
				test.Fatal("canceled quiesce left admission closed")
			}
			close(release)
			if err := <-holderDone; err != nil {
				test.Fatal(err)
			}
			if err := <-written; err != nil {
				test.Fatal(err)
			}
		})
	}
}

type managedListsTransport func(*http.Request) (*http.Response, error)

func (transport managedListsTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func managedListFixture(test *testing.T, store *Store) (ListSyncResult, string) {
	test.Helper()
	token, err := github.NewToken("synthetic-managed-lists", nil)
	if err != nil {
		test.Fatal("synthetic token failed")
	}
	generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
	if err != nil {
		test.Fatal(err)
	}
	client, err := github.NewListsClient(token, &http.Client{Transport: managedListsTransport(func(request *http.Request) (*http.Response, error) {
		var query struct {
			Variables struct {
				ID string `json:"id"`
			} `json:"variables"`
		}
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			return nil, err
		}
		body := `{"data":{"viewer":{"id":"viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"id":"selected","name":"selected","slug":"selected","isPrivate":false,"updatedAt":"2026-01-01T00:00:00Z","lastAddedAt":"2026-01-01T00:00:00Z"}]}}}}`
		if query.Variables.ID != "" {
			body = `{"data":{"node":{"__typename":"UserList","id":"selected","name":"selected","slug":"selected","isPrivate":false,"updatedAt":"2026-01-01T00:00:00Z","lastAddedAt":"2026-01-01T00:00:00Z","user":{"id":"viewer"},"items":{"totalCount":1,"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[{"__typename":"Repository","id":"remote","nameWithOwner":"org/source","isPrivate":false,"databaseId":null}]}}}}`
		}
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}, nil
	})})
	if err != nil {
		test.Fatal(err)
	}
	read := func() ListSyncResult {
		ticket, err := store.BeginListSync(test.Context(), "alice", generation)
		if err != nil {
			test.Fatal(err)
		}
		result, err := store.ReadListSync(test.Context(), ticket, client)
		if err != nil {
			test.Fatal(err)
		}
		if err := store.ApplyListSync(test.Context(), result); err != nil {
			test.Fatal(err)
		}
		return result
	}
	read()
	if err := store.SetListSubscription(test.Context(), "alice", "selected", true); err != nil {
		test.Fatal(err)
	}
	return read(), generation
}

func TestManagedStoreListsCallbacksAndReadClaims(test *testing.T) {
	for _, action := range []string{"read-claim", "sync-commit", "subscription", "collection-batch", "collection-cursor", "star-collect"} {
		test.Run(action, func(test *testing.T) {
			store, instance, _ := managedStoreFixture(test)
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			result, generation := managedListFixture(test, store)
			var ticket ListSyncTicket
			if action == "read-claim" || action == "sync-commit" {
				var err error
				ticket, err = store.BeginListSync(test.Context(), "alice", generation)
				if err != nil {
					test.Fatal(err)
				}
				if action == "sync-commit" {
					result, err = store.ReadListSync(test.Context(), ticket, result.client)
					if err != nil {
						test.Fatal(err)
					}
				}
			}
			invoke := func(ctx context.Context) error {
				switch action {
				case "read-claim":
					_, err := store.ReadListSync(ctx, ticket, result.client)
					return err
				case "sync-commit":
					return store.ApplyListSync(ctx, result)
				case "subscription":
					return store.SetListSubscription(ctx, "alice", "selected", false)
				case "collection-batch":
					_, err := store.ListCollectionBatch(ctx, result, 1)
					return err
				case "collection-cursor":
					return store.AdvanceListCollection(ctx, result, 1)
				default:
					_, err := store.CollectListRepository(ctx, result, "remote", repository.Reference{NameWithOwner: "org/source"}, strings.Repeat("a", 64), []repository.ArchivedSkill{{Path: "skills/demo", Name: "Demo", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}})
					return err
				}
			}
			exclusive, err := instance.Quiesce(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			defer exclusive.Release()
			if err := invoke(test.Context()); !errors.Is(err, maintenance.ErrQuiescing) {
				test.Fatalf("Lists writer bypassed gate: %v", err)
			}
			exclusive.Release()
			if err := invoke(test.Context()); err != nil {
				test.Fatalf("Lists writer failed after release: %v", err)
			}
		})
	}
}

type managedCloseDriver struct {
	dialect.Driver
	entered chan struct{}
	release chan struct{}
}

type managedFailCloseDriver struct {
	dialect.Driver
	entered  chan struct{}
	release  chan struct{}
	attempts atomic.Int64
	failure  error
}

func (driver *managedFailCloseDriver) Close() error {
	if driver.attempts.Add(1) == 1 {
		close(driver.entered)
	}
	<-driver.release
	return driver.failure
}

func TestManagedStoreFailedSQLCloseRetainsRoot(test *testing.T) {
	for _, mode := range []string{"last-reference", "peer-reference"} {
		test.Run(mode, func(test *testing.T) {
			store, instance, directory := managedStoreFixture(test)
			if err := instance.Close(); err != nil {
				test.Fatal(err)
			}
			var peer *Store
			if mode == "peer-reference" {
				var err error
				peer, err = OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
				if err != nil {
					test.Fatal(err)
				}
				test.Cleanup(func() { _ = peer.Close() })
			}
			if err := store.state.client.Close(); err != nil {
				test.Fatal(err)
			}
			database, err := sql.Open("sqlite3", filepath.Join(directory, "server.db"))
			if err != nil {
				test.Fatal(err)
			}
			physical := entsql.OpenDB(dialect.SQLite, database)
			test.Cleanup(func() {
				_ = physical.Close()
				_ = store.state.maintenance.instance.Close()
			})
			failure := errors.New("synthetic physical close failure")
			driver := &managedFailCloseDriver{Driver: physical, entered: make(chan struct{}), release: make(chan struct{}), failure: failure}
			store.state.client = ent.NewClient(ent.Driver(driver))
			defer func() {
				select {
				case <-driver.release:
				default:
					close(driver.release)
				}
			}()
			finished := make(chan error, 2)
			go func() { finished <- store.Close() }()
			select {
			case <-driver.entered:
			case <-time.After(time.Second):
				test.Fatal("physical Close was not attempted")
			}
			if err := store.CreateUser(test.Context(), "denied-during-close"); !errors.Is(err, maintenance.ErrClosed) {
				test.Fatal("Store admitted a writer while SQL Close was pending")
			}
			managedProcessProbe(test, directory, "blocked")
			go func() { finished <- store.Close() }()
			close(driver.release)
			for range 2 {
				if err := <-finished; !errors.Is(err, failure) {
					test.Fatal("Store.Close lost the actual SQL Close failure")
				}
			}
			if err := store.state.client.User.Create().SetUserKey("physical-client-still-writes").Exec(test.Context()); err != nil {
				test.Fatal("failure fixture did not preserve a live SQLite connection")
			}
			if peer != nil {
				if err := peer.CreateUser(test.Context(), "peer-still-writes"); err != nil {
					test.Fatal("failed Store.Close closed another Store")
				}
				if err := peer.Close(); err != nil {
					test.Fatal(err)
				}
			}
			for range 3 {
				if err := store.Close(); !errors.Is(err, failure) {
					test.Fatal("repeated Store.Close did not retain its cached failure")
				}
				if err := store.CreateUser(test.Context(), "denied-after-failed-close"); !errors.Is(err, maintenance.ErrClosed) {
					test.Fatal("failed Store.Close reopened writer admission")
				}
			}
			if driver.attempts.Load() != 1 {
				test.Fatal("Store.Close implicitly retried an unknown physical Close")
			}
			managedProcessProbe(test, directory, "blocked")
			if err := store.state.client.User.Create().SetUserKey("physical-client-still-writes-after-repeated-close").Exec(test.Context()); err != nil {
				test.Fatal("repeated Close silently closed the real connection")
			}
			if err := physical.Close(); err != nil {
				test.Fatal(err)
			}
			if err := store.Close(); !errors.Is(err, failure) {
				test.Fatal("external physical Close cleared the cached Store failure")
			}
			managedProcessProbe(test, directory, "blocked")
		})
	}
}

func (driver *managedCloseDriver) Close() error {
	close(driver.entered)
	<-driver.release
	return driver.Driver.Close()
}

func TestManagedStoreKeepsInstanceUntilClientCloseReturns(test *testing.T) {
	store, instance, directory := managedStoreFixture(test)
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	if err := store.state.client.Close(); err != nil {
		test.Fatal(err)
	}
	database, err := sql.Open("sqlite3", filepath.Join(directory, "server.db"))
	if err != nil {
		test.Fatal(err)
	}
	driver := &managedCloseDriver{Driver: entsql.OpenDB(dialect.SQLite, database), entered: make(chan struct{}), release: make(chan struct{})}
	store.state.client = ent.NewClient(ent.Driver(driver))
	defer func() {
		select {
		case <-driver.release:
		default:
			close(driver.release)
		}
	}()
	finished := make(chan error, 1)
	go func() { finished <- store.Close() }()
	select {
	case <-driver.entered:
	case <-time.After(time.Second):
		test.Fatal("client Close not reached")
	}
	managedProcessProbe(test, directory, "blocked")
	close(driver.release)
	if err := <-finished; err != nil {
		test.Fatal(err)
	}
	managedProcessProbe(test, directory, "acquired")
}

func TestManagedStoreLogicalRetryRetainsAdmission(test *testing.T) {
	store, instance, directory := managedStoreFixture(test)
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	peer, err := OpenWithOptions(test.Context(), filepath.Join(directory, "server.db"), make([]byte, 32), Options{MaintenanceManaged: true})
	if err != nil {
		test.Fatal(err)
	}
	defer peer.Close()
	read, proceed, retried, finish := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	defer func() {
		for _, signal := range []chan struct{}{proceed, finish} {
			select {
			case <-signal:
			default:
				close(signal)
			}
		}
	}()
	completed := make(chan error, 1)
	attempts := 0
	go func() {
		completed <- store.catalogTransaction(test.Context(), func(tx *ent.Tx) error {
			if _, err := tx.User.Query().All(test.Context()); err != nil {
				return err
			}
			attempts++
			if attempts == 1 {
				close(read)
				<-proceed
			} else {
				close(retried)
				<-finish
			}
			return tx.User.Create().SetUserKey("retried-writer").Exec(test.Context())
		})
	}()
	select {
	case <-read:
	case <-time.After(time.Second):
		test.Fatal("first read not reached")
	}
	if err := peer.CreateUser(test.Context(), "competing-writer"); err != nil {
		test.Fatal(err)
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitManagedAdmissionClosed(test, store)
	close(proceed)
	select {
	case <-retried:
	case err := <-completed:
		test.Fatalf("logical retry lost its admission: %v", err)
	case <-time.After(time.Second):
		test.Fatal("real SQLite BUSY did not retry")
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("exclusive escaped during logical retry")
	default:
	}
	close(finish)
	if err := <-completed; err != nil {
		test.Fatal(err)
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("successful retry did not drain")
	}
	if attempts != 2 {
		test.Fatalf("unexpected SQLite attempts: %d", attempts)
	}
}

func TestManagedStoreLeaseIncludesOnCommitCompletion(test *testing.T) {
	store, instance, _ := managedStoreFixture(test)
	committed, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	completed := make(chan error, 1)
	go func() {
		completed <- store.catalogTransaction(test.Context(), func(tx *ent.Tx) error {
			tx.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
					err := next.Commit(ctx, transaction)
					close(committed)
					<-release
					_, releaseNested, nestedError := store.enterWriter(ctx)
					if nestedError != nil {
						return nestedError
					}
					releaseNested()
					return err
				})
			})
			return tx.User.Create().SetUserKey("committed-writer").Exec(test.Context())
		})
	}()
	select {
	case <-committed:
	case <-time.After(time.Second):
		test.Fatal("commit not reached")
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitManagedAdmissionClosed(test, store)
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("lease ended before OnCommit callback returned")
	default:
	}
	close(release)
	if err := <-completed; err != nil {
		test.Fatal(err)
	}
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("OnCommit completion did not drain")
	}
}

func TestManagedStoreNonNativeWritersRejectBeforeSQLite(test *testing.T) {
	for _, action := range []string{"account", "session", "logout", "oidc", "credential", "sync-begin", "catalog-read", "collect", "pack-create", "pack-update", "pack-delete", "share-create", "share-revoke", "category-create", "category-update", "category-delete", "category-assign", "publish", "refresh", "configuration", "mcp-create", "mcp-update", "mcp-rotate", "mcp-revoke", "audit"} {
		test.Run(action, func(test *testing.T) {
			store, instance, _ := managedStoreFixture(test)
			if err := store.CreateUser(test.Context(), "alice"); err != nil {
				test.Fatal(err)
			}
			passwordHash, sessionHash := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
			if _, err := store.CreatePasswordAccount(test.Context(), "local", passwordHash); err != nil {
				test.Fatal(err)
			}
			now := time.Now().UTC()
			if err := store.CreatePasswordSession(test.Context(), "local", passwordHash, sessionHash, now, now.Add(time.Hour)); err != nil {
				test.Fatal(err)
			}
			token, err := github.NewToken("synthetic-managed-token", nil)
			if err != nil {
				test.Fatal("synthetic token failed")
			}
			generation, err := store.PutCredential(test.Context(), "alice", "viewer", "", token)
			if err != nil {
				test.Fatal(err)
			}
			skills := []repository.ArchivedSkill{{Path: "skills/demo", Name: "Demo", Description: "Synthetic", ContentHash: strings.Repeat("b", 64)}}
			reference := repository.Reference{NameWithOwner: "org/source"}
			collected, err := store.CollectRepository(test.Context(), "alice", reference, strings.Repeat("a", 64), skills)
			if err != nil {
				test.Fatal(err)
			}
			collected, err = store.CollectedRepository(test.Context(), "alice", collected.ID)
			if err != nil {
				test.Fatal(err)
			}
			draft := PackDraft{Name: "Synthetic", Items: []PackSelection{{SkillID: collected.Skills[0].ID, Slug: "demo"}}}
			pack, err := store.CreatePack(test.Context(), "alice", draft)
			if err != nil {
				test.Fatal(err)
			}
			share, err := store.CreatePackShare(test.Context(), "alice", pack.ID, strings.Repeat("S", 26), bytes.Repeat([]byte{3}, 32), nil)
			if err != nil {
				test.Fatal(err)
			}
			category, err := store.CreateCategory(test.Context(), "alice", "before")
			if err != nil {
				test.Fatal(err)
			}
			metadata := MCPMetadata{Name: "Synthetic", Scopes: []string{MCPCatalogRead}, ExpiresAt: now.Add(time.Hour)}
			credential, err := store.CreateMCPCredential(test.Context(), "alice", bytes.Repeat([]byte{4}, 32), metadata)
			if err != nil {
				test.Fatal(err)
			}
			invoke := func(ctx context.Context) error {
				switch action {
				case "account":
					_, err := store.CreatePasswordAccount(ctx, "new-account", passwordHash)
					return err
				case "session":
					return store.CreatePasswordSession(ctx, "local", passwordHash, bytes.Repeat([]byte{5}, 32), now, now.Add(time.Hour))
				case "logout":
					return store.RevokeSession(ctx, sessionHash)
				case "oidc":
					_, err := store.CreateOIDCSession(ctx, OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "synthetic", TokenHash: bytes.Repeat([]byte{6}, 32), IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
					return err
				case "credential":
					_, err := store.PutCredential(ctx, "alice", "viewer", generation, token)
					return err
				case "sync-begin":
					_, err := store.BeginListSync(ctx, "alice", generation)
					return err
				case "catalog-read":
					_, err := store.GitHubCatalog(ctx, "alice")
					return err
				case "collect":
					_, err := store.CollectRepository(ctx, "alice", repository.Reference{NameWithOwner: "org/second"}, strings.Repeat("c", 64), skills)
					return err
				case "pack-create":
					_, err := store.CreatePack(ctx, "alice", draft)
					return err
				case "pack-update":
					_, err := store.UpdatePack(ctx, "alice", pack.ID, pack.Revision, draft)
					return err
				case "pack-delete":
					return store.DeletePack(ctx, "alice", pack.ID, pack.Revision)
				case "share-create":
					_, err := store.CreatePackShare(ctx, "alice", pack.ID, strings.Repeat("T", 26), bytes.Repeat([]byte{7}, 32), nil)
					return err
				case "share-revoke":
					return store.RevokePackShare(ctx, "alice", pack.ID, share.ID)
				case "category-create":
					_, err := store.CreateCategory(ctx, "alice", "new")
					return err
				case "category-update":
					_, err := store.UpdateCategory(ctx, "alice", category.ID, category.Revision, "after")
					return err
				case "category-delete":
					return store.DeleteCategory(ctx, "alice", category.ID, category.Revision)
				case "category-assign":
					_, err := store.SetSkillCategories(ctx, "alice", collected.Skills[0].ID, []string{category.ID})
					return err
				case "publish":
					_, err := store.SetPublication(ctx, "alice", collected.ID, collected.PublicationRevision, true, true)
					return err
				case "refresh":
					_, err := store.RefreshCollectedRepository(ctx, "alice", collected.ID, RepositoryVersion{ArchiveHash: collected.ArchiveHash, PublicationRevision: collected.PublicationRevision}, strings.Repeat("c", 64), skills)
					return err
				case "configuration":
					_, err := store.CreateConfigurationAsset(ctx, "alice", ConfigurationAssetDraft{AgentID: "claude-code", Kind: ConfigurationKindProvider, Name: "Synthetic", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-configuration"))})
					return err
				case "mcp-create":
					_, err := store.CreateMCPCredential(ctx, "alice", bytes.Repeat([]byte{8}, 32), metadata)
					return err
				case "mcp-update":
					_, err := store.UpdateMCPCredential(ctx, "alice", credential.ID, credential.Revision, metadata)
					return err
				case "mcp-rotate":
					_, err := store.RotateMCPCredential(ctx, "alice", credential.ID, credential.Revision, bytes.Repeat([]byte{9}, 32))
					return err
				case "mcp-revoke":
					_, err := store.RevokeMCPCredential(ctx, "alice", credential.ID, credential.Revision)
					return err
				default:
					return store.RecordMCPAudit(ctx, credential.ID, "catalog_list", "", "success")
				}
			}
			exclusive, err := instance.Quiesce(test.Context())
			if err != nil {
				test.Fatal(err)
			}
			defer exclusive.Release()
			expectedError := maintenance.ErrQuiescing
			if action == "configuration" {
				expectedError = ErrConfigurationUnavailable
			}
			if err := invoke(test.Context()); !errors.Is(err, expectedError) {
				test.Fatalf("writer bypassed maintenance: %v", err)
			}
			if _, _, err := store.SessionUser(test.Context(), sessionHash, now); err != nil {
				test.Fatal("maintenance blocked a pure read or changed session")
			}
			exclusive.Release()
			if err := invoke(test.Context()); err != nil {
				test.Fatalf("writer did not recover after maintenance: %v", err)
			}
			if action == "logout" {
				if _, _, err := store.SessionUser(test.Context(), sessionHash, now); !errors.Is(err, ErrSessionInvalid) {
					test.Fatal("logout did not actually delete session")
				}
			}
			if action == "audit" {
				entries, err := store.ListMCPAudit(test.Context(), "alice", credential.ID)
				if err != nil || len(entries) != 1 {
					test.Fatal("audit did not actually insert after maintenance")
				}
			}
		})
	}
}

func TestManagedStoreRejectsRealWriteDuringMaintenance(test *testing.T) {
	store, instance, _ := managedStoreFixture(test)
	exclusive, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer exclusive.Release()
	if err := store.CreateUser(test.Context(), "blocked"); !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatalf("managed write admitted: %v", err)
	}
	count, err := store.state.client.User.Query().Count(context.Background())
	if err != nil || count != 0 {
		test.Fatal("rejected write persisted")
	}
	exclusive.Release()
	if err := store.CreateUser(test.Context(), "allowed"); err != nil {
		test.Fatal(err)
	}
}

func awaitManagedAdmissionClosed(test *testing.T, store *Store) {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), time.Second)
	defer cancel()
	for {
		_, release, err := store.enterWriter(ctx)
		if errors.Is(err, maintenance.ErrQuiescing) {
			return
		}
		if err != nil {
			test.Fatalf("admission did not quiesce: %v", err)
		}
		release()
		runtime.Gosched()
	}
}

func TestManagedStoreNestedLeaseAndRollbackDrain(test *testing.T) {
	store, instance, _ := managedStoreFixture(test)
	parent, releaseParent, err := store.enterWriter(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer releaseParent()
	child, releaseChild, err := store.enterWriter(parent)
	if err != nil {
		test.Fatal(err)
	}
	defer releaseChild()
	releaseParent()
	if err := store.CreateUser(parent, "stale"); !errors.Is(err, maintenance.ErrLease) {
		test.Fatal("expired parent lent a lease")
	}
	acquired := make(chan *maintenance.Exclusive, 1)
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitManagedAdmissionClosed(test, store)
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("released parent erased active child")
	default:
	}
	if err := store.CreateUser(child, "nested"); err != nil {
		test.Fatalf("active child could not finish during quiesce: %v", err)
	}
	releaseChild()
	select {
	case exclusive := <-acquired:
		exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("child release did not drain")
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	completed := make(chan error, 1)
	rollbackError := errors.New("synthetic callback rollback")
	go func() {
		completed <- store.catalogTransaction(test.Context(), func(tx *ent.Tx) error {
			if err := tx.User.Create().SetUserKey("rolled-back").Exec(test.Context()); err != nil {
				return err
			}
			close(entered)
			<-release
			return rollbackError
		})
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		test.Fatal("transaction did not write")
	}
	go func() {
		exclusive, err := instance.Quiesce(test.Context())
		if err == nil {
			acquired <- exclusive
		}
	}()
	awaitManagedAdmissionClosed(test, store)
	select {
	case exclusive := <-acquired:
		exclusive.Release()
		test.Fatal("maintenance escaped live callback/transaction")
	default:
	}
	close(release)
	if err := <-completed; !errors.Is(err, rollbackError) {
		test.Fatal("callback rollback result lost")
	}
	select {
	case exclusive := <-acquired:
		defer exclusive.Release()
	case <-time.After(time.Second):
		test.Fatal("rollback did not drain")
	}
	count, err := store.state.client.User.Query().Count(test.Context())
	if err != nil || count != 1 {
		test.Fatal("callback transaction did not roll back before exclusive admission")
	}
}

func TestManagedStoreCloseDrainsSQLBeforeLastInstanceRelease(test *testing.T) {
	store, instance, directory := managedStoreFixture(test)
	if err := store.CreateUser(test.Context(), "alice"); err != nil {
		test.Fatal(err)
	}
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	store.state.client.Category.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			value, err := next.Mutate(ctx, mutation)
			if err == nil {
				close(entered)
				<-release
			}
			return value, err
		})
	})
	written := make(chan error, 1)
	go func() { _, err := store.CreateCategory(test.Context(), "alice", "held"); written <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		test.Fatal("writer did not enter SQLite")
	}
	managed := store.state.maintenance
	managed.mutex.Lock()
	changed := managed.changed
	managed.mutex.Unlock()
	closed := make(chan error, 1)
	go func() { closed <- store.Close() }()
	select {
	case <-changed:
	case <-time.After(time.Second):
		test.Fatal("Close did not stop admission")
	}
	if err := store.CreateUser(test.Context(), "late"); !errors.Is(err, maintenance.ErrClosed) {
		test.Fatal("Close accepted new writer")
	}
	select {
	case <-closed:
		test.Fatal("Close returned while SQL callback was live")
	default:
	}
	managedProcessProbe(test, directory, "blocked")
	if _, err := store.state.client.User.Query().Count(test.Context()); err != nil {
		test.Fatal("SQL closed before active writer drained")
	}
	close(release)
	if err := <-written; err != nil {
		test.Fatal(err)
	}
	if err := <-closed; err != nil {
		test.Fatal(err)
	}
	if _, err := store.state.client.User.Query().Count(test.Context()); err == nil {
		test.Fatal("Close left SQL client open")
	}
	managedProcessProbe(test, directory, "acquired")
}
