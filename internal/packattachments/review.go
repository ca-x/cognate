package packattachments

import (
	"context"
	"slices"
	"time"
)

type Identity struct{ Origin, OwnerID string }

type Review struct {
	Identity
	Before       Snapshot
	References   []Reference
	Confirmation string
	Expires      time.Time
}

func (client *Client) Identity(caller context.Context) (Identity, error) {
	ctx, state, release, err := client.start(caller)
	if err != nil {
		return Identity{}, err
	}
	defer release()
	if err := authenticate(ctx, state); err != nil {
		return Identity{}, err
	}
	state.mutex.Lock()
	defer state.mutex.Unlock()
	return Identity{Origin: state.origin, OwnerID: state.owner}, nil
}

func (operation *Operation) Review() (Review, error) {
	if operation == nil || operation.state == nil {
		return Review{}, ErrClosed
	}
	state := operation.state()
	state.mutex.Lock()
	defer state.mutex.Unlock()
	if state.closed || state.used || !time.Now().Before(state.expires) {
		return Review{}, ErrClosed
	}
	client := state.client.state()
	client.mutex.Lock()
	defer client.mutex.Unlock()
	if client.closed || client.blocked {
		return Review{}, ErrClosed
	}
	before := state.before
	before.Entries = slices.Clone(before.Entries)
	return Review{Identity: Identity{Origin: client.origin, OwnerID: state.owner}, Before: before, References: slices.Clone(state.refs), Confirmation: state.confirmation, Expires: state.expires}, nil
}
