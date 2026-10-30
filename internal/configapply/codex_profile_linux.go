//go:build linux

package configapply

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/ca-x/cognate/internal/codexconfig"
	"golang.org/x/sys/unix"
)

func (state *codexEngineState) captureCodexLaunch(ctx context.Context, launch CodexLaunch) (codexLaunchDescriptor, [3]nativeImage, nativeIdentity, time.Time, error) {
	var descriptor codexLaunchDescriptor
	var baseline [3]nativeImage
	var targetID nativeIdentity
	if err := state.checkCodexContext(ctx); err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	if len(launch.Executable) > 4096 || len(launch.Directory) > 4096 || !filepath.IsAbs(launch.Executable) || filepath.Clean(launch.Executable) != launch.Executable || !filepath.IsAbs(launch.Directory) || filepath.Clean(launch.Directory) != launch.Directory || !slices.Equal(launch.Arguments, []string{"app-server"}) {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexUnsupported
	}
	home := codexDirectoryPath(state.home)
	userConfig := filepath.Join(home, ".codex", "config.toml")
	environment := []string{"HOME=" + home, "CODEX_HOME=" + filepath.Join(home, ".codex"), "PATH=/usr/bin:/bin", "LANG=C.UTF-8"}
	if len(launch.Environment) != 0 && !slices.Equal(launch.Environment, environment) {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexUnsupported
	}
	launch.Arguments = slices.Clone(launch.Arguments)
	launch.Environment = environment
	target, images, bodies, err := state.observeCodex()
	defer codexClearBodies(&bodies)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	defer target.close()
	var local [3]codexconfig.LocalImage
	for index := range images {
		presence := codexconfig.Missing
		if images[index].Present {
			presence = codexconfig.Present
		}
		local[index], err = codexconfig.NewImage(presence, bodies[index])
		if err != nil {
			for _, image := range local {
				image.Destroy()
			}
			return descriptor, baseline, targetID, time.Time{}, ErrCodexCapacity
		}
	}
	defer func() {
		for _, image := range local {
			image.Destroy()
		}
	}()
	facts, expires, err := codexconfig.InspectNative0166(ctx, local[0], local[1], state.now())
	facts.Destroy()
	if err != nil {
		if ctx.Err() != nil {
			return descriptor, baseline, targetID, time.Time{}, ctx.Err()
		}
		return descriptor, baseline, targetID, time.Time{}, ErrCodexUnsupported
	}
	workspace, err := codexOpenDirectory(launch.Directory)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	defer workspace.close()
	sources, err := codexLaunchSources(home, launch.Directory, userConfig)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	file, executable, err := codexOpenExecutable(ctx, launch.Executable)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	defer file.Close()
	if err := state.checkpointContext(ctx, "observation-config-started"); err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	report, err := codexNativeConfig(ctx, file, launch)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	defer clear(report)
	layers, err := codexconfig.Native0166LayerWitness(ctx, report, local[0], userConfig)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexUnsupported
	}
	againSources, err := codexLaunchSources(home, launch.Directory, userConfig)
	if err != nil || !slices.Equal(sources, againSources) || workspace.check() != nil {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexConflict
	}
	againFile, againExecutable, err := codexOpenExecutable(ctx, launch.Executable)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	if againFile.Close() != nil || executable != againExecutable {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexConflict
	}
	againTarget, againImages, againBodies, err := state.observeCodex()
	defer codexClearBodies(&againBodies)
	if err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	defer againTarget.close()
	if !sameDirectory(target.identity, againTarget.identity) || images != againImages {
		return descriptor, baseline, targetID, time.Time{}, ErrCodexConflict
	}
	if err := state.checkCodexContext(ctx); err != nil {
		return descriptor, baseline, targetID, time.Time{}, err
	}
	return codexLaunchDescriptor{Version: 1, Launch: launch, Executable: executable, Directory: codexRootID(workspace.identity), Sources: sources, Layers: layers, Auth: images[1]}, images, target.identity, expires, nil
}

func codexDirectoryPath(directory *nativeDirectory) string {
	parts := []string{"/"}
	for _, pin := range directory.pins {
		if pin.name != "" {
			parts = append(parts, pin.name)
		}
	}
	return filepath.Join(parts...)
}

