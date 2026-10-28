package importer

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/skillhub/internal/server/plugin"
	"github.com/ca-x/skillhub/internal/skill"
)

type InstalledSkill struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Directory string `json:"directory"`
}

type SkillPreview struct {
	Found  bool
	Skills []InstalledSkill
}

func PreviewCCSwitchSkills(ctx context.Context, path string) (SkillPreview, error) {
	input := make([]InstalledSkill, 0)
	found, err := readCCSwitch(ctx, path, func(tx *sql.Tx) error {
		var kind string
		if err := tx.QueryRowContext(ctx, `SELECT type FROM sqlite_schema WHERE name = 'skills'`).Scan(&kind); err != nil || kind != "table" {
			return databaseError(ctx)
		}
		rows, err := tx.QueryContext(ctx, `SELECT substr(id,1,257), substr(name,1,257), substr(directory,1,257), length(CAST(id AS BLOB)), length(CAST(name AS BLOB)), length(CAST(directory AS BLOB)) FROM skills ORDER BY id LIMIT 1001`)
		if err != nil {
			return databaseError(ctx)
		}
		defer rows.Close()
		seen := make(map[string]struct{})
		for rows.Next() {
			var item InstalledSkill
			var idBytes, nameBytes, directoryBytes int64
			if err := rows.Scan(&item.ID, &item.Name, &item.Directory, &idBytes, &nameBytes, &directoryBytes); err != nil {
				return databaseError(ctx)
			}
			if len(input) >= maxRepositories || idBytes < 1 || idBytes > 256 || nameBytes < 1 || nameBytes > 256 || directoryBytes < 1 || directoryBytes > 256 || !utf8.ValidString(item.ID) || !utf8.ValidString(item.Name) || !utf8.ValidString(item.Directory) || strings.ContainsFunc(item.ID, unsafeIdentifierRune) || strings.ContainsFunc(item.Directory, unsafeIdentifierRune) || skill.ValidateSlug(item.Directory) != nil {
				return fmt.Errorf("invalid cc-switch skill record")
			}
			if _, exists := seen[item.ID]; exists {
				return fmt.Errorf("duplicate cc-switch skill identifier")
			}
			seen[item.ID] = struct{}{}
			input = append(input, item)
		}
		if err := rows.Err(); err != nil {
			return databaseError(ctx)
		}
		if err := rows.Close(); err != nil {
			return databaseError(ctx)
		}
		return nil
	})
	if err != nil || !found {
		return SkillPreview{}, err
	}
	encoded, err := json.Marshal(input)
	if err != nil {
		return SkillPreview{}, fmt.Errorf("encode cc-switch skills")
	}
	var results []InstalledSkill
	if err := plugin.RunJSON(ctx, ccSwitchModule, "listInstalledSkills", encoded, &results); err != nil {
		return SkillPreview{}, err
	}
	if len(results) != len(input) {
		return SkillPreview{}, fmt.Errorf("invalid cc-switch plugin result")
	}
	for index := range results {
		if results[index] != input[index] {
			return SkillPreview{}, fmt.Errorf("invalid cc-switch plugin result")
		}
	}
	return SkillPreview{Found: true, Skills: results}, nil
}

func unsafeIdentifierRune(character rune) bool {
	return unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character)
}
