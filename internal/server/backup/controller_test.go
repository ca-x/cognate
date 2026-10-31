package backup

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"uuid"

	"github.com/ca-x/cognate/internal/server/library"
	"github.com/spf13/afero"
)

func TestControllerReconcilePreservesCancellation(test *testing.T) {
	for _, mode := range []string{"during-reconcile", "already-canceled", "legacy-cancel-flag", "attempted-complete"} {
		test.Run(mode, func(test *testing.T) {
			controller, store, fixture := controllerFixture(test)
			var behavior atomic.Int32
			behavior.Store(1)
			entered, release := make(chan struct{}), make(chan struct{})
			var enterOnce, releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(release) })
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				if behavior.Load() == 1 && (mode != "attempted-complete" && request.Method == http.MethodPut && strings.Contains(key, "/data/") || mode == "attempted-complete" && request.Method == http.MethodHead && strings.Contains(key, "/complete/")) {
					fixtureError(writer, 503, "InternalError")
					return true
				}
				if behavior.Load() == 2 && request.Method == http.MethodHead {
					enterOnce.Do(func() { close(entered) })
					select {
					case <-release:
					case <-request.Context().Done():
					}
					return true
				}
				return false
			}
			principal := uuid.New().String()
			job, err := controller.Start(test.Context(), principal, uuid.New().String())
			if err != nil {
				test.Fatal(err)
			}
			initialPhase, canceledPhase, canceledCode := "interrupted", "canceled", "canceled"
			if mode == "attempted-complete" {
				initialPhase, canceledPhase, canceledCode = "commit_unknown", "commit_unknown", "verify_unknown"
			}
			if finished := waitControlJob(test, controller, job.ID); finished.Phase != initialPhase {
				test.Fatal("unknown data PUT setup failed", finished)
			}
			before := len(fixture.snapshot())
			var reconciled Job
			if mode == "already-canceled" || mode == "legacy-cancel-flag" {
				if mode == "legacy-cancel-flag" {
					physical := store.get()
					candidate := physical.clone()
					candidate.Jobs[0].Job.CancelRequested = true
					candidate.Jobs[0].Job.Code = "verify_unknown"
					if err := physical.persist(candidate); err != nil {
						test.Fatal("legacy interrupted cancellation fixture refused", err)
					}
				}
				if canceled, err := controller.Cancel(test.Context(), principal, job.ID); err != nil || canceled.Phase != "canceled" {
					test.Fatal("idle cancel did not establish terminal state", canceled, err)
				}
				behavior.Store(0)
				reconciled, err = controller.Reconcile(test.Context(), principal, job.ID)
			} else {
				behavior.Store(2)
				done := make(chan struct{})
				go func() {
					defer close(done)
					reconciled, err = controller.Reconcile(test.Context(), principal, job.ID)
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					test.Fatal("reconcile did not enter exact HEAD")
				}
				if canceled, err := controller.Cancel(test.Context(), principal, job.ID); err != nil || !canceled.CancelRequested {
					test.Fatal("cancel was not durable", canceled, err)
				}
				releaseOnce.Do(func() { close(release) })
				select {
				case <-done:
				case <-time.After(5 * time.Second):
					test.Fatal("canceled reconcile did not drain")
				}
			}
			if err != nil || reconciled.Phase != canceledPhase || reconciled.Code != canceledCode || !reconciled.CancelRequested {
				test.Fatal("reconcile revived cancellation", reconciled, err)
			}
			for range 2 {
				if repeated, err := controller.Cancel(test.Context(), principal, job.ID); err != nil || repeated.Phase != canceledPhase || repeated.Code != canceledCode {
					test.Fatal("repeated cancel did not converge", repeated, err)
				}
			}
			if store.get().data.Jobs[0].CompleteAttempted != (mode == "attempted-complete") {
				test.Fatal("cancellation changed complete-attempt intent")
			}
			if mode == "attempted-complete" {
				behavior.Store(0)
				verified, err := controller.Reconcile(test.Context(), principal, job.ID)
				if err != nil || verified.Phase != "remote_committed" || !verified.CancelRequested {
					test.Fatal("exact verified commit exception was lost", verified, err)
				}
			}
			for _, call := range fixture.snapshot()[before:] {
				if call.method == http.MethodPut {
					test.Fatal("cancellation reconciliation replayed PUT")
				}
			}
		})
	}
}