func codexOpenDirectory(path string) (_ *nativeDirectory, result error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(strings.Split(path, "/")) > 64 {
		return nil, ErrCodexUnsupported
	}
	descriptor, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrCodexAttention
	}
	file := os.NewFile(uintptr(descriptor), "Codex source directory")
	identity, err := nativeTrustDirectory(file, false, false)
	if err != nil {
		_ = file.Close()
		return nil, ErrCodexUnsupported
	}
	directory := &nativeDirectory{file: file, identity: identity, owned: []*os.File{file}, pins: []nativePin{{file: file, identity: identity}}}
	defer func() {
		if result != nil {
			_ = directory.close()
		}
	}()
	if path != "/" {
		for name := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
			if !nativeName(name) {
				return nil, ErrCodexUnsupported
			}
			parent := directory.file
			childFD, err := unix.Openat(int(parent.Fd()), name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
			if err != nil {
				return nil, ErrCodexUnsupported
			}
			child := os.NewFile(uintptr(childFD), "Codex source directory")
			directory.owned = append(directory.owned, child)
			identity, err := nativeTrustDirectory(child, false, false)
			if err != nil {
				return nil, ErrCodexUnsupported
			}
			directory.pins = append(directory.pins, nativePin{parent: parent, file: child, name: name, identity: identity})
			directory.file, directory.identity = child, identity
		}
	}
	if directory.check() != nil {
		return nil, ErrCodexConflict
	}
	return directory, nil
}

