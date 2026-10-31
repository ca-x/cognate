//go:build linux

package state

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func snapshotPlatform(ctx context.Context) error { return ctx.Err() }

func retainLocalSnapshotSource(path string) (*localSnapshotSource, error) {
	descriptor, err := unix.Open(filepath.Dir(path), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrLocalSnapshotInvalid
	}
	parent := os.NewFile(uintptr(descriptor), filepath.Dir(path))
	mainFD, err := unix.Openat(descriptor, filepath.Base(path), unix.O_PATH|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		if parent.Close() != nil {
			retainSnapshotResource(parent)
			return nil, ErrLocalSnapshotUncertain
		}
		return nil, ErrLocalSnapshotInvalid
	}
	source := &localSnapshotSource{parent: parent, database: os.NewFile(uintptr(mainFD), path), name: filepath.Base(path)}
	source.parentInfo, err = parent.Stat()
	if err == nil {
		source.databaseInfo, err = source.database.Stat()
	}
	if err != nil || source.recheck() != nil {
		if cleanup := source.close(); cleanup != nil {
			return nil, cleanup
		}
		return nil, ErrLocalSnapshotInvalid
	}
	return source, nil
}

func (source *localSnapshotSource) recheck() error {
	if source == nil || source.parent == nil || source.database == nil {
		return ErrLocalSnapshotInvalid
	}
	parentInfo, err := snapshotSourceParent(source.parent)
	if err != nil || !os.SameFile(source.parentInfo, parentInfo) {
		return ErrLocalSnapshotInvalid
	}
	opened, err := source.database.Stat()
	if err != nil || !snapshotSourcePrivateFile(opened) || !os.SameFile(source.databaseInfo, opened) {
		return ErrLocalSnapshotInvalid
	}
	var named unix.Stat_t
	var physical unix.Stat_t
	if unix.Fstatat(int(source.parent.Fd()), source.name, &named, unix.AT_SYMLINK_NOFOLLOW) != nil || unix.Fstat(int(source.database.Fd()), &physical) != nil || named.Dev != physical.Dev || named.Ino != physical.Ino || named.Mode&unix.S_IFMT != unix.S_IFREG || named.Nlink != 1 {
		return ErrLocalSnapshotInvalid
	}
	return nil
}

func (source *localSnapshotSource) inspect(ctx context.Context, limits LocalSnapshotLimits) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := source.recheck(); err != nil {
		return err
	}
	if !snapshotSourceJournalAbsent(source.parent, source.name) {
		return ErrLocalSnapshotInvalid
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		var info unix.Stat_t
		err := unix.Fstatat(int(source.parent.Fd()), source.name+suffix, &info, unix.AT_SYMLINK_NOFOLLOW)
		if err == unix.ENOENT {
			continue
		}
		maximum := limits.MaxWALBytes
		if suffix == "-shm" {
			maximum = limits.MaxSHMBytes
		}
		if err != nil || info.Size < 0 || info.Size > maximum {
			return ErrLocalSnapshotBudget
		}
		if info.Mode&unix.S_IFMT != unix.S_IFREG || info.Mode&(0077|unix.S_ISUID|unix.S_ISGID|unix.S_ISVTX) != 0 || info.Uid != uint32(os.Geteuid()) || info.Nlink != 1 {
			return ErrLocalSnapshotInvalid
		}
	}
	return ctx.Err()
}

func (source *localSnapshotSource) newStage() (*localSnapshotStage, error) {
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return nil, ErrLocalSnapshotInvalid
	}
	name := ".snapshot-" + hex.EncodeToString(nonce[:])
	if unix.Mkdirat(int(source.parent.Fd()), name, 0700) != nil {
		return nil, ErrLocalSnapshotInvalid
	}
	descriptor, err := unix.Openat(int(source.parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		retainSnapshotResource(struct {
			source *localSnapshotSource
			name   string
		}{source, name})
		return nil, ErrLocalSnapshotCleanup
	}
	stage := &localSnapshotStage{root: os.NewFile(uintptr(descriptor), filepath.Join(source.parent.Name(), name)), name: name, parent: source.parent, pins: make(map[string]os.FileInfo, 3)}
	if source.parent.Sync() != nil {
		retainSnapshotResource(stage)
		return nil, ErrLocalSnapshotCleanup
	}
	return stage, nil
}

func (stage *localSnapshotStage) open(name string) (*os.File, error) {
	if name != "snapshot.sqlite" && name != "references.ndjson" && name != "candidate.json" {
		return nil, ErrLocalSnapshotInvalid
	}
	descriptor, err := unix.Openat(int(stage.root.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrLocalSnapshotInvalid
	}
	file := os.NewFile(uintptr(descriptor), filepath.Join(stage.root.Name(), name))
	info, err := file.Stat()
	expected, pinned := stage.pins[name]
	if err != nil || !snapshotSourcePrivateFile(info) || pinned && !snapshotSourceSame(expected, info) {
		if file.Close() != nil {
			retainSnapshotResource(file)
			return nil, ErrLocalSnapshotUncertain
		}
		return nil, ErrLocalSnapshotInvalid
	}
	if !pinned {
		stage.pins[name] = info
	}
	return file, nil
}

func (stage *localSnapshotStage) cleanup() error {
	if stage == nil {
		return nil
	}
	if _, err := stage.root.Seek(0, io.SeekStart); err != nil {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotCleanup
	}
	names, err := stage.root.ReadDir(8)
	if err != nil && !errors.Is(err, io.EOF) {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotCleanup
	}
	if len(names) > 6 {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotCleanup
	}
	for _, entry := range names {
		name := entry.Name()
		if name != "snapshot.sqlite" && name != "references.ndjson" && name != "candidate.json" && name != "snapshot.sqlite-wal" && name != "snapshot.sqlite-shm" && name != "snapshot.sqlite-journal" {
			retainSnapshotResource(stage)
			return ErrLocalSnapshotCleanup
		}
		if unix.Unlinkat(int(stage.root.Fd()), name, 0) != nil {
			retainSnapshotResource(stage)
			return ErrLocalSnapshotCleanup
		}
	}
	if stage.root.Sync() != nil {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotCleanup
	}
	if stage.root.Close() != nil {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotUncertain
	}
	if unix.Unlinkat(int(stage.parent.Fd()), stage.name, unix.AT_REMOVEDIR) != nil || stage.parent.Sync() != nil {
		retainSnapshotResource(stage)
		return ErrLocalSnapshotCleanup
	}
	return nil
}
