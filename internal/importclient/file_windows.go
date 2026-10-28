package importclient

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

func openExport(path string) (*os.File, error) {
	absolute, err := filepath.Abs(path)
	if err != nil || strings.HasPrefix(absolute, `\\`) || strings.Contains(strings.TrimPrefix(absolute, filepath.VolumeName(absolute)), ":") {
		return nil, errFile
	}
	volume := filepath.VolumeName(absolute)
	current := volume + `\`
	components := strings.Split(strings.TrimPrefix(absolute, current), `\`)
	var directories []windows.Handle
	defer func() {
		for _, handle := range directories {
			_ = windows.CloseHandle(handle)
		}
	}()
	for index, component := range components {
		current = filepath.Join(current, component)
		name, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return nil, errFile
		}
		handle, err := windows.CreateFile(name, windows.GENERIC_READ, windows.FILE_SHARE_READ, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_OPEN_REPARSE_POINT|windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
		if err != nil {
			return nil, errFile
		}
		var info windows.ByHandleFileInformation
		if err := windows.GetFileInformationByHandle(handle, &info); err != nil || info.FileAttributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			_ = windows.CloseHandle(handle)
			return nil, errFile
		}
		if index == len(components)-1 {
			return os.NewFile(uintptr(handle), "import export"), nil
		}
		if info.FileAttributes&windows.FILE_ATTRIBUTE_DIRECTORY == 0 {
			_ = windows.CloseHandle(handle)
			return nil, errFile
		}
		directories = append(directories, handle)
	}
	return nil, errFile
}
