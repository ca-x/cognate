package stagename

import (
	"fmt"
	"path/filepath"
	"strings"
)

const Prefix = ".skillhub-stage-"

func Validate(name string) error {
	if !strings.HasPrefix(name, Prefix) || name == Prefix || strings.ContainsAny(name, "/\\\x00") || !filepath.IsLocal(name) {
		return fmt.Errorf("stage name must be a local Cognate stage component")
	}
	return nil
}