func controlCapacityCatalog(dataObjects uint32) controlJob {
	record := controlTestRecord()
	record.Job.Phase, record.CompleteAttempted = "remote_committed", true
	object := controlObject{Kind: Bootstrap, Size: 100, SHA256: strings.Repeat("03", 32), Attempt: strings.Repeat("04", 16)}
	record.Catalog = append(record.Catalog, object)
	object.Kind = DataExtent
	for ordinal := range dataObjects {
		object.Ordinal = ordinal
		record.Catalog = append(record.Catalog, object)
	}
	object.Kind, object.Ordinal = ManifestPage, 1
	record.Catalog = append(record.Catalog, object)
	object.Ordinal = 0
	record.Catalog = append(record.Catalog, object)
	object.Kind = Complete
	record.Catalog = append(record.Catalog, object)
	record.Job.Objects = uint32(len(record.Catalog))
	record.Job.CiphertextBytes = int64(len(record.Catalog)) * 100
	return record
}

func controlCapacityHistory(test *testing.T, journal controlJournal, pending controlJob, spare int) controlJournal {
	test.Helper()
	fixed := controlCapacityCatalog(90000)
	length := func(candidate controlJournal) int {
		encoded, err := json.Marshal(candidate)
		if err != nil {
			test.Fatal(err)
		}
		defer clear(encoded)
		return len(encoded) + 52
	}
	target := controlLimit - spare
	lower, upper := uint32(1), uint32(20000)
	for lower < upper {
		middle := (lower + upper + 1) / 2
		journal.Jobs = []controlJob{fixed, controlCapacityCatalog(middle), pending}
		if length(journal) <= target {
			lower = middle
		} else {
			upper = middle - 1
		}
	}
	journal.Jobs = []controlJob{fixed, controlCapacityCatalog(lower), pending}
	remaining := target - length(journal)
	if remaining < 0 || remaining > 500 {
		test.Fatal("exact-capacity fixture padding invalid", remaining)
	}
	for index := range remaining {
		journal.Jobs[1].Catalog[index].Size = 1000
		journal.Jobs[1].Job.CiphertextBytes += 900
	}
	if length(journal) != target || journal.valid() != nil {
		test.Fatal("capacity fixture is not a valid exact-size journal", length(journal), target)
	}
	journal.Jobs = journal.Jobs[:2]
	return journal
}

