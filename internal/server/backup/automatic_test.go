package backup

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"
	"uuid"

	"github.com/spf13/afero"
)

func automaticFixture(test *testing.T) (*Controller, *ControlStore, *s3Fixture) {
	test.Helper()
	previous, store, fixture := controllerFixture(test)
	state := previous.get()
	if err := previous.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	options := state.options
	options.AutomaticInterval = time.Minute
	controller, err := NewController(test.Context(), store, state.service, state.target, state.engine, options)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := controller.Close(ctx); err != nil {
			test.Error(err)
		}
	})
	return controller, store, fixture
}

func automaticJournal(test *testing.T, store *ControlStore) []byte {
	test.Helper()
	physical := store.get()
	physical.mutex.Lock()
	defer physical.mutex.Unlock()
	data, err := physical.read(controlLogName)
	if err != nil {
		test.Fatal(err)
	}
	return data
}

func TestAutomaticFirstDelayAndNoCatchUp(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		done, release := make(chan struct{}), make(chan struct{})
		var calls atomic.Int32
		go func() {
			defer close(done)
			automaticLoop(ctx, time.Minute, func(context.Context) error {
				if calls.Add(1) == 1 {
					<-release
				}
				return nil
			})
		}()
		synctest.Wait()
		time.Sleep(time.Minute - time.Nanosecond)
		if calls.Load() != 0 {
			test.Fatal("first automatic tick was immediate")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 1 {
			test.Fatal("first due tick was not executed")
		}
		time.Sleep(10 * time.Minute)
		close(release)
		synctest.Wait()
		if calls.Load() != 1 {
			test.Fatal("blocked tick accumulated catch-up work")
		}
		time.Sleep(time.Minute - time.Nanosecond)
		if calls.Load() != 1 {
			test.Fatal("timer was measured from the previous due slot")
		}
		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if calls.Load() != 2 {
			test.Fatal("next fixed-delay tick missing")
		}
		cancel()
		<-done
		time.Sleep(time.Hour)
		if calls.Load() != 2 {
			test.Fatal("canceled scheduler created later work")
		}
	})
}

func TestAutomaticStartStopAndDisabled(test *testing.T) {
	for _, enabled := range []bool{false, true} {
		test.Run(map[bool]string{false: "off", true: "on"}[enabled], func(test *testing.T) {
			controller, store, _ := automaticFixture(test)
			if !enabled {
				controller.get().options.AutomaticInterval = 0
			}
			if controller.AutomaticEnabled() != enabled {
				test.Fatal("automatic status does not reflect configured policy")
			}
			before := automaticJournal(test, store)
			if err := controller.StartAutomatic(test.Context()); err != nil {
				test.Fatal(err)
			}
			done := controller.get().automatic.done
			if enabled == (done == nil) {
				test.Fatal("disabled scheduler owns a loop or enabled scheduler owns none")
			}
			if err := controller.StartAutomatic(test.Context()); err != nil || controller.get().automatic.done != done {
				test.Fatal("duplicate Start created another automatic loop", err)
			}
			var workers sync.WaitGroup
			errorsFound := make(chan error, 8)
			for range 8 {
				workers.Go(func() { errorsFound <- controller.StopAutomatic(test.Context()) })
			}
			workers.Wait()
			close(errorsFound)
			for err := range errorsFound {
				if err != nil {
					test.Fatal(err)
				}
			}
			if enabled && !errors.Is(controller.StartAutomatic(test.Context()), ErrClosed) {
				test.Fatal("stopped scheduler silently restarted")
			}
			if !bytes.Equal(before, automaticJournal(test, store)) {
				test.Fatal("first delay or shutdown registered a job")
			}
		})
	}
}

func TestAutomaticSharesAdmission(test *testing.T) {
	controller, _, fixture := automaticFixture(test)
	release := make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
		if request.Method == http.MethodPut {
			select {
			case <-release:
			case <-request.Context().Done():
			}
		}
		return false
	}
	var workers sync.WaitGroup
	var admitted atomic.Int32
	problems := make(chan error, 20)
	for index := range 20 {
		workers.Go(func() {
			var err error
			if index%2 == 0 {
				err = controller.automaticStep(test.Context())
			} else {
				_, err = controller.Start(test.Context(), uuid.New().String(), uuid.New().String())
			}
			if err == nil {
				admitted.Add(1)
			} else if !errors.Is(err, ErrBusy) {
				problems <- err
			}
		})
	}
	workers.Wait()
	close(problems)
	for err := range problems {
		test.Fatal(err)
	}
	page, err := controller.List(test.Context(), 1000, "")
	if err != nil || admitted.Load() != 1 || len(page.Jobs) != 1 {
		test.Fatal("manual and automatic work do not share one admission slot", err)
	}
	once.Do(func() { close(release) })
	if job := waitControlJob(test, controller, page.Jobs[0].ID); job.Phase != "remote_committed" {
		test.Fatal("admitted automatic/manual task failed", job)
	}
}

