package restore

import (
	"context"
	"os"
	"sync"

	"github.com/ca-x/cognate/internal/server/backup"
)

type CandidateSource func(context.Context, func(context.Context, backup.CryptoVerifiedBundle) error) error

func PrepareCandidate(ctx context.Context, source CandidateSource, parent *os.File, key []byte, limits CandidateLimits) (*Candidate, error) {
	return prepareCandidate(ctx, source, parent, key, limits, defaultCandidateOps())
}

func prepareCandidate(ctx context.Context, source CandidateSource, parent *os.File, key []byte, limits CandidateLimits, operations candidateFileOps) (*Candidate, error) {
	if ctx == nil || source == nil || parent == nil || !limits.valid() {
		return nil, ErrCandidateInvalid
	}
	if len(key) != 32 {
		return nil, ErrCandidateKey
	}
	if err := ctx.Err(); err != nil {
		return nil, candidateError(err)
	}
	ctx, cancel := context.WithTimeout(ctx, limits.OperationTimeout)
	defer cancel()
	var mutex sync.Mutex
	var candidate *Candidate
	var stageError, protocolError error
	var called, active, expired bool
	done := make(chan struct{})
	sourceReturned, prepared := false, false
	defer func() {
		if prepared {
			return
		}
		cancel()
		mutex.Lock()
		expired = true
		running := active
		mutex.Unlock()
		if running {
			<-done
		}
		if candidate != nil {
			if !sourceReturned {
				candidate.value.files.failure = candidateMerge(candidate.value.files.failure, ErrCandidateUncertain)
			}
			_ = candidate.Close()
		}
	}()
	sourceError := source(ctx, func(callbackContext context.Context, bundle backup.CryptoVerifiedBundle) error {
		mutex.Lock()
		if expired {
			mutex.Unlock()
			return ErrCandidateClosed
		}
		if called {
			protocolError = ErrCandidateInvalid
			mutex.Unlock()
			return ErrCandidateInvalid
		}
		called, active = true, true
		mutex.Unlock()
		var materialized *Candidate
		err := ErrCandidateUncertain
		defer func() {
			mutex.Lock()
			candidate, stageError = materialized, err
			active = false
			close(done)
			mutex.Unlock()
		}()
		if callbackContext == nil {
			err = ErrCandidateInvalid
		} else {
			scoped, cancelStage := context.WithCancel(callbackContext)
			defer cancelStage()
			stopStage := context.AfterFunc(ctx, cancelStage)
			defer stopStage()
			if ctx.Err() != nil {
				cancelStage()
			}
			materialized, err = stageCandidate(scoped, bundle, parent, key, limits, operations)
		}
		return err
	})
	sourceReturned = true
	mutex.Lock()
	expired = true
	joined := active
	mutex.Unlock()
	if joined {
		cancel()
		<-done
	}
	mutex.Lock()
	err := candidateMerge(sourceError, candidateMerge(stageError, protocolError))
	if !called || joined || candidate == nil {
		err = candidateMerge(err, ErrCandidateInvalid)
	}
	mutex.Unlock()
	if ctx.Err() != nil {
		err = candidateMerge(err, ctx.Err())
	}
	if err != nil {
		if candidate != nil {
			err = candidateMerge(err, candidate.Close())
		}
		return nil, err
	}
	candidate.value.mutex.Lock()
	candidate.value.ready = true
	candidate.value.mutex.Unlock()
	prepared = true
	return candidate, nil
}