func TestControllerJournalBudgetReservesSuccessors(test *testing.T) {
	for _, spare := range []int{0, 512} {
		test.Run(fmt.Sprintf("spare-%d", spare), func(test *testing.T) {
			controller, store, fixture := controllerFixture(test)
			state, physical := controller.get(), store.get()
			pending := controlTestRecord()
			pending.Generation, pending.Options = strings.Repeat("01", 32), state.fingerprint
			pending.Job.CreatedAt = time.Date(2026, 10, 31, 12, 0, 0, 123456789, time.UTC)
			pending.Job.UpdatedAt = pending.Job.CreatedAt
			history := controlCapacityHistory(test, physical.clone(), pending, spare)
			if err := physical.persist(history); err != nil {
				test.Fatal("valid synthetic history refused", err)
			}
			job, err := controller.Start(test.Context(), pending.Principal, pending.Request)
			if spare == 0 {
				if !errors.Is(err, ErrControlBudget) || job != (Job{}) {
					test.Fatal("registration spent successor bookkeeping reserve", job, err)
				}
			} else {
				if err != nil {
					test.Fatal("bounded bookkeeping reserve refused", err)
				}
				if finished := waitControlJob(test, controller, job.ID); finished.Phase != "failed" || finished.Code != "budget" || finished.Objects != 0 {
					test.Fatal("catalog budget did not produce a durable bounded failure", finished)
				}
			}
			if physical.uncertain || len(fixture.snapshot()) != 0 {
				test.Fatal("clean budget rejection poisoned ownership or uploaded", physical.uncertain)
			}
			if spare != 0 {
				candidate := physical.clone()
				candidate.Jobs[len(candidate.Jobs)-1].Job.Phase = "capturing"
				candidate.Jobs[len(candidate.Jobs)-1].Job.Code = ""
				if err := physical.persist(candidate); err != nil {
					test.Fatal("accepted bookkeeping could not persist a crash boundary", err)
				}
			}
			page, err := controller.List(test.Context(), 1000, "")
			if err != nil || len(page.Jobs) != len(physical.data.Jobs) || len(page.Jobs) < 2 {
				test.Fatal("budget rejection hid or evicted history", err)
			}
			root, err := physical.root.OpenRoot(".")
			if err != nil {
				test.Fatal(err)
			}
			defer root.Close()
			if err := controller.Close(test.Context()); err != nil {
				test.Fatal("ordinary capacity refusal prevented controller close", err)
			}
			if err := store.Close(); err != nil {
				test.Fatal("ordinary capacity refusal poisoned the store", err)
			}
			reopened, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{73}, 32))
			if err != nil {
				test.Fatal("budget history could not reopen", err)
			}
			defer reopened.Close()
			restarted, err := NewController(test.Context(), reopened, state.service, state.target, state.engine, state.options)
			if err != nil {
				test.Fatal("budget history prevented restart normalization", err)
			}
			defer restarted.Close(test.Context())
			if page, err := restarted.List(test.Context(), 1000, ""); err != nil || len(page.Jobs) != len(physical.data.Jobs) {
				test.Fatal("restart changed retained history", err)
			}
			if spare != 0 {
				if normalized, err := restarted.Get(test.Context(), job.ID); err != nil || normalized.Phase != "interrupted" || normalized.Code != "interrupted" {
					test.Fatal("reserved capacity did not cover restart bookkeeping", normalized, err)
				}
				if canceled, err := restarted.Cancel(test.Context(), pending.Principal, job.ID); err != nil || canceled.Phase != "canceled" {
					test.Fatal("reserved capacity did not cover cancel bookkeeping", canceled, err)
				}
				if repeated, err := restarted.Start(test.Context(), pending.Principal, pending.Request); err != nil || repeated.ID != job.ID || repeated.Phase != "canceled" {
					test.Fatal("budget/restart/cancel lost persistent idempotency", repeated, err)
				}
			}
		})
	}
}

func controllerFixture(test *testing.T) (*Controller, *ControlStore, *s3Fixture) {
	test.Helper()
	if runtime.GOOS != "linux" {
		test.Skip("real B2 snapshot worker is Linux-only")
	}
	root, _ := controlRoot(test)
	store, err := OpenControlStore(test.Context(), root, bytes.Repeat([]byte{73}, 32))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	service, _, _, _, _ := captureFixture(test)
	engine, _, _ := sealFixture(test)
	fixture := newFixture(test)
	target := fixture.target(test, false)
	data := target.options.read()
	data.instance = store.InstanceID()
	target.options = S3Options{read: func() optionData { return data }}
	controller, err := NewController(test.Context(), store, service, target, engine, ControllerOptions{Generation: [32]byte{1}, AppVersion: "control-test", ContentLimits: captureOptions().Limits, MaxJobs: 1000, JobTimeout: 10 * time.Second})
	if err != nil {
		test.Fatal("controller refused real managed source and TLS target", err)
	}
	test.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = controller.Close(ctx)
	})
	return controller, store, fixture
}

func TestControllerCipherCleanupUncertaintyWinsRemoteSuccess(test *testing.T) {
	controller, store, fixture := controllerFixture(test)
	engine := controller.get().engine.get()
	original := engine.ops.sync
	var failCleanup atomic.Bool
	engine.ops.sync = func(file *os.File) error {
		if failCleanup.Load() && file == engine.directory.root {
			return errors.New("private-cipher-cleanup-secret")
		}
		return original(file)
	}
	fixture.hook = func(_ http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == http.MethodHead && strings.Contains(key, "/complete/") {
			failCleanup.Store(true)
		}
		return false
	}
	job, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	store.get().mutex.Lock()
	active := controller.get().active
	store.get().mutex.Unlock()
	if active != nil {
		select {
		case <-active.done:
		case <-time.After(5 * time.Second):
			test.Fatal("cipher cleanup failure did not drain")
		}
	}
	engine.ops.sync = original
	store.get().mutex.Lock()
	record := store.get().data.Jobs[0]
	uncertain := store.get().uncertain
	store.get().mutex.Unlock()
	if record.Job.ID != job.ID || record.Job.Phase != "commit_unknown" || record.Job.Code != "uncertain" || !uncertain {
		test.Fatal("remote success downgraded local cleanup uncertainty", record.Job)
	}
	if err := controller.Close(test.Context()); !errors.Is(err, ErrControlUncertain) {
		test.Fatal("unsafe service teardown after local cleanup failure", err)
	}
	if err := store.Close(); !errors.Is(err, ErrControlUncertain) {
		test.Fatal("uncertain cleanup released control ownership", err)
	}
}

