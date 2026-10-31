package restore

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

var candidateRetained struct {
	sync.Mutex
	files  []*os.File
	scopes []*candidateFiles
}

func candidatePrivate(info os.FileInfo, directory bool) bool {
	if info == nil {
		return false
	}
	attributes, ok := info.Sys().(*syscall.Stat_t)
	if !ok || attributes.Uid != uint32(os.Geteuid()) || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
		return false
	}
	if directory {
		return info.IsDir() && info.Mode().Perm() == 0700
	}
	return info.Mode().IsRegular() && info.Mode().Perm() == 0600 && attributes.Nlink == 1
}

func (files *candidateFiles) initialize(parent *os.File) error {
	operations := files.ops
	if parent == nil || operations.sync == nil || operations.close == nil || operations.unlink == nil || operations.finalRemove == nil {
		return ErrCandidateInvalid
	}
	name := parent.Name()
	canonical, err := filepath.EvalSymlinks(name)
	info, statError := parent.Stat()
	named, nameError := os.Lstat(name)
	if !filepath.IsAbs(name) || filepath.Clean(name) != name || err != nil || canonical != name || statError != nil || nameError != nil || !candidatePrivate(info, true) || !os.SameFile(info, named) {
		return ErrCandidateInvalid
	}
	descriptor, err := unix.Openat(int(parent.Fd()), ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return ErrCandidateInvalid
	}
	files.parent = os.NewFile(uintptr(descriptor), name)
	files.parentInfo, files.parentName, files.entries = info, name, make(map[string]os.FileInfo)
	fenceDescriptor, err := unix.Openat(descriptor, candidateFence, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return ErrCandidateBusy
	}
	files.fenceMade = true
	fence := os.NewFile(uintptr(fenceDescriptor), filepath.Join(name, candidateFence))
	err = func() (returnedError error) {
		defer func() { returnedError = candidateMerge(returnedError, files.closeFile(fence)) }()
		count, writeError := io.WriteString(fence, candidateFenceText)
		syncError := files.ops.sync(fence)
		var statError error
		files.fenceInfo, statError = fence.Stat()
		if writeError != nil || count != len(candidateFenceText) || syncError != nil || statError != nil {
			return ErrCandidateCleanup
		}
		return nil
	}()
	if err != nil {
		files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		return files.failure
	}
	if files.ops.sync(files.parent) != nil {
		files.failure = ErrCandidateCleanup
		return files.failure
	}
	if unix.Mkdirat(descriptor, "candidate", 0700) != nil {
		return ErrCandidateBusy
	}
	files.rootMade = true
	files.root, err = candidateOpenDirectory(files.parent, "candidate")
	if err != nil {
		return err
	}
	files.rootInfo, err = files.root.Stat()
	if err != nil {
		return ErrCandidateCleanup
	}
	if unix.Mkdirat(int(files.root.Fd()), "archives", 0700) != nil {
		return ErrCandidateCleanup
	}
	files.archivesMade = true
	files.archives, err = candidateOpenDirectory(files.root, "archives")
	if err != nil {
		return err
	}
	files.archiveInfo, err = files.archives.Stat()
	if err != nil {
		return ErrCandidateCleanup
	}
	return files.check()
}

func candidateOpenDirectory(parent *os.File, name string) (*os.File, error) {
	descriptor, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrCandidateInvalid
	}
	return os.NewFile(uintptr(descriptor), filepath.Join(parent.Name(), name)), nil
}

func (files *candidateFiles) closeFile(file *os.File) error {
	if file == nil {
		return nil
	}
	if files.ops.close(file) == nil {
		return nil
	}
	files.failure = ErrCandidateUncertain
	candidateRetained.Lock()
	candidateRetained.files = append(candidateRetained.files, file)
	candidateRetained.Unlock()
	return ErrCandidateUncertain
}

func candidateDirectoryPinned(file *os.File, expected os.FileInfo) bool {
	if file == nil || expected == nil {
		return false
	}
	info, err := file.Stat()
	named, nameError := os.Lstat(file.Name())
	return err == nil && nameError == nil && candidatePrivate(info, true) && candidatePrivate(named, true) && os.SameFile(info, expected) && os.SameFile(info, named)
}

func (files *candidateFiles) check() error {
	if files.failure != nil {
		return files.failure
	}
	if !candidateDirectoryPinned(files.parent, files.parentInfo) || !candidateDirectoryPinned(files.root, files.rootInfo) || !candidateDirectoryPinned(files.archives, files.archiveInfo) {
		return ErrCandidateInvalid
	}
	return nil
}

func (files *candidateFiles) locate(name string) (*os.File, string, error) {
	directory, base := files.root, name
	if strings.HasPrefix(name, "archives/") {
		directory, base = files.archives, strings.TrimPrefix(name, "archives/")
	}
	if directory == nil || base == "" || base == "." || base == ".." || strings.ContainsAny(base, "/\\\x00") {
		return nil, "", ErrCandidateInvalid
	}
	return directory, base, nil
}

