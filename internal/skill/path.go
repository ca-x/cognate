package skill

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func ValidateSlug(slug string) error {
	if slug == "" || slug == "." || slug == ".." || strings.ContainsAny(slug, "/\\\x00") || !filepath.IsLocal(slug) {
		return fmt.Errorf("skill slug must be a local path component")
	}
	return nil
}

func ResolvePath(path string) (string, error) {
	if !filepath.IsAbs(path) {
		if filepath.VolumeName(path) != "" {
			return "", fmt.Errorf("drive-relative paths are unsupported")
		}
		workingDirectory, err := os.Getwd()
		if err != nil {
			return "", err
		}
		workingDirectory, err = filepath.EvalSymlinks(workingDirectory)
		if err != nil {
			return "", err
		}
		path = workingDirectory + string(filepath.Separator) + path
	}
	return filepath.EvalSymlinks(path)
}