func TestControllerUnknownRestartReadOnlyReconcile(test *testing.T) {
	for _, mode := range []string{"complete-response", "data-put"} {
		test.Run(mode, func(test *testing.T) {
			controller, store, fixture := controllerFixture(test)
			var injecting atomic.Bool
			injecting.Store(true)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				if injecting.Load() && (mode == "complete-response" && request.Method == http.MethodHead && strings.Contains(key, "/complete/") || mode == "data-put" && request.Method == http.MethodPut && strings.Contains(key, "/data/")) {
					fixtureError(writer, 503, "InternalError")
					return true
				}
				return false
			}
			principal, requestID := uuid.New().String(), uuid.New().String()
			job, err := controller.Start(test.Context(), principal, requestID)
			if err != nil {
				test.Fatal(err)
			}
			finished := waitControlJob(test, controller, job.ID)
			expected := "interrupted"
			if mode == "complete-response" {
				expected = "commit_unknown"
			}
			if finished.Phase != expected || finished.Code != "put_unknown" {
				test.Fatal("unknown PUT pretended success/failure", finished)
			}
			before := fixture.snapshot()
			if err := controller.Close(test.Context()); err != nil {
				test.Fatal(err)
			}
			physical := store.get()
			ownedRoot, err := physical.root.OpenRoot(".")
			if err != nil {
				test.Fatal(err)
			}
			defer ownedRoot.Close()
			if err := store.Close(); err != nil {
				test.Fatal(err)
			}
			reopened, err := OpenControlStore(test.Context(), ownedRoot, bytes.Repeat([]byte{73}, 32))
			if err != nil {
				test.Fatal(err)
			}
			defer reopened.Close()
			previous := controller.get()
			restarted, err := NewController(test.Context(), reopened, previous.service, previous.target, previous.engine, previous.options)
			if err != nil {
				test.Fatal(err)
			}
			defer restarted.Close(test.Context())
			again, err := restarted.Start(test.Context(), principal, requestID)
			if err != nil || again.ID != job.ID || again.Phase != expected {
				test.Fatal("restart lost persistent idempotency", err, again)
			}
			if len(fixture.snapshot()) != len(before) {
				test.Fatal("restart automatically replayed unknown PUT")
			}
			injecting.Store(false)
			reconciled, err := restarted.Reconcile(test.Context(), principal, job.ID)
			if err != nil {
				test.Fatal(err)
			}
			if mode == "complete-response" && reconciled.Phase != "remote_committed" || mode == "data-put" && (reconciled.Phase != "interrupted" || reconciled.Code != "missing") {
				test.Fatal("read-only reconciliation invented completion", reconciled)
			}
			for _, call := range fixture.snapshot()[len(before):] {
				if call.method != http.MethodGet && call.method != http.MethodHead {
					test.Fatal("reconcile mutated remote objects", call.method)
				}
			}
		})
	}
}

func TestControllerCancelBusySourceBarrierAndPrincipalIsolation(test *testing.T) {
	controller, store, fixture := controllerFixture(test)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	fixture.hook = func(_ http.ResponseWriter, request *http.Request, _ string) bool {
		if request.Method == http.MethodPut {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-request.Context().Done():
			}
			return true
		}
		return false
	}
	principal, requestID := uuid.New().String(), uuid.New().String()
	job, err := controller.Start(test.Context(), principal, requestID)
	if err != nil {
		test.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("worker did not upload")
	}
	again, err := controller.Start(test.Context(), principal, requestID)
	if err != nil || again.ID != job.ID {
		test.Fatal("same in-flight idempotent request was busy", err)
	}
	if _, err := controller.Start(test.Context(), uuid.New().String(), requestID); !errors.Is(err, ErrBusy) {
		test.Fatal("cross-principal request aliased active job", err)
	}
	if _, err := controller.get().service.WithContentSnapshot(test.Context(), captureOptions().Limits, func(context.Context, library.ContentSnapshotView) error { return nil }); err != nil {
		test.Fatal("slow network upload retained source snapshot barrier", err)
	}
	canceled, err := controller.Cancel(test.Context(), principal, job.ID)
	if err != nil || !canceled.CancelRequested {
		test.Fatal("cancel request did not persist", err)
	}
	store.get().mutex.Lock()
	durable := store.get().data.Jobs[0].Job.CancelRequested
	store.get().mutex.Unlock()
	if !durable {
		test.Fatal("worker canceled before durable cancellation")
	}
	releaseOnce.Do(func() { close(release) })
	finished := waitControlJob(test, controller, job.ID)
	if finished.Phase != "canceled" || !finished.CancelRequested {
		test.Fatal("explicit cancellation became success or rollback", finished)
	}
}