func (files *candidateFiles) write(ctx context.Context, name string, size int64, produce func(io.Writer) error) (returnedError error) {
	if err := files.check(); err != nil {
		return err
	}
	directory, base, err := files.locate(name)
	if err != nil || size <= 0 || produce == nil {
		return ErrCandidateInvalid
	}
	descriptor, err := unix.Openat(int(directory.Fd()), base, unix.O_RDWR|unix.O_CREAT|unix.O_EXCL|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0600)
	if err != nil {
		return ErrCandidateInvalid
	}
	file := os.NewFile(uintptr(descriptor), filepath.Join(directory.Name(), base))
	defer func() { returnedError = candidateMerge(returnedError, files.closeFile(file)) }()
	files.entries[name], err = file.Stat()
	if err != nil {
		files.failure = ErrCandidateCleanup
		return files.failure
	}
	writer := &candidateBoundWriter{ctx: ctx, file: file, remaining: size}
	if err := produce(writer); err != nil {
		return candidateError(err)
	}
	if writer.remaining != 0 {
		return ErrCandidateInvalid
	}
	if files.ops.sync(file) != nil {
		return ErrCandidateCleanup
	}
	info, err := file.Stat()
	if err != nil || !candidatePrivate(info, false) || !os.SameFile(files.entries[name], info) || info.Size() != size {
		return ErrCandidateInvalid
	}
	files.entries[name] = info
	return nil
}

type candidateBoundWriter struct {
	ctx       context.Context
	file      *os.File
	remaining int64
}

func (writer *candidateBoundWriter) Write(payload []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, candidateError(err)
	}
	if int64(len(payload)) > writer.remaining {
		return 0, ErrCandidateBudget
	}
	count, err := writer.file.Write(payload)
	writer.remaining -= int64(count)
	if err != nil {
		return count, ErrCandidateInvalid
	}
	if count != len(payload) {
		return count, io.ErrShortWrite
	}
	return count, candidateError(writer.ctx.Err())
}

func (files *candidateFiles) regularPinned(file *os.File, expected os.FileInfo) bool {
	if file == nil || expected == nil {
		return false
	}
	info, err := file.Stat()
	named, nameError := os.Lstat(file.Name())
	return err == nil && nameError == nil && candidatePrivate(info, false) && candidatePrivate(named, false) && os.SameFile(info, expected) && os.SameFile(info, named) && info.Size() == expected.Size() && info.Mode() == expected.Mode() && info.ModTime().Equal(expected.ModTime())
}

func (files *candidateFiles) withFile(name string, consume func(*os.File) error) (returnedError error) {
	if err := files.check(); err != nil {
		return err
	}
	directory, base, err := files.locate(name)
	if err != nil || files.entries[name] == nil {
		return ErrCandidateInvalid
	}
	descriptor, err := unix.Openat(int(directory.Fd()), base, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return ErrCandidateInvalid
	}
	file := os.NewFile(uintptr(descriptor), filepath.Join(directory.Name(), base))
	defer func() { returnedError = candidateMerge(returnedError, files.closeFile(file)) }()
	if !files.regularPinned(file, files.entries[name]) {
		return ErrCandidateInvalid
	}
	if err := consume(file); err != nil {
		return candidateError(err)
	}
	if !files.regularPinned(file, files.entries[name]) {
		return ErrCandidateInvalid
	}
	return nil
}

func (files *candidateFiles) read(ctx context.Context, name string, size, maximum int64) (payload []byte, returnedError error) {
	if size <= 0 || size > maximum {
		return nil, ErrCandidateBudget
	}
	returnedError = files.withFile(name, func(file *os.File) error {
		var err error
		payload, err = io.ReadAll(io.LimitReader(inventoryReader{ctx: ctx, source: io.NewSectionReader(file, 0, size+1)}, size+1))
		if err != nil {
			return candidateError(err)
		}
		if int64(len(payload)) != size {
			return ErrCandidateInvalid
		}
		return nil
	})
	if returnedError != nil {
		clear(payload)
		payload = nil
	}
	return
}

func (files *candidateFiles) syncDirectories() error {
	if err := files.check(); err != nil {
		return err
	}
	for _, directory := range []*os.File{files.archives, files.root, files.parent} {
		if files.ops.sync(directory) != nil {
			return ErrCandidateCleanup
		}
	}
	return nil
}

