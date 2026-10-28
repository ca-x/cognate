package objectid

import (
	"fmt"
	"strconv"
	"strings"
)

func Validate(identity string) error {
	parts := strings.Split(identity, ":")
	if len(parts) != 3 || parts[0] != "linux-dev-ino-v1" {
		return fmt.Errorf("unsupported object identity")
	}
	for index, part := range parts[1:] {
		value, err := strconv.ParseUint(part, 10, 64)
		if err != nil || strconv.FormatUint(value, 10) != part || index == 1 && value == 0 {
			return fmt.Errorf("invalid object identity")
		}
	}
	return nil
}