func TestControllerGenerationIsolationAndPagination(test *testing.T) {
	controller, store, fixture := controllerFixture(test)
	principal, requestID := uuid.New().String(), uuid.New().String()
	first, err := controller.Start(test.Context(), principal, requestID)
	if err != nil {
		test.Fatal(err)
	}
	if job := waitControlJob(test, controller, first.ID); job.Phase != "remote_committed" {
		test.Fatal(job)
	}
	second, err := controller.Start(test.Context(), uuid.New().String(), requestID)
	if err != nil || second.ID == first.ID {
		test.Fatal("idempotency was not principal-scoped", err)
	}
	if job := waitControlJob(test, controller, second.ID); job.Phase != "remote_committed" {
		test.Fatal(job)
	}
	page, err := controller.List(test.Context(), 1, "")
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != second.ID || page.Next != second.ID {
		test.Fatal("first page ordering", err)
	}
	page, err = controller.List(test.Context(), 1, page.Next)
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != first.ID || page.Next != "" {
		test.Fatal("second page ordering", err)
	}
	previous := controller.get()
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	options := previous.options
	options.Generation = [32]byte{2}
	reconfigured, err := NewController(test.Context(), store, previous.service, previous.target, previous.engine, options)
	if err != nil {
		test.Fatal(err)
	}
	defer reconfigured.Close(test.Context())
	before := len(fixture.snapshot())
	if _, err := reconfigured.Start(test.Context(), principal, requestID); !errors.Is(err, ErrControlConflict) {
		test.Fatal("generation change reused request", err)
	}
	if _, err := reconfigured.Reconcile(test.Context(), principal, first.ID); !errors.Is(err, ErrControlConflict) {
		test.Fatal("new generation probed old target", err)
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("generation mismatch reached network")
	}
	if _, err := reconfigured.Get(test.Context(), strings.ToUpper("aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa")); !errors.Is(err, ErrInvalid) {
		test.Fatal("noncanonical UUID admitted", err)
	}
	if _, err := reconfigured.Get(test.Context(), uuid.New().String()); !errors.Is(err, ErrControlNotFound) {
		test.Fatal("missing job not distinguished", err)
	}
}

func TestControllerCloseTimeoutDrainsBeforeStoreRelease(test *testing.T) {
	controller, store, _ := controllerFixture(test)
	engine := controller.get().engine.get()
	original := engine.ops.sync
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	engine.ops.sync = func(file *os.File) error {
		if strings.HasSuffix(file.Name(), "-1-00000000") {
			close(entered)
			<-release
		}
		return original(file)
	}
	job, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("capture did not enter controlled sync")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 20*time.Millisecond)
	defer cancel()
	if err := controller.Close(ctx); !errors.Is(err, ErrControlUncertain) || !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("close deadline pretended drain", err)
	}
	if err := store.Close(); !errors.Is(err, ErrBusy) {
		test.Fatal("store released while capture still active", err)
	}
	if _, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String()); !errors.Is(err, ErrClosed) {
		test.Fatal("closing controller accepted a task", err)
	}
	once.Do(func() { close(release) })
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal("controller did not finish drainage", err)
	}
	engine.ops.sync = original
	if store.get().data.Jobs[0].Job.ID != job.ID || store.get().data.Jobs[0].Job.Phase != "interrupted" {
		test.Fatal("shutdown did not retain interrupted task")
	}
}