func TestAutomaticUnknownIsNotResubmitted(test *testing.T) {
	for _, mode := range []string{"data-put", "complete-response"} {
		test.Run(mode, func(test *testing.T) {
			controller, store, fixture := automaticFixture(test)
			var injecting atomic.Bool
			injecting.Store(true)
			fixture.hook = func(writer http.ResponseWriter, request *http.Request, key string) bool {
				if injecting.Load() && (mode == "data-put" && request.Method == http.MethodPut && strings.Contains(key, "/data/") || mode == "complete-response" && request.Method == http.MethodHead && strings.Contains(key, "/complete/")) {
					fixtureError(writer, 503, "InternalError")
					return true
				}
				return false
			}
			if err := controller.automaticStep(test.Context()); err != nil {
				test.Fatal(err)
			}
			page, err := controller.List(test.Context(), 1, "")
			if err != nil || len(page.Jobs) != 1 {
				test.Fatal(err)
			}
			job := waitControlJob(test, controller, page.Jobs[0].ID)
			want := "interrupted"
			if mode == "complete-response" {
				want = "commit_unknown"
			}
			if job.Phase != want || job.Code != "put_unknown" {
				test.Fatal("real unknown publication was not retained", job)
			}
			before, calls := automaticJournal(test, store), len(fixture.snapshot())
			for range 3 {
				if err := controller.automaticStep(test.Context()); !errors.Is(err, ErrControlConflict) {
					test.Fatal("unknown was hidden by a fresh automatic request", err)
				}
			}
			if calls != len(fixture.snapshot()) || !bytes.Equal(before, automaticJournal(test, store)) {
				test.Fatal("unresolved automatic ticks touched network or journal")
			}
			if err := controller.Close(test.Context()); err != nil {
				test.Fatal(err)
			}
			previous := controller.get()
			restarted, err := NewController(test.Context(), store, previous.service, previous.target, previous.engine, previous.options)
			if err != nil {
				test.Fatal(err)
			}
			defer restarted.Close(test.Context())
			if err := restarted.automaticStep(test.Context()); !errors.Is(err, ErrControlConflict) || calls != len(fixture.snapshot()) {
				test.Fatal("restart replayed automatic unknown", err)
			}
			injecting.Store(false)
			if mode == "data-put" {
				retired, err := restarted.Cancel(test.Context(), uuid.New().String(), job.ID)
				if err != nil || retired.Phase != "canceled" {
					test.Fatal("administrator could not retire incomplete unknown", err)
				}
			} else {
				verified, err := restarted.Reconcile(test.Context(), uuid.New().String(), job.ID)
				if err != nil || verified.Phase != "remote_committed" {
					test.Fatal("administrator could not reconcile automatic instance actor", err)
				}
				for _, call := range fixture.snapshot()[calls:] {
					if call.method != http.MethodGet && call.method != http.MethodHead {
						test.Fatal("automatic unknown reconciliation wrote remote objects")
					}
				}
			}
			if err := restarted.automaticStep(test.Context()); err != nil {
				test.Fatal("retired unknown still blocks the next independent interval", err)
			}
			page, err = restarted.List(test.Context(), 2, "")
			if err != nil || len(page.Jobs) != 2 || page.Jobs[0].ID == job.ID {
				test.Fatal("new cycle reused old unknown identity", err)
			}
			if next := waitControlJob(test, restarted, page.Jobs[0].ID); next.Phase != "remote_committed" {
				test.Fatal(next)
			}
		})
	}
}

func TestAutomaticCapacityAndCrossGenerationBlock(test *testing.T) {
	for _, mode := range []string{"1000-jobs", "16MiB", "old-interrupted"} {
		test.Run(mode, func(test *testing.T) {
			controller, store, fixture := automaticFixture(test)
			physical := store.get()
			journal := physical.clone()
			want := ErrControlBudget
			switch mode {
			case "1000-jobs":
				for range 1000 {
					record := controlTestRecord()
					record.Job.Phase = "failed"
					journal.Jobs = append(journal.Jobs, record)
				}
			case "16MiB":
				pending := controlTestRecord()
				pending.Generation = hex.EncodeToString(controller.get().options.Generation[:])
				pending.Options = controller.get().fingerprint
				journal = controlCapacityHistory(test, journal, pending, 0)
			case "old-interrupted":
				record := controlTestRecord()
				record.Job.Phase, record.Job.Code = "interrupted", "interrupted"
				record.Generation = strings.Repeat("ab", 32)
				journal.Jobs = append(journal.Jobs, record)
				want = ErrControlConflict
			}
			if err := physical.persist(journal); err != nil {
				test.Fatal(err)
			}
			before := automaticJournal(test, store)
			if err := controller.automaticStep(test.Context()); !errors.Is(err, want) {
				test.Fatal("automatic admission ignored retained budget or old generation", err)
			}
			if !bytes.Equal(before, automaticJournal(test, store)) || len(fixture.snapshot()) != 0 {
				test.Fatal("rejected automatic work modified retained history or network")
			}
		})
	}
}

