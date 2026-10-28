package objectid

import (
	"fmt"
	"strconv"
	"strings"
)

func Validate(identity string) error {
	parts := strings.Split(identity, ":")
	if len(parts) != 3 {
		return fmt.Errorf("unsupported object identity")
	}
	bitSizes := [2]int{64, 64}
	switch parts[0] {
	case "linux-dev-ino-v1":
	case "darwin-dev-ino-v1":
		bitSizes[0] = 32
	default:
		return fmt.Errorf("unsupported object identity")
	}
	for index, part := range parts[1:] {
		value, err := strconv.ParseUint(part, 10, bitSizes[index])
		if err != nil || strconv.FormatUint(value, 10) != part || index == 1 && value == 0 {
			return fmt.Errorf("invalid object identity")
		}
	}
	return nil
}
