//go:build linux

package configapply

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/ca-x/cognate/internal/geminiconfig"
	"golang.org/x/sys/unix"
)

type geminiSourceStamp struct {
	Path              string
	Parent            nativeIdentity
	MissingAt         int
	Image             nativeImage
	Modified, Changed [2]int64
}
type geminiDescriptor struct {
	Version                        uint32
	Launch                         GeminiLaunch
	Node                           nativeImage
	PackageRoot, Directory, Target nativeIdentity
	PackageDigest, Environment     [32]byte
	Sources                        []geminiSourceStamp
}
type geminiObserved struct {
	engine     *geminiEngineState
	descriptor geminiDescriptor
	baseline   [2]nativeImage
	expires    time.Time
}

func (value *geminiObserved) destroy()                      { *value = geminiObserved{} }
func geminiPlatform() error                                 { return codexPlatform() }
func geminiDirectoryPath(directory *nativeDirectory) string { return codexDirectoryPath(directory) }
func geminiPath(value string) bool {
	return len(value) > 0 && len(value) <= 4096 && filepath.IsAbs(value) && filepath.Clean(value) == value && !strings.ContainsAny(value, "\x00\r\n") && len(strings.Split(value, "/")) <= 64
}

func geminiReadSource(ctx context.Context, path string, limit int64) (stamp geminiSourceStamp, body []byte, result error) {
	if !geminiPath(path) {
		return stamp, nil, ErrGeminiUnsupported
	}
	if err := ctx.Err(); err != nil {
		return stamp, nil, err
	}
	absent, err := codexAbsentSource(path)
	if err == nil {
		return geminiSourceStamp{Path: path, Parent: absent.Parent, MissingAt: absent.MissingAt}, nil, nil
	}
	directory, err := codexOpenDirectory(filepath.Dir(path))
	if err != nil {
		return stamp, nil, ErrGeminiUnsupported
	}
	defer directory.close()
	descriptor, err := unix.Openat(int(directory.file.Fd()), filepath.Base(path), unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return stamp, nil, ErrGeminiUnsupported
	}
	file := os.NewFile(uintptr(descriptor), "Gemini bounded source")
	defer file.Close()
	var before unix.Stat_t
	if unix.Fstat(descriptor, &before) != nil {
		return stamp, nil, ErrGeminiAttention
	}
	identity := statIdentity(&before)
	if identity.Mode&unix.S_IFMT != unix.S_IFREG || identity.Owner != 0 && identity.Owner != uint32(os.Geteuid()) || identity.Links != 1 || identity.Mode&0o022 != 0 || identity.Size < 0 || identity.Size > limit || nativeACL(file) != nil {
		return stamp, nil, ErrGeminiUnsupported
	}
	var content bytes.Buffer
	var buffer [32768]byte
	for {
		if err := ctx.Err(); err != nil {
			return stamp, nil, err
		}
		count, err := file.Read(buffer[:])
		if count > 0 {
			if int64(content.Len()+count) > limit {
				return stamp, nil, ErrGeminiCapacity
			}
			_, _ = content.Write(buffer[:count])
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return stamp, nil, ErrGeminiAttention
		}
	}
	body = bytes.Clone(content.Bytes())
	defer func() {
		clear(content.Bytes())
		clear(buffer[:])
		if result != nil {
			clear(body)
			body = nil
		}
	}()
	var after, entry unix.Stat_t
	if unix.Fstat(descriptor, &after) != nil || before.Mtim != after.Mtim || before.Ctim != after.Ctim || statIdentity(&after) != identity || unix.Fstatat(int(directory.file.Fd()), filepath.Base(path), &entry, unix.AT_SYMLINK_NOFOLLOW) != nil || statIdentity(&entry) != identity || directory.check() != nil {
		return stamp, body, ErrGeminiConflict
	}
	return geminiSourceStamp{Path: path, Parent: codexRootID(directory.identity), Image: nativeImage{Present: true, Identity: identity, SHA256: sha256.Sum256(body)}, Modified: [2]int64{before.Mtim.Sec, before.Mtim.Nsec}, Changed: [2]int64{before.Ctim.Sec, before.Ctim.Nsec}}, body, nil
}

