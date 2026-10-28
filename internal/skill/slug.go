package skill

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

func PortableSlug(name string) string {
	var slug strings.Builder
	for _, letter := range strings.ToLower(strings.TrimSpace(name)) {
		if letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' {
			slug.WriteRune(letter)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
	}
	value := strings.Trim(slug.String(), "-")
	digest := sha256.Sum256([]byte(name))
	if value == "" {
		value = "skill-" + hex.EncodeToString(digest[:6])
	}
	if len(value) > 80 {
		value = strings.TrimRight(value[:67], "-") + "-" + hex.EncodeToString(digest[:6])
	}
	if reservedSlug(value) {
		value = "skill-" + value
	}
	return value
}

func ValidatePortableSlug(value string) error {
	if value == "" || len(value) > 80 || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") || reservedSlug(value) {
		return errors.New("skill alias must be a portable lowercase slug")
	}
	for _, letter := range value {
		if !(letter >= 'a' && letter <= 'z' || letter >= '0' && letter <= '9' || letter == '-' || letter == '_') {
			return errors.New("skill alias must contain lowercase letters, numbers, hyphens or underscores")
		}
	}
	return nil
}

func reservedSlug(value string) bool {
	if value == "con" || value == "prn" || value == "aux" || value == "nul" {
		return true
	}
	return len(value) == 4 && (strings.HasPrefix(value, "com") || strings.HasPrefix(value, "lpt")) && value[3] >= '1' && value[3] <= '9'
}