func (files *candidateFiles) checkEntries() (returnedError error) {
	if err := files.check(); err != nil {
		return err
	}
	for _, archived := range []bool{false, true} {
		directory := files.root
		expected := map[string]bool{"server.db": true, "inventory.jsonl": true, "archives": true}
		if archived {
			directory = files.archives
			expected = make(map[string]bool)
			for name := range files.entries {
				if strings.HasPrefix(name, "archives/") {
					expected[strings.TrimPrefix(name, "archives/")] = true
				}
			}
		}
		reader, err := candidateOpenDirectory(directory, ".")
		if err != nil {
			return err
		}
		err = func() error {
			var seen int
			for {
				entries, readError := reader.ReadDir(128)
				for _, entry := range entries {
					if !expected[entry.Name()] || seen >= len(expected) {
						return ErrCandidateInvalid
					}
					if archived || entry.Name() != "archives" {
						name := entry.Name()
						if archived {
							name = "archives/" + name
						}
						info, err := entry.Info()
						before := files.entries[name]
						if err != nil || before == nil || !candidatePrivate(info, false) || !os.SameFile(info, before) || info.Size() != before.Size() || info.Mode() != before.Mode() || !info.ModTime().Equal(before.ModTime()) {
							return ErrCandidateInvalid
						}
					}
					seen++
				}
				if errors.Is(readError, io.EOF) {
					if seen != len(expected) {
						return ErrCandidateInvalid
					}
					return nil
				}
				if readError != nil {
					return ErrCandidateInvalid
				}
			}
		}()
		err = candidateMerge(err, files.closeFile(reader))
		if err != nil {
			return err
		}
	}
	return nil
}

func candidateUnlink(directory *os.File, name string, folder bool) error {
	flags := 0
	if folder {
		flags = unix.AT_REMOVEDIR
	}
	if unix.Unlinkat(int(directory.Fd()), name, flags) != nil {
		return ErrCandidateCleanup
	}
	return nil
}

func (files *candidateFiles) cleanup() error {
	if files == nil {
		return nil
	}
	if files.failure == nil && files.rootMade {
		if !candidateDirectoryPinned(files.parent, files.parentInfo) || !candidateDirectoryPinned(files.root, files.rootInfo) {
			files.failure = ErrCandidateCleanup
		}
	}
	if files.failure == nil && files.archivesMade && !candidateDirectoryPinned(files.archives, files.archiveInfo) {
		files.failure = ErrCandidateCleanup
	}
	if files.failure == nil {
		for name, expected := range files.entries {
			directory, base, err := files.locate(name)
			if err != nil {
				files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
				continue
			}
			named, err := os.Lstat(filepath.Join(directory.Name(), base))
			if err != nil || expected == nil || !candidatePrivate(named, false) || !os.SameFile(named, expected) {
				files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
				continue
			}
			files.failure = candidateMerge(files.failure, files.ops.unlink(directory, base, false))
		}
	}
	if files.archives != nil {
		if files.failure == nil && files.ops.sync(files.archives) != nil {
			files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		}
		pinned := candidateDirectoryPinned(files.archives, files.archiveInfo)
		file := files.archives
		files.archives = nil
		_ = files.closeFile(file)
		if files.failure == nil && pinned {
			files.failure = candidateMerge(files.failure, files.ops.unlink(files.root, "archives", true))
		} else if !pinned {
			files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		}
	}
	if files.root != nil {
		if files.failure == nil && files.ops.sync(files.root) != nil {
			files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		}
		pinned := candidateDirectoryPinned(files.root, files.rootInfo)
		file := files.root
		files.root = nil
		_ = files.closeFile(file)
		if files.failure == nil && pinned {
			files.failure = candidateMerge(files.failure, files.ops.unlink(files.parent, "candidate", true))
		} else if !pinned {
			files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		}
	}
	if files.parent != nil {
		if files.failure == nil && files.ops.sync(files.parent) != nil {
			files.failure = candidateMerge(files.failure, ErrCandidateCleanup)
		}
		file := files.parent
		files.parent = nil
		_ = files.closeFile(file)
	}
	if files.failure == nil && files.fenceMade {
		parent, err := os.Lstat(files.parentName)
		fence, nameError := os.Lstat(filepath.Join(files.parentName, candidateFence))
		if err != nil || nameError != nil || !candidatePrivate(parent, true) || !candidatePrivate(fence, false) || !os.SameFile(parent, files.parentInfo) || files.fenceInfo == nil || !os.SameFile(fence, files.fenceInfo) || fence.Size() != files.fenceInfo.Size() || !fence.ModTime().Equal(files.fenceInfo.ModTime()) {
			files.failure = ErrCandidateCleanup
		} else {
			files.failure = candidateError(files.ops.finalRemove(filepath.Join(files.parentName, candidateFence)))
			if files.failure != nil {
				files.failure = ErrCandidateCleanup
			}
		}
	}
	if files.failure != nil {
		candidateRetained.Lock()
		candidateRetained.scopes = append(candidateRetained.scopes, files)
		candidateRetained.Unlock()
	}
	return files.failure
}
