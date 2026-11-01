package packattachments

import (
	"errors"
	"testing"
	"time"
)

func TestTypedReviewIsDetachedAndExpiresWithOperation(test *testing.T) {
	fixture := newFixture(test)
	client, err := New(fixture.origin, fixture.session, fixture.transport)
	if err != nil {
		test.Fatal(err)
	}
	defer client.Close()
	operation, err := client.Prepare(test.Context(), fixture.pack, 1, 1, []Reference{{AssetID: fixture.asset, ExpectedAssetRevision: 1, TargetID: "claude.provider.user"}})
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	review, err := operation.Review()
	if err != nil || review.Origin != fixture.origin || review.OwnerID != fixture.owner || review.Before.PackID != fixture.pack || len(review.References) != 1 || review.Confirmation != operation.Confirmation() || review.Expires.IsZero() {
		test.Fatal("typed review must bind origin, owner, both revisions and exact ordered references")
	}
	review.References[0].AssetID = "changed"
	again, err := operation.Review()
	if err != nil || again.References[0].AssetID != fixture.asset {
		test.Fatal("review aliases operation")
	}
	if _, err := operation.Submit(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := operation.Review(); !errors.Is(err, ErrClosed) {
		test.Fatal("used review remains actionable")
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrClosed) || fixture.control.replace.Load() != 1 {
		test.Fatal("used operation submitted twice")
	}
	operation.Close()
	if _, err := operation.Review(); !errors.Is(err, ErrClosed) {
		test.Fatal("closed review remains readable")
	}
	next, err := client.Prepare(test.Context(), fixture.pack, 2, 2, []Reference{})
	if err != nil {
		test.Fatal(err)
	}
	defer next.Close()
	nested, err := next.Review()
	if err != nil {
		test.Fatal(err)
	}
	nested.Before.Entries[0].AssetID = "changed"
	again, err = next.Review()
	if err != nil || again.Before.Entries[0].AssetID != fixture.asset {
		test.Fatal("before entries alias review")
	}
	next.state().mutex.Lock()
	next.state().expires = time.Now().Add(-time.Second)
	next.state().mutex.Unlock()
	if _, err := next.Review(); !errors.Is(err, ErrClosed) || next.Confirmation() != "" {
		test.Fatal("expired review remains actionable")
	}
}