func geminiPackage(ctx context.Context, root string) (nativeIdentity, [32]byte, error) {
	manifest := geminiconfig.Native0610PackageManifest()
	defer clear(manifest)
	expectedManifest, _ := hex.DecodeString(geminiconfig.Native0610PackageManifestSHA256)
	if sha256.Sum256(manifest) != [32]byte(expectedManifest) {
		return nativeIdentity{}, [32]byte{}, ErrGeminiUnsupported
	}
	expected := make(map[string][32]byte)
	for line := range strings.SplitSeq(strings.TrimSuffix(string(manifest), "\n"), "\n") {
		digest, path, found := strings.Cut(line, "  ./")
		decoded, err := hex.DecodeString(digest)
		if !found || err != nil || len(decoded) != 32 || path == "" || strings.Contains(path, "\\") || filepath.IsAbs(path) || filepath.Clean(path) != path || strings.HasPrefix(path, "../") {
			return nativeIdentity{}, [32]byte{}, ErrGeminiUnsupported
		}
		if _, exists := expected[path]; exists {
			return nativeIdentity{}, [32]byte{}, ErrGeminiUnsupported
		}
		expected[path] = [32]byte(decoded)
	}
	if len(expected) != 449 {
		return nativeIdentity{}, [32]byte{}, ErrGeminiUnsupported
	}
	directory, err := codexOpenDirectory(root)
	if err != nil {
		return nativeIdentity{}, [32]byte{}, ErrGeminiUnsupported
	}
	defer directory.close()
	var stamps []geminiSourceStamp
	var total int64
	directories := 0
	var walk func(string) error
	walk = func(relative string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		directories++
		if directories > 512 {
			return ErrGeminiCapacity
		}
		current, err := codexOpenDirectory(filepath.Join(root, relative))
		if err != nil {
			return ErrGeminiUnsupported
		}
		defer current.close()
		names, err := current.entries()
		if err != nil || len(names) > 512 {
			return ErrGeminiCapacity
		}
		slices.Sort(names)
		for _, name := range names {
			if !nativeName(name) {
				return ErrGeminiUnsupported
			}
			path := filepath.Join(relative, name)
			var stat unix.Stat_t
			if unix.Fstatat(int(current.file.Fd()), name, &stat, unix.AT_SYMLINK_NOFOLLOW) != nil {
				return ErrGeminiConflict
			}
			if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
				if len(strings.Split(path, "/")) > 64 {
					return ErrGeminiCapacity
				}
				if err := walk(path); err != nil {
					return err
				}
				continue
			}
			digest, exists := expected[path]
			if !exists || len(stamps) >= 512 {
				return ErrGeminiUnsupported
			}
			stamp, body, err := geminiReadSource(ctx, filepath.Join(root, path), 128<<20)
			clear(body)
			if err != nil || !stamp.Image.Present || stamp.Image.SHA256 != digest {
				return ErrGeminiUnsupported
			}
			total += stamp.Image.Identity.Size
			if total > 128<<20 {
				return ErrGeminiCapacity
			}
			stamp.Path = path
			stamps = append(stamps, stamp)
		}
		return current.check()
	}
	if err := walk(""); err != nil {
		return nativeIdentity{}, [32]byte{}, err
	}
	if len(stamps) != len(expected) || directory.check() != nil {
		return nativeIdentity{}, [32]byte{}, ErrGeminiConflict
	}
	encoded, err := json.Marshal(stamps)
	if err != nil {
		return nativeIdentity{}, [32]byte{}, ErrGeminiAttention
	}
	return codexRootID(directory.identity), sha256.Sum256(encoded), nil
}

