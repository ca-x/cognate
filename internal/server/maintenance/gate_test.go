//go:build unix

package maintenance_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/ca-x/cognate/internal/server/maintenance"
)

type quiesceResult struct {
	token *maintenance.Exclusive
	err   error
}

func joined(test *testing.T) *maintenance.Instance {
	test.Helper()
	instance, err := maintenance.Join(test.Context(), privateDirectory(test))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() {
		if err := instance.Close(); err != nil {
			test.Error(err)
		}
	})
	return instance
}

func enter(test *testing.T, instance *maintenance.Instance, ctx context.Context) (context.Context, func()) {
	test.Helper()
	leased, release, err := instance.Enter(ctx)
	if err != nil {
		test.Fatal(err)
	}
	return leased, release
}

func expectEnterError(test *testing.T, instance *maintenance.Instance, ctx context.Context, expected error) {
	test.Helper()
	leased, release, err := instance.Enter(ctx)
	if release != nil {
		release()
	}
	if leased != nil || release != nil || !errors.Is(err, expected) {
		test.Fatalf("Enter expected %v, got %v", expected, err)
	}
}

func pendingQuiesce(instance *maintenance.Instance, ctx context.Context) (<-chan quiesceResult, func()) {
	result := make(chan quiesceResult, 1)
	finish := make(chan struct{})
	go func() {
		token, err := instance.Quiesce(ctx)
		result <- quiesceResult{token, err}
		<-finish
		if token != nil {
			token.Release()
		}
	}()
	return result, sync.OnceFunc(func() { close(finish) })
}

func TestNestedLeasesRemainCounted(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		parent, releaseParent := enter(test, instance, test.Context())
		defer releaseParent()
		result, finish := pendingQuiesce(instance, test.Context())
		defer finish()
		synctest.Wait()
		expectEnterError(test, instance, test.Context(), maintenance.ErrQuiescing)
		child, releaseChild := enter(test, instance, parent)
		defer releaseChild()
		releaseParent()
		releaseParent()
		synctest.Wait()
		select {
		case <-result:
			test.Fatal("quiesce ignored an independent child lease")
		default:
		}
		expectEnterError(test, instance, parent, maintenance.ErrLease)
		_, releaseGrandchild := enter(test, instance, child)
		defer releaseGrandchild()
		releaseChild()
		synctest.Wait()
		select {
		case <-result:
			test.Fatal("quiesce ignored an independent grandchild lease")
		default:
		}
		releaseGrandchild()
		synctest.Wait()
		outcome := <-result
		if outcome.err != nil || !outcome.token.ValidFor(instance) {
			test.Fatalf("exclusive acquisition failed: %v", outcome.err)
		}
		other := joined(test)
		if outcome.token.ValidFor(other) {
			test.Fatal("exclusive token authorized a different directory")
		}
		outcome.token.Release()
		outcome.token.Release()
		if outcome.token.ValidFor(instance) {
			test.Fatal("released exclusive token remained valid")
		}
		_, release := enter(test, instance, test.Context())
		release()
	})
}

func TestCanceledQuiesceRestoresAdmission(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		_, releaseParent := enter(test, instance, test.Context())
		defer releaseParent()
		ctx, cancel := context.WithCancel(test.Context())
		defer cancel()
		result, finish := pendingQuiesce(instance, ctx)
		defer finish()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if outcome := <-result; !errors.Is(outcome.err, context.Canceled) || outcome.token != nil {
			test.Fatalf("canceled quiesce issued a token: %v", outcome.err)
		}
		_, release := enter(test, instance, test.Context())
		release()
		releaseParent()
		token, err := instance.Quiesce(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		token.Release()
	})
}

func TestLeaseAndExclusiveScope(test *testing.T) {
	instance := joined(test)
	parent, release := enter(test, instance, test.Context())
	defer release()
	if token, err := instance.Quiesce(parent); token != nil || !errors.Is(err, maintenance.ErrLease) {
		test.Fatalf("quiesce waited on its own writer: %v", err)
	}
	other := joined(test)
	expectEnterError(test, other, parent, maintenance.ErrLease)
	release()
	ctx, cancel := context.WithCancel(test.Context())
	token, err := instance.Quiesce(ctx)
	if err != nil {
		test.Fatal(err)
	}
	defer token.Release()
	cancel()
	if !token.ValidFor(instance) {
		test.Fatal("ctx cancellation implicitly released a held exclusive")
	}
	expectEnterError(test, instance, test.Context(), maintenance.ErrQuiescing)
	if competing, err := instance.Quiesce(test.Context()); competing != nil || !errors.Is(err, maintenance.ErrQuiescing) {
		test.Fatalf("competing exclusive accepted: %v", err)
	}
	token.Release()
	if competing, err := instance.Quiesce(ctx); competing != nil || !errors.Is(err, context.Canceled) {
		test.Fatalf("pre-canceled exclusive accepted: %v", err)
	}
	expectEnterError(test, instance, ctx, context.Canceled)
}