func TestControllerPersistenceBarriers(test *testing.T) {
	for _, mode := range []string{"register", "catalog", "complete-intent", "committed-state"} {
		test.Run(mode, func(test *testing.T) {
			controller, store, fixture := controllerFixture(test)
			physical := store.get()
			original := physical.ops.sync
			physical.ops.sync = func(file afero.File) error {
				phase := ""
				if len(physical.data.Jobs) != 0 {
					phase = physical.data.Jobs[0].Job.Phase
				}
				if file != physical.directory && (mode == "register" && phase == "" || mode == "catalog" && phase == "capturing" || mode == "complete-intent" && phase == "uploading" || mode == "committed-state" && phase == "committing") {
					return errors.New("private-journal-persist-fault")
				}
				return original(file)
			}
			job, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String())
			if mode == "register" {
				if job.ID != "" || !errors.Is(err, ErrControlUncertain) {
					test.Fatal("unpersisted job was accepted", err)
				}
			} else {
				if err != nil {
					test.Fatal(err)
				}
				physical.mutex.Lock()
				active := controller.get().active
				physical.mutex.Unlock()
				if active != nil {
					select {
					case <-active.done:
					case <-time.After(5 * time.Second):
						test.Fatal("persist fault worker did not drain")
					}
				}
			}
			physical.mutex.Lock()
			physical.ops.sync = original
			uncertain := physical.uncertain
			physical.mutex.Unlock()
			if !uncertain {
				test.Fatal("physical persistence failure was treated as ordinary failure")
			}
			puts, complete := 0, 0
			for _, call := range fixture.snapshot() {
				if call.method == http.MethodPut {
					puts++
					if strings.Contains(call.key, "/complete/") {
						complete++
					}
				}
			}
			expected := map[string]int{"register": 0, "catalog": 0, "complete-intent": 6, "committed-state": 7}[mode]
			if puts != expected || mode != "committed-state" && complete != 0 {
				test.Fatal("remote effect crossed an unpersisted barrier", puts, complete)
			}
			if err := controller.Close(test.Context()); !errors.Is(err, ErrControlUncertain) {
				test.Fatal("uncertain controller allowed service teardown", err)
			}
			if err := store.Close(); !errors.Is(err, ErrControlUncertain) {
				test.Fatal("uncertain control store released lock/root", err)
			}
		})
	}
}

func TestControllerCloseDeadlineIncludesJournalPersistence(test *testing.T) {
	controller, store, _ := controllerFixture(test)
	physical := store.get()
	original := physical.ops.sync
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(release) })
	var block atomic.Bool
	block.Store(true)
	physical.ops.sync = func(file afero.File) error {
		if block.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
		return original(file)
	}
	started := make(chan error, 1)
	go func() {
		_, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String())
		started <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("registration did not enter journal sync")
	}
	ctx, cancel := context.WithTimeout(test.Context(), 25*time.Millisecond)
	defer cancel()
	closed := make(chan error, 1)
	go func() { closed <- controller.Close(ctx) }()
	select {
	case err := <-closed:
		if !errors.Is(err, ErrControlUncertain) || !errors.Is(err, context.DeadlineExceeded) {
			test.Fatal("blocked journal sync lost bounded close uncertainty", err)
		}
	case <-time.After(500 * time.Millisecond):
		test.Fatal("Close ignored its deadline while journal persistence held the mutex")
	}
	if _, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String()); !errors.Is(err, ErrClosed) {
		test.Fatal("closing controller accepted a task during blocked journal sync", err)
	}
	if _, err := physical.root.Stat("."); err != nil {
		test.Fatal("close timeout released owned root", err)
	}
	releaseOnce.Do(func() { close(release) })
	select {
	case err := <-started:
		if err != nil {
			test.Fatal("registered task lost its durable result", err)
		}
	case <-time.After(5 * time.Second):
		test.Fatal("registration did not drain after journal sync release")
	}
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal("close retry did not drain persisted worker", err)
	}
	physical.ops.sync = original
	if err := store.Close(); err != nil {
		test.Fatal("drained controller retained healthy store", err)
	}
}