func TestAutomaticCloseBeforeControllerDrain(test *testing.T) {
	controller, store, _ := automaticFixture(test)
	physical := store.get()
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(release) })
	original := physical.ops.sync
	var blocking atomic.Bool
	blocking.Store(true)
	physical.ops.sync = func(file afero.File) error {
		if blocking.CompareAndSwap(true, false) {
			close(entered)
			<-release
		}
		return original(file)
	}
	ctx, cancel := context.WithCancel(test.Context())
	defer cancel()
	schedule := &controller.get().automatic
	schedule.cancel, schedule.done = cancel, make(chan struct{})
	stepDone := make(chan error, 1)
	go func() {
		defer close(schedule.done)
		stepDone <- controller.automaticStep(ctx)
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		test.Fatal("automatic registration never reached real encrypted journal sync")
	}
	closing, stop := context.WithTimeout(test.Context(), 25*time.Millisecond)
	defer stop()
	if err := controller.Close(closing); !errors.Is(err, ErrControlUncertain) || !errors.Is(err, context.DeadlineExceeded) {
		test.Fatal("blocked automatic producer lost bounded uncertainty", err)
	}
	if controller.get().ctx.Err() != nil || !errors.Is(controller.StartAutomatic(test.Context()), ErrClosed) {
		test.Fatal("controller drained before automatic producer acknowledgement")
	}
	once.Do(func() { close(release) })
	<-stepDone
	var workers sync.WaitGroup
	failures := make(chan error, 2)
	workers.Go(func() { failures <- controller.StopAutomatic(test.Context()) })
	workers.Go(func() { failures <- controller.Close(test.Context()) })
	workers.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			test.Fatal("concurrent scheduler stop and controller close failed", err)
		}
	}
	if _, err := controller.Start(test.Context(), uuid.New().String(), uuid.New().String()); !errors.Is(err, ErrClosed) {
		test.Fatal("late registration survived successful shutdown")
	}
}

func TestAutomaticPolicyKeepsTargetFingerprint(test *testing.T) {
	controller, store, fixture := automaticFixture(test)
	state := controller.get()
	if err := controller.automaticStep(test.Context()); err != nil {
		test.Fatal(err)
	}
	page, err := controller.List(test.Context(), 1, "")
	if err != nil || len(page.Jobs) != 1 {
		test.Fatal(err)
	}
	if job := waitControlJob(test, controller, page.Jobs[0].ID); job.Phase != "remote_committed" {
		test.Fatal(job)
	}
	record := store.get().data.Jobs[0]
	if record.Principal != uuid.UUID(store.InstanceID()).String() {
		test.Fatal("automatic task did not retain the stable instance actor")
	}
	if err := controller.Close(test.Context()); err != nil {
		test.Fatal(err)
	}
	for _, interval := range []time.Duration{-1, time.Second, time.Minute - 1, 24*time.Hour + 1} {
		options := state.options
		options.AutomaticInterval = interval
		if invalid, err := NewController(test.Context(), store, state.service, state.target, state.engine, options); invalid != nil || !errors.Is(err, ErrInvalid) {
			test.Fatal("invalid scheduling interval accepted", err)
		}
	}
	options := state.options
	options.AutomaticInterval = time.Hour
	reopened, err := NewController(test.Context(), store, state.service, state.target, state.engine, options)
	if err != nil {
		test.Fatal(err)
	}
	defer reopened.Close(test.Context())
	calls := len(fixture.snapshot())
	repeated, err := reopened.Start(test.Context(), record.Principal, record.Request)
	if err != nil || repeated.ID != record.Job.ID || calls != len(fixture.snapshot()) {
		test.Fatal("interval changed durable Start idempotency", err)
	}
	if verified, err := reopened.Reconcile(test.Context(), uuid.New().String(), record.Job.ID); err != nil || verified.Phase != "remote_committed" {
		test.Fatal("interval-only change blocked old target reconciliation", err)
	}
	for _, call := range fixture.snapshot()[calls:] {
		if call.method != http.MethodGet && call.method != http.MethodHead {
			test.Fatal("policy update triggered remote writes")
		}
	}
}

func TestAutomaticCanceledAdmissionHasNoLateRegistration(test *testing.T) {
	controller, store, fixture := automaticFixture(test)
	before := automaticJournal(test, store)
	physical := store.get()
	physical.mutex.Lock()
	ctx, cancel := context.WithCancel(test.Context())
	done := make(chan error, 1)
	go func() { done <- controller.automaticStep(ctx) }()
	cancel()
	physical.mutex.Unlock()
	if err := <-done; !errors.Is(err, context.Canceled) {
		test.Fatal("canceled automatic tick retained admission", err)
	}
	if !bytes.Equal(before, automaticJournal(test, store)) || len(fixture.snapshot()) != 0 {
		test.Fatal("canceled tick registered late work")
	}
}