func TestCloseWaitsForEachChild(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		parent, releaseParent := enter(test, instance, test.Context())
		defer releaseParent()
		child, releaseChild := enter(test, instance, parent)
		defer releaseChild()
		closed := make(chan error, 1)
		go func() { closed <- instance.Close() }()
		synctest.Wait()
		expectEnterError(test, instance, test.Context(), maintenance.ErrClosed)
		releaseParent()
		_, releaseGrandchild := enter(test, instance, child)
		defer releaseGrandchild()
		releaseChild()
		synctest.Wait()
		select {
		case <-closed:
			test.Fatal("Close returned before grandchild released")
		default:
		}
		releaseGrandchild()
		synctest.Wait()
		if err := <-closed; err != nil {
			test.Fatal(err)
		}
		expectEnterError(test, instance, child, maintenance.ErrClosed)
		if err := instance.Close(); err != nil {
			test.Fatal(err)
		}
	})
}

func TestAsynchronousChildAcrossHandles(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		directory := privateDirectory(test)
		first, err := maintenance.Join(test.Context(), directory)
		if err != nil {
			test.Fatal(err)
		}
		defer first.Close()
		second, err := maintenance.Join(test.Context(), directory)
		if err != nil {
			test.Fatal(err)
		}
		defer second.Close()
		parent, releaseParent := enter(test, first, test.Context())
		defer releaseParent()
		result, finish := pendingQuiesce(second, test.Context())
		defer finish()
		synctest.Wait()
		ctx, cancel := context.WithCancel(parent)
		defer cancel()
		entered := make(chan error, 1)
		releaseChild := make(chan struct{})
		resume := sync.OnceFunc(func() { close(releaseChild) })
		defer resume()
		go func() {
			_, release, err := second.Enter(ctx)
			entered <- err
			if err == nil {
				defer release()
				<-releaseChild
			}
		}()
		if err := <-entered; err != nil {
			test.Fatal(err)
		}
		releaseParent()
		if err := first.Close(); err != nil {
			test.Fatal(err)
		}
		cancel()
		synctest.Wait()
		select {
		case <-result:
			test.Fatal("parent Close or child ctx cancellation released an active async child")
		default:
		}
		resume()
		synctest.Wait()
		outcome := <-result
		if outcome.err != nil || !outcome.token.ValidFor(second) || outcome.token.ValidFor(first) {
			test.Fatalf("shared exclusive lifecycle: %v", outcome.err)
		}
		outcome.token.Release()
	})
}

func TestCloseCancelsItsPendingQuiesce(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		_, release := enter(test, instance, test.Context())
		defer release()
		result, finish := pendingQuiesce(instance, test.Context())
		defer finish()
		synctest.Wait()
		closed := make(chan error, 1)
		go func() { closed <- instance.Close() }()
		synctest.Wait()
		if outcome := <-result; outcome.token != nil || !errors.Is(outcome.err, maintenance.ErrClosed) {
			test.Fatalf("closing pending quiesce issued a token: %v", outcome.err)
		}
		select {
		case <-closed:
			test.Fatal("Close ignored an active writer")
		default:
		}
		release()
		synctest.Wait()
		if err := <-closed; err != nil {
			test.Fatal(err)
		}
	})
}

func TestQuiesceDeadlineDoesNotReleaseWriter(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		_, release := enter(test, instance, test.Context())
		defer release()
		ctx, cancel := context.WithTimeout(test.Context(), time.Second)
		defer cancel()
		result, finish := pendingQuiesce(instance, ctx)
		defer finish()
		if outcome := <-result; outcome.token != nil || !errors.Is(outcome.err, context.DeadlineExceeded) {
			test.Fatalf("deadline quiesce: %v", outcome.err)
		}
		_, releaseOther := enter(test, instance, test.Context())
		releaseOther()
		result, finishOther := pendingQuiesce(instance, test.Context())
		defer finishOther()
		synctest.Wait()
		select {
		case <-result:
			test.Fatal("deadline secretly discarded an existing writer")
		default:
		}
		release()
		synctest.Wait()
		if outcome := <-result; outcome.err != nil || !outcome.token.ValidFor(instance) {
			test.Fatalf("retry after canceled quiesce: %v", outcome.err)
		}
		finishOther()
		synctest.Wait()
	})
}

func TestCopiedHandlesAndStaleExclusive(test *testing.T) {
	instance := joined(test)
	first, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	first.Release()
	second, err := instance.Quiesce(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	defer second.Release()
	first.Release()
	copyToken := *second
	copyToken.Release()
	if !second.ValidFor(instance) || copyToken.ValidFor(instance) {
		test.Fatal("copied or stale token changed the active exclusive")
	}
	second.Release()
	copyHandle := *instance
	if err := copyHandle.Close(); err != nil {
		test.Fatal(err)
	}
	expectEnterError(test, instance, test.Context(), maintenance.ErrClosed)
	if err := instance.Close(); err != nil {
		test.Fatal(err)
	}
	var zero maintenance.Exclusive
	zero.Release()
	if zero.ValidFor(instance) {
		test.Fatal("zero token is valid")
	}
}

func TestCloseWaitsForExclusive(test *testing.T) {
	synctest.Test(test, func(test *testing.T) {
		instance := joined(test)
		token, err := instance.Quiesce(test.Context())
		if err != nil {
			test.Fatal(err)
		}
		defer token.Release()
		closed := make(chan error, 1)
		go func() { closed <- instance.Close() }()
		synctest.Wait()
		select {
		case <-closed:
			test.Fatal("Close returned with a live exclusive")
		default:
		}
		if !token.ValidFor(instance) {
			test.Fatal("Close revoked an in-use exclusive")
		}
		token.Release()
		synctest.Wait()
		if err := <-closed; err != nil {
			test.Fatal(err)
		}
	})
}