func TestControllerCancelAtCompleteRemainsUnknown(test *testing.T) {
	controller, _, fixture := controllerFixture(test)
	entered, release := make(chan struct{}), make(chan struct{})
	var enterOnce, releaseOnce sync.Once
	var blockComplete atomic.Bool
	blockComplete.Store(true)
	defer releaseOnce.Do(func() { close(release) })
	fixture.hook = func(_ http.ResponseWriter, request *http.Request, key string) bool {
		if blockComplete.Load() && request.Method == http.MethodHead && strings.Contains(key, "/complete/") {
			enterOnce.Do(func() { close(entered) })
			select {
			case <-release:
			case <-request.Context().Done():
			}
			return true
		}
		return false
	}
	principal := uuid.New().String()
	job, err := controller.Start(test.Context(), principal, uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("complete was not attempted")
	}
	if _, err := controller.Cancel(test.Context(), principal, job.ID); err != nil {
		test.Fatal(err)
	}
	releaseOnce.Do(func() { close(release) })
	finished := waitControlJob(test, controller, job.ID)
	if finished.Phase != "commit_unknown" || !finished.CancelRequested {
		test.Fatal("cancel pretended to roll back attempted complete", finished)
	}
	blockComplete.Store(false)
	before := len(fixture.snapshot())
	reconciled, err := controller.Reconcile(test.Context(), principal, job.ID)
	if err != nil || reconciled.Phase != "remote_committed" || !reconciled.CancelRequested {
		test.Fatal("exact readonly reconciliation could not establish canceled commit", err, reconciled)
	}
	for _, call := range fixture.snapshot()[before:] {
		if call.method == http.MethodPut {
			test.Fatal("cancel reconciliation replayed complete")
		}
	}
}

func TestControllerRestartNormalizesAndOldCloseCannotDetach(test *testing.T) {
	controller, store, fixture := controllerFixture(test)
	principal, requestID := uuid.New().String(), uuid.New().String()
	job, err := controller.Start(test.Context(), principal, requestID)
	if err != nil {
		test.Fatal(err)
	}
	if finished := waitControlJob(test, controller, job.ID); finished.Phase != "remote_committed" {
		test.Fatal(finished)
	}
	previous := controller.get()
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	candidate := store.get().clone()
	candidate.Jobs[0].Job.Phase = "committing"
	for _, phase := range []string{"pending", "capturing"} {
		record := controlTestRecord()
		record.Job.Phase = phase
		record.Generation = candidate.Jobs[0].Generation
		record.Options = candidate.Jobs[0].Options
		candidate.Jobs = append(candidate.Jobs, record)
	}
	if err := store.get().persist(candidate); err != nil {
		test.Fatal(err)
	}
	before := len(fixture.snapshot())
	restarted, err := NewController(test.Context(), store, previous.service, previous.target, previous.engine, previous.options)
	if err != nil {
		test.Fatal(err)
	}
	defer restarted.Close(test.Context())
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := store.Close(); !errors.Is(err, ErrBusy) {
		test.Fatal("stale controller Close detached new controller", err)
	}
	records := store.get().data.Jobs
	if records[0].Job.Phase != "commit_unknown" || records[1].Job.Phase != "interrupted" || records[2].Job.Phase != "interrupted" {
		test.Fatal("restart did not preserve unknown intent / interrupt running tasks")
	}
	if len(fixture.snapshot()) != before {
		test.Fatal("restart performed a remote side effect")
	}
	if existing, err := restarted.Start(test.Context(), principal, requestID); err != nil || existing.ID != job.ID || existing.Phase != "commit_unknown" {
		test.Fatal("restart lost attempt idempotency", err)
	}
}