func geminiEnvironment(home string) ([32]byte, error) {
	if os.Getenv("HOME") != home {
		return [32]byte{}, ErrGeminiUnsupported
	}
	names := []string{"HOME", "PATH", "LANG", "LC_ALL", "TZ", "GEMINI_CLI_NO_RELAUNCH", "NODE_USE_SYSTEM_CA", "TERM", "COLORTERM", "TMPDIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		upper := strings.ToUpper(name)
		allowed := name == "GEMINI_CLI_NO_RELAUNCH" || name == "NODE_USE_SYSTEM_CA"
		if !allowed && (strings.HasPrefix(upper, "GEMINI_") || strings.HasPrefix(upper, "GOOGLE_") || strings.HasPrefix(upper, "GCLOUD_") || strings.HasPrefix(upper, "NODE_") || strings.HasPrefix(upper, "LD_") || strings.HasPrefix(upper, "CODE_ASSIST_") || slices.Contains([]string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "CLOUD_SHELL", "SANDBOX", "ELECTRON_RUN_AS_NODE", "OPENSSL_CONF", "OPENSSL_MODULES", "SSLKEYLOGFILE"}, upper)) {
			return [32]byte{}, ErrGeminiUnsupported
		}
	}
	if value := os.Getenv("GEMINI_CLI_NO_RELAUNCH"); value != "" && value != "1" {
		return [32]byte{}, ErrGeminiUnsupported
	}
	if value := os.Getenv("NODE_USE_SYSTEM_CA"); value != "" && value != "1" {
		return [32]byte{}, ErrGeminiUnsupported
	}
	var values []string
	for _, name := range names {
		value, present := os.LookupEnv(name)
		if present {
			values = append(values, name+"="+value)
		} else {
			values = append(values, name)
		}
	}
	encoded, _ := json.Marshal(values)
	defer clear(encoded)
	return sha256.Sum256(encoded), nil
}

