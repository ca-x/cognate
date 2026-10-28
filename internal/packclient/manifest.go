package packclient

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"io"
	"io/fs"
	"strings"
	"unicode"
	"unicode/utf8"

	packv1 "github.com/ca-x/skillhub/gen/skillhub/pack/v1"
	"github.com/ca-x/skillhub/internal/skill"
)

func validateManifest(manifest *packv1.GetManifestResponse) error {
	if manifest == nil || manifest.FormatVersion != 1 || manifest.Revision <= 0 || !validID(manifest.PackId) || !validText(manifest.Name, 128, false) || strings.TrimSpace(manifest.Name) == "" || !validText(manifest.Description, 2048, true) || !validHash(manifest.ManifestHash) || len(manifest.Skills) == 0 || len(manifest.Skills) > 256 {
		return errManifest
	}
	digest := sha256.New()
	manifestStrings(digest, "skillhub/pack-manifest/v1", manifest.PackId, manifest.Name, manifest.Description)
	_, _ = digest.Write(binary.BigEndian.AppendUint64(nil, uint64(manifest.Revision)))
	identifiers := make(map[string]struct{}, len(manifest.Skills))
	previousSlug := ""
	for _, entry := range manifest.Skills {
		if entry == nil || !validID(entry.SkillId) || !validID(entry.RepositoryId) || skill.ValidatePortableSlug(entry.Slug) != nil || !validHash(entry.ContentHash) || !validText(entry.Name, 256, false) || !validText(entry.Description, 4096, true) || !validText(entry.RepositoryName, 256, false) || !validText(entry.Path, 1024, false) || !fs.ValidPath(entry.Path) || strings.Contains(entry.Path, "\\") || entry.Slug <= previousSlug {
			return errManifest
		}
		if _, duplicate := identifiers[entry.SkillId]; duplicate {
			return errManifest
		}
		identifiers[entry.SkillId] = struct{}{}
		previousSlug = entry.Slug
		manifestStrings(digest, entry.SkillId, entry.Slug, entry.Name, entry.Description, entry.ContentHash, entry.RepositoryId, entry.RepositoryName, entry.Path)
	}
	if manifest.ManifestHash != hex.EncodeToString(digest.Sum(nil)) {
		return errManifest
	}
	return nil
}

func validID(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'a' && letter <= 'z' || letter >= 'A' && letter <= 'Z' || letter >= '0' && letter <= '9' || letter == '-' || letter == '_') {
			return false
		}
	}
	return true
}

func validHash(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, letter := range value {
		if !(letter >= 'a' && letter <= 'f' || letter >= '0' && letter <= '9') {
			return false
		}
	}
	return true
}

func validText(value string, maximum int, multiline bool) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, func(letter rune) bool {
		return unicode.IsControl(letter) && !(multiline && (letter == '\n' || letter == '\t'))
	})
}

func manifestStrings(destination hash.Hash, values ...string) {
	for _, value := range values {
		_, _ = destination.Write(binary.BigEndian.AppendUint64(nil, uint64(len(value))))
		_, _ = io.WriteString(destination, value)
	}
}