func waitControlJob(test *testing.T, controller *Controller, id string) Job {
	test.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		job, err := controller.Get(test.Context(), id)
		if err != nil {
			test.Fatal(err)
		}
		if job.Phase != "pending" && job.Phase != "capturing" && job.Phase != "uploading" && job.Phase != "committing" {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	test.Fatal("controller job did not finish")
	return Job{}
}

func TestControllerDurableCaptureTLSAndIdempotency(test *testing.T) {
	controller, store, fixture := controllerFixture(test)
	principal, requestID := uuid.New().String(), uuid.New().String()
	fixture.hook = func(_ http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == http.MethodPut {
			physical := store.get()
			physical.mutex.Lock()
			record := physical.data.Jobs[0]
			physical.mutex.Unlock()
			if len(record.Catalog) != 7 || record.Job.Objects != 7 {
				test.Error("PUT preceded durable complete catalog")
			}
			if strings.Contains(key, "/complete/") && !record.CompleteAttempted {
				test.Error("complete PUT preceded durable intent")
			}
		}
		return false
	}
	ctx, cancel := context.WithCancel(test.Context())
	job, err := controller.Start(ctx, principal, requestID)
	cancel()
	if err != nil {
		test.Fatal(err)
	}
	finished := waitControlJob(test, controller, job.ID)
	if finished.Phase != "remote_committed" || finished.Objects != 7 || finished.CiphertextBytes <= 0 {
		test.Fatal("TLS capture did not commit", finished)
	}
	again, err := controller.Start(test.Context(), principal, requestID)
	if err != nil || again.ID != job.ID {
		test.Fatal("durable idempotency missed", err)
	}
	puts := 0
	for _, call := range fixture.snapshot() {
		if call.method == http.MethodPut {
			puts++
			if puts == 7 && !strings.Contains(call.key, "/complete/") {
				test.Fatal("complete was not last")
			}
		}
	}
	if puts != 7 {
		test.Fatal("idempotent request replayed PUT", puts)
	}
	page, err := controller.List(test.Context(), 1, "")
	if err != nil || len(page.Jobs) != 1 || page.Jobs[0].ID != job.ID || page.Next != "" {
		test.Fatal("safe job listing failed", err)
	}
	for _, value := range []any{store, controller} {
		encoded, err := json.Marshal(value)
		var logged bytes.Buffer
		slog.New(slog.NewTextHandler(&logged, nil)).Info("private", "handle", value)
		if err != nil || string(encoded) != "{}" || strings.Contains(fmt.Sprintf("%#v %+v", value, value)+logged.String(), principal) {
			test.Fatal("control handle leaked internals", err)
		}
	}
	if err := store.Close(); !errors.Is(err, ErrBusy) {
		test.Fatal("store closed under live controller", err)
	}
	store.get().mutex.Lock()
	encoded, readError := store.get().read(controlLogName)
	store.get().mutex.Unlock()
	defer clear(encoded)
	if readError != nil {
		test.Fatal(readError)
	}
	for _, secret := range []string{principal, requestID, "SYNTHETIC-PRIVATE-SECRET", "capture-master-key-secret-sentinel"} {
		if bytes.Contains(encoded, []byte(secret)) {
			test.Fatal("private journal contained plaintext sensitive value")
		}
	}
}

func TestControllerOptionsAndJobBudget(test *testing.T) {
	controller, store, _ := controllerFixture(test)
	previous := controller.get()
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	options := previous.options
	for _, mode := range []string{"zero-generation", "zero-jobs", "too-many-jobs", "no-timeout", "bad-version", "bad-limits"} {
		test.Run(mode, func(test *testing.T) {
			invalid := options
			switch mode {
			case "zero-generation":
				invalid.Generation = [32]byte{}
			case "zero-jobs":
				invalid.MaxJobs = 0
			case "too-many-jobs":
				invalid.MaxJobs = 1001
			case "no-timeout":
				invalid.JobTimeout = 0
			case "bad-version":
				invalid.AppVersion = "private\nvalue"
			case "bad-limits":
				invalid.ContentLimits.Database.DrainTimeout = 0
			}
			if bad, err := NewController(test.Context(), store, previous.service, previous.target, previous.engine, invalid); bad != nil || !errors.Is(err, ErrInvalid) {
				test.Fatal("invalid controller options accepted", err)
			}
		})
	}
	options.MaxJobs = 1
	bounded, err := NewController(test.Context(), store, previous.service, previous.target, previous.engine, options)
	if err != nil {
		test.Fatal(err)
	}
	defer bounded.Close(test.Context())
	job, err := bounded.Start(test.Context(), uuid.New().String(), uuid.New().String())
	if err != nil {
		test.Fatal(err)
	}
	if finished := waitControlJob(test, bounded, job.ID); finished.Phase != "remote_committed" {
		test.Fatal(finished)
	}
	if _, err := bounded.Start(test.Context(), uuid.New().String(), uuid.New().String()); !errors.Is(err, ErrControlBudget) {
		test.Fatal("retained job budget silently dropped history", err)
	}
	if _, err := bounded.List(test.Context(), 0, ""); !errors.Is(err, ErrInvalid) {
		test.Fatal("invalid page limit admitted", err)
	}
	if _, err := bounded.Get(nil, job.ID); !errors.Is(err, ErrInvalid) {
		test.Fatal("nil context admitted", err)
	}
}