func codexAbsentSource(path string) (codexSourceStamp, error) {
	parts := strings.Split(strings.TrimPrefix(path, "/"), "/")
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || len(parts) > 64 {
		return codexSourceStamp{}, ErrCodexUnsupported
	}
	directory, err := codexOpenDirectory("/")
	if err != nil {
		return codexSourceStamp{}, err
	}
	defer directory.close()
	for index, name := range parts {
		if !nativeName(name) {
			return codexSourceStamp{}, ErrCodexUnsupported
		}
		descriptor, err := unix.Openat(int(directory.file.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
		if errors.Is(err, unix.ENOENT) {
			if directory.check() != nil {
				return codexSourceStamp{}, ErrCodexConflict
			}
			return codexSourceStamp{Path: path, Parent: codexRootID(directory.identity), MissingAt: index}, nil
		}
		if err != nil {
			return codexSourceStamp{}, ErrCodexUnsupported
		}
		child := os.NewFile(uintptr(descriptor), "Codex absent source")
		directory.owned = append(directory.owned, child)
		if index == len(parts)-1 {
			return codexSourceStamp{}, ErrCodexUnsupported
		}
		identity, err := nativeTrustDirectory(child, false, false)
		if err != nil {
			return codexSourceStamp{}, ErrCodexUnsupported
		}
		directory.pins = append(directory.pins, nativePin{parent: directory.file, file: child, name: name, identity: identity})
		directory.file, directory.identity = child, identity
	}
	return codexSourceStamp{}, ErrCodexUnsupported
}

func codexLaunchSources(home, workspace, userConfig string) ([]codexSourceStamp, error) {
	paths := []string{"/etc/codex/config.toml", "/etc/codex/managed_config.toml", "/etc/codex/requirements.toml", filepath.Join(home, ".codex", "managed_config.toml"), filepath.Join(workspace, "config.toml")}
	for parent := workspace; ; parent = filepath.Dir(parent) {
		path := filepath.Join(parent, ".codex", "config.toml")
		if path != userConfig {
			paths = append(paths, path)
		}
		if parent == "/" {
			break
		}
		if len(paths) > 64 {
			return nil, ErrCodexCapacity
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	stamps := make([]codexSourceStamp, 0, len(paths))
	for _, path := range paths {
		stamp, err := codexAbsentSource(path)
		if err != nil {
			return nil, err
		}
		stamps = append(stamps, stamp)
	}
	return stamps, nil
}

func codexOpenExecutable(ctx context.Context, path string) (_ *os.File, image nativeImage, result error) {
	parent, err := codexOpenDirectory(filepath.Dir(path))
	if err != nil {
		return nil, image, err
	}
	defer parent.close()
	name := filepath.Base(path)
	if !nativeName(name) {
		return nil, image, ErrCodexUnsupported
	}
	descriptor, err := unix.Openat(int(parent.file.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, image, ErrCodexUnsupported
	}
	file := os.NewFile(uintptr(descriptor), "pinned Codex executable")
	defer func() {
		if result != nil {
			_ = file.Close()
		}
	}()
	var before unix.Stat_t
	if unix.Fstat(descriptor, &before) != nil || before.Mode&unix.S_IFMT != unix.S_IFREG || before.Nlink != 1 || before.Uid != uint32(os.Geteuid()) && before.Uid != 0 || before.Mode&0o022 != 0 || before.Mode&0o111 == 0 || before.Size < 4 || before.Size > 384<<20 || nativeACL(file) != nil {
		return nil, image, ErrCodexUnsupported
	}
	hash := sha256.New()
	buffer := make([]byte, 256<<10)
	defer clear(buffer)
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return nil, image, err
		}
		count, err := file.Read(buffer)
		if count > 0 {
			total += int64(count)
			if total > 384<<20 {
				return nil, image, ErrCodexCapacity
			}
			_, _ = hash.Write(buffer[:count])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, image, ErrCodexAttention
		}
	}
	var after, entry unix.Stat_t
	if unix.Fstat(descriptor, &after) != nil || statIdentity(&before) != statIdentity(&after) || before.Ctim != after.Ctim || before.Mtim != after.Mtim || total != after.Size || unix.Fstatat(int(parent.file.Fd()), name, &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || statIdentity(&entry) != statIdentity(&after) || parent.check() != nil {
		return nil, image, ErrCodexConflict
	}
	digest := hash.Sum(nil)
	if hex.EncodeToString(digest) != codexconfig.Native0166ExecutableSHA256 {
		return nil, image, ErrCodexUnsupported
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return nil, image, ErrCodexAttention
	}
	copy(image.SHA256[:], digest)
	image.Present, image.Identity = true, statIdentity(&after)
	return file, image, nil
}

func codexNativeConfig(parent context.Context, executable *os.File, launch CodexLaunch) ([]byte, error) {
	ctx, cancel := context.WithTimeout(parent, 5*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "/proc/self/fd/3", "app-server")
	command.ExtraFiles = []*os.File{executable}
	command.Dir = launch.Directory
	command.Env = slices.Clone(launch.Environment)
	command.Stderr = io.Discard
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	command.WaitDelay = time.Second
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	input, err := command.StdinPipe()
	if err != nil {
		return nil, ErrCodexAttention
	}
	defer input.Close()
	output, err := command.StdoutPipe()
	if err != nil {
		return nil, ErrCodexAttention
	}
	defer output.Close()
	if command.Start() != nil {
		return nil, ErrCodexUnsupported
	}
	defer func() { _ = syscall.Kill(-command.Process.Pid, syscall.SIGKILL); _ = command.Wait() }()
	scanner := bufio.NewScanner(io.LimitReader(output, (2<<20)+1))
	scanner.Buffer(make([]byte, 4096), 1<<20)
	requests := []any{
		map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]any{"name": "cognate-profile-observer", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}}},
		map[string]any{"id": 2, "method": "config/read", "params": map[string]any{"cwd": launch.Directory, "includeLayers": true}},
	}
	send := func(value any) error {
		raw, err := json.Marshal(value)
		if err != nil {
			return ErrCodexInvalid
		}
		defer clear(raw)
		raw = append(raw, '\n')
		written, err := input.Write(raw)
		if err != nil || written != len(raw) {
			return ErrCodexAttention
		}
		return nil
	}
	var total int
	for index, request := range requests {
		if send(request) != nil {
			return nil, ErrCodexUnsupported
		}
		found := false
		for range 64 {
			if !scanner.Scan() {
				break
			}
			line := scanner.Bytes()
			total += len(line) + 1
			if total > 2<<20 {
				return nil, ErrCodexCapacity
			}
			var response struct {
				ID     *int           `json:"id"`
				Error  jsontext.Value `json:"error"`
				Result jsontext.Value `json:"result"`
			}
			if json.Unmarshal(line, &response) != nil {
				return nil, ErrCodexUnsupported
			}
			if response.ID == nil {
				continue
			}
			if *response.ID != index+1 || len(response.Error) != 0 || len(response.Result) == 0 {
				return nil, ErrCodexUnsupported
			}
			found = true
			if index == 1 {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				return slices.Clone(response.Result), nil
			}
			if send(map[string]any{"method": "initialized"}) != nil {
				return nil, ErrCodexUnsupported
			}
			break
		}
		if !found {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return nil, ErrCodexUnsupported
		}
	}
	return nil, ErrCodexUnsupported
}