func geminiJSON(body []byte) (map[string]any, error) {
	if len(body) > 256<<10 {
		return nil, ErrGeminiCapacity
	}
	clean := bytes.Clone(body)
	defer clear(clean)
	quoted, escaped := false, false
	depth, tokens := 0, 0
	for index := 0; index < len(clean); index++ {
		character := clean[index]
		if quoted {
			if escaped {
				escaped = false
			} else if character == '\\' {
				escaped = true
			} else if character == '"' {
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			continue
		}
		if character == '/' && index+1 < len(clean) && (clean[index+1] == '/' || clean[index+1] == '*') {
			block := clean[index+1] == '*'
			clean[index], clean[index+1] = ' ', ' '
			index += 2
			closed := !block
			for index < len(clean) {
				if block && index+1 < len(clean) && clean[index] == '*' && clean[index+1] == '/' {
					clean[index], clean[index+1] = ' ', ' '
					index++
					closed = true
					break
				}
				if !block && (clean[index] == '\n' || clean[index] == '\r') {
					index--
					break
				}
				if clean[index] != '\n' && clean[index] != '\r' {
					clean[index] = ' '
				}
				index++
			}
			if !closed {
				return nil, ErrGeminiUnsupported
			}
			continue
		}
		if character == '{' || character == '[' {
			depth++
			if depth > 32 {
				return nil, ErrGeminiCapacity
			}
		}
		if character == '}' || character == ']' {
			depth--
		}
		if character == ',' || character == ':' {
			tokens++
			if tokens > 32768 {
				return nil, ErrGeminiCapacity
			}
		}
	}
	var value map[string]any
	if json.Unmarshal(clean, &value) != nil || value == nil {
		return nil, ErrGeminiUnsupported
	}
	return value, nil
}
func geminiSettingsSafe(body []byte, user bool) error {
	if len(body) == 0 {
		return nil
	}
	value, err := geminiJSON(body)
	if err != nil {
		return err
	}
	denied := []string{"/general/disableAutoUpdate", "/general/disableUpdateNag", "/ui/accessibility/disableLoadingPhrases", "/ui/accessibility/enableLoadingPhrases", "/context/fileFiltering/disableFuzzySearch", "/tools", "/experimental", "/modelConfigs", "/advanced", "/mcpServers", "/hooks"}
	var inspect func(any, string) error
	inspect = func(value any, path string) error {
		if slices.Contains(denied, path) {
			return ErrGeminiUnsupported
		}
		switch typed := value.(type) {
		case string:
			if strings.Contains(typed, "$") {
				return ErrGeminiUnsupported
			}
		case map[string]any:
			for key, child := range typed {
				if err := inspect(child, path+"/"+key); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range typed {
				if err := inspect(child, path); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := inspect(value, ""); err != nil {
		return err
	}
	for name, child := range value {
		switch name {
		case "ui", "foreign":
		case "model":
			if !user {
				return ErrGeminiUnsupported
			}
			model, ok := child.(map[string]any)
			if !ok {
				return ErrGeminiUnsupported
			}
			for key := range model {
				if key != "name" && key != "other" {
					return ErrGeminiUnsupported
				}
			}
		case "security":
			security, ok := child.(map[string]any)
			if !ok {
				return ErrGeminiUnsupported
			}
			for key, entry := range security {
				switch key {
				case "auth":
					if !user {
						return ErrGeminiUnsupported
					}
					auth, ok := entry.(map[string]any)
					if !ok {
						return ErrGeminiUnsupported
					}
					for key := range auth {
						if key != "selectedType" && key != "note" {
							return ErrGeminiUnsupported
						}
					}
				default:
					return ErrGeminiUnsupported
				}
			}
		default:
			return ErrGeminiUnsupported
		}
	}
	return nil
}
func geminiTrust(ctx context.Context, body []byte, workspace string) error {
	rules, err := geminiJSON(body)
	if err != nil || len(rules) > 64 {
		return ErrGeminiUnsupported
	}
	longest := -1
	trusted := false
	for path, raw := range rules {
		level, ok := raw.(string)
		if !ok || !geminiPath(path) || level != "TRUST_FOLDER" && level != "TRUST_PARENT" && level != "DO_NOT_TRUST" {
			return ErrGeminiUnsupported
		}
		effective := path
		if level == "TRUST_PARENT" {
			effective = filepath.Dir(path)
		}
		directory, err := codexOpenDirectory(effective)
		if err != nil {
			return ErrGeminiUnsupported
		}
		if directory.close() != nil {
			return ErrGeminiUnknown
		}
		if workspace == effective || strings.HasPrefix(workspace, effective+"/") || effective == "/" {
			length := len(utf16.Encode([]rune(path)))
			if length == longest {
				return ErrGeminiUnsupported
			}
			if length > longest {
				longest = length
				trusted = level != "DO_NOT_TRUST"
			}
		}
		if err := ctx.Err(); err != nil {
			return err
		}
	}
	if !trusted {
		return ErrGeminiUnsupported
	}
	return nil
}

func (state *geminiEngineState) captureGeminiOnce(ctx context.Context, launch GeminiLaunch) (geminiDescriptor, [2]nativeImage, error) {
	var descriptor geminiDescriptor
	if err := state.check(ctx); err != nil {
		return descriptor, [2]nativeImage{}, err
	}
	for _, path := range []string{launch.NodeExecutable, launch.PackageRoot, launch.Directory} {
		if !geminiPath(path) {
			return descriptor, [2]nativeImage{}, ErrGeminiUnsupported
		}
	}
	home := geminiDirectoryPath(state.home)
	environment, err := geminiEnvironment(home)
	if err != nil {
		return descriptor, [2]nativeImage{}, err
	}
	node, body, err := geminiReadSource(ctx, launch.NodeExecutable, 256<<20)
	clear(body)
	if err != nil || !node.Image.Present || node.Image.Identity.Mode&0o111 == 0 || hex.EncodeToString(node.Image.SHA256[:]) != geminiconfig.Native0610NodeSHA256 {
		return descriptor, [2]nativeImage{}, ErrGeminiUnsupported
	}
	packageRoot, packageDigest, err := geminiPackage(ctx, launch.PackageRoot)
	if err != nil {
		return descriptor, [2]nativeImage{}, err
	}
	workspace, err := codexOpenDirectory(launch.Directory)
	if err != nil {
		return descriptor, [2]nativeImage{}, ErrGeminiUnsupported
	}
	defer workspace.close()
	target, images, bodies, err := state.observePair()
	defer geminiClearBodies(&bodies)
	if err != nil {
		return descriptor, images, err
	}
	defer target.close()
	if err := geminiSettingsSafe(bodies[1], true); err != nil {
		return descriptor, images, err
	}
	envTarget := filepath.Join(home, ".gemini", ".env")
	settingsTarget := filepath.Join(home, ".gemini", "settings.json")
	paths := []string{filepath.Join(home, ".gemini", "trustedFolders.json"), "/etc/gemini-cli/settings.json", "/etc/gemini-cli/system-defaults.json", filepath.Join(launch.Directory, ".gemini", "settings.json"), filepath.Join(home, ".env")}
	var envPaths []string
	for parent := launch.Directory; ; parent = filepath.Dir(parent) {
		envPaths = append(envPaths, filepath.Join(parent, ".gemini", ".env"), filepath.Join(parent, ".env"))
		if parent == "/" {
			break
		}
		if len(envPaths) > 64 {
			return descriptor, images, ErrGeminiCapacity
		}
	}
	envPaths = append(envPaths, envTarget, filepath.Join(home, ".env"))
	paths = append(paths, envPaths...)
	for parent := launch.PackageRoot; ; parent = filepath.Dir(parent) {
		paths = append(paths, filepath.Join(parent, "node_modules"))
		if parent == "/" {
			break
		}
		if len(paths) > 96 {
			return descriptor, images, ErrGeminiCapacity
		}
	}
	for _, root := range []string{filepath.Join(home, ".gemini"), filepath.Join(launch.Directory, ".gemini")} {
		for _, name := range []string{"extensions", "hooks", "commands", "policies"} {
			paths = append(paths, filepath.Join(root, name))
		}
	}
	slices.Sort(paths)
	paths = slices.Compact(paths)
	if len(paths) > 64 {
		return descriptor, images, ErrGeminiCapacity
	}
	total := len(bodies[0]) + len(bodies[1])
	presentEnv := make(map[string]bool)
	var stamps []geminiSourceStamp
	for _, path := range paths {
		if path == envTarget {
			presentEnv[path] = images[0].Present
			continue
		}
		if path == settingsTarget {
			continue
		}
		stamp, body, err := geminiReadSource(ctx, path, 256<<10)
		if err != nil {
			clear(body)
			return descriptor, images, err
		}
		total += len(body)
		if total > 1<<20 {
			clear(body)
			return descriptor, images, ErrGeminiCapacity
		}
		stamps = append(stamps, stamp)
		if filepath.Base(path) == ".env" {
			presentEnv[path] = stamp.Image.Present
		}
		if stamp.Image.Present {
			switch {
			case path == filepath.Join(home, ".gemini", "trustedFolders.json"):
				err = geminiTrust(ctx, body, launch.Directory)
			case filepath.Base(path) == "settings.json" || filepath.Base(path) == "system-defaults.json":
				err = geminiSettingsSafe(body, false)
			case filepath.Base(path) == ".env":
				err = ErrGeminiUnsupported
			default:
				err = ErrGeminiUnsupported
			}
		} else if path == filepath.Join(home, ".gemini", "trustedFolders.json") {
			err = ErrGeminiUnsupported
		}
		clear(body)
		if err != nil {
			return descriptor, images, err
		}
	}
	selected := ""
	for _, path := range envPaths {
		if presentEnv[path] {
			selected = path
			break
		}
	}
	if selected != "" && selected != envTarget {
		return descriptor, images, ErrGeminiUnsupported
	}
	if workspace.check() != nil || target.check() != nil {
		return descriptor, images, ErrGeminiConflict
	}
	descriptor = geminiDescriptor{Version: 1, Launch: launch, Node: node.Image, PackageRoot: packageRoot, Directory: codexRootID(workspace.identity), Target: codexRootID(target.identity), PackageDigest: packageDigest, Environment: environment, Sources: stamps}
	return descriptor, images, state.check(ctx)
}
func (state *geminiEngineState) captureGemini(ctx context.Context, launch GeminiLaunch) (geminiDescriptor, [2]nativeImage, error) {
	before, images, err := state.captureGeminiOnce(ctx, launch)
	if err != nil {
		return before, images, err
	}
	if err := state.checkpoint(ctx, "observation-between"); err != nil {
		return before, images, err
	}
	after, again, err := state.captureGeminiOnce(ctx, launch)
	if err != nil || !reflect.DeepEqual(before, after) || images != again {
		return before, images, ErrGeminiConflict
	}
	return before, images, nil
}
func (engine *GeminiEngine) ObserveGemini(ctx context.Context, launch GeminiLaunch, deadline time.Time) (GeminiObservation, error) {
	state := engine.private()
	var result GeminiObservation
	started := time.Now()
	expires := minTime(started.Add(2*time.Minute), deadline)
	if current, ok := ctx.Deadline(); ok {
		expires = minTime(expires, current)
	}
	err := state.withLock(ctx, func(ctx context.Context) error {
		if !state.now().Before(expires) {
			return ErrGeminiExpired
		}
		descriptor, images, err := state.captureGemini(ctx, launch)
		if err != nil {
			return err
		}
		if !state.now().Before(expires) {
			return ErrGeminiExpired
		}
		result = GeminiObservation{geminiOpaque{new(any(&geminiObserved{engine: state, descriptor: descriptor, baseline: images, expires: expires}))}}
		return nil
	})
	return result, err
}
func geminiRestoreMode(target *nativeDirectory, index int, expected nativeImage, mode uint32, admit func() error) (nativeImage, error) {
	if index != 1 || mode != 0o600 && mode != 0o644 && mode != 0o640 && mode != 0o400 && mode != 0o440 && mode != 0o444 {
		return nativeImage{}, ErrGeminiUnsupported
	}
	name := geminiRoleNames[index]
	actual, body, err := target.read(name, 256<<10)
	clear(body)
	if err != nil || actual != expected || !actual.Present {
		return nativeImage{}, ErrGeminiConflict
	}
	descriptor, err := unix.Openat(int(target.file.Fd()), name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nativeImage{}, ErrGeminiAttention
	}
	file := os.NewFile(uintptr(descriptor), "Gemini owned selector mode")
	defer file.Close()
	identity, err := nativeStat(file)
	if err != nil || identity != expected.Identity || nativeACL(file) != nil {
		return nativeImage{}, ErrGeminiConflict
	}
	if err := admit(); err != nil {
		return nativeImage{}, err
	}
	if unix.Fchmod(descriptor, mode) != nil || file.Sync() != nil || target.sync() != nil {
		return nativeImage{}, ErrGeminiUnknown
	}
	actual, body, err = target.read(name, 256<<10)
	clear(body)
	if err != nil || actual.SHA256 != expected.SHA256 || actual.Identity.Inode != expected.Identity.Inode || actual.Identity.Device != expected.Identity.Device || actual.Identity.Mode&0o777 != mode {
		return nativeImage{}, ErrGeminiUnknown
	}
	return actual, nil
}
