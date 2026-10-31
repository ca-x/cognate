package state

import (
	"os"
	"sync"
)

type localSnapshotSource struct {
	parent, database         *os.File
	parentInfo, databaseInfo os.FileInfo
	name                     string
	closeOnce                sync.Once
	closeError               error
}

type localSnapshotStage struct {
	pins   map[string]os.FileInfo
	root   *os.File
	name   string
	parent *os.File
}

var snapshotRetained struct {
	sync.Mutex
	resources []any
}

func retainSnapshotResource(resource any) {
	snapshotRetained.Lock()
	snapshotRetained.resources = append(snapshotRetained.resources, resource)
	snapshotRetained.Unlock()
}

func (source *localSnapshotSource) close() error {
	if source == nil {
		return nil
	}
	source.closeOnce.Do(func() {
		if source.database.Close() != nil {
			source.closeError = ErrLocalSnapshotUncertain
		}
		if source.parent.Close() != nil {
			source.closeError = ErrLocalSnapshotUncertain
		}
		if source.closeError != nil {
			retainSnapshotResource(source)
		}
	})
	return source.closeError
}
