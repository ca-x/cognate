package importer

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"path"
	"strings"

	"github.com/ca-x/skillhub/internal/repository"
	"github.com/ca-x/skillhub/internal/server/plugin"
	"github.com/ca-x/skillhub/internal/skill"
)

//go:embed magpie.mjs
var magpieModule string

const (
	MaxMagpieExportBytes = 1 << 20
	MaxMagpieSkills      = 1000
)

type MagpieSource struct {
	Kind string `json:"kind"`
	Repo string `json:"repo,omitempty"`
	Ref  string `json:"ref,omitempty"`
	Path string `json:"path,omitempty"`
}

type MagpieSkill struct {
	Name   string        `json:"name"`
	Source *MagpieSource `json:"source,omitempty"`
}

type MagpieRepository struct {
	Repo string `json:"repo"`
	Ref  string `json:"ref,omitempty"`
}

type MagpiePreview struct {
	Found        bool               `json:"found"`
	Skills       []MagpieSkill      `json:"skills"`
	Repositories []MagpieRepository `json:"repositories"`
}

func PreviewMagpie(ctx context.Context, export []byte) (MagpiePreview, error) {
	if err := ctx.Err(); err != nil {
		return MagpiePreview{}, err
	}
	if export == nil {
		return MagpiePreview{}, nil
	}
	if len(export) == 0 || len(export) > MaxMagpieExportBytes {
		return MagpiePreview{}, fmt.Errorf("invalid magpie library export")
	}
	var library struct {
		Skills jsontext.Value `json:"skills"`
	}
	if err := json.Unmarshal(export, &library); err != nil || len(library.Skills) == 0 {
		return MagpiePreview{}, fmt.Errorf("invalid magpie library export")
	}
	skills, err := decodeMagpieSkills(library.Skills)
	if err != nil {
		return MagpiePreview{}, err
	}
	seen := make(map[string]struct{}, len(skills))
	repositories := make(map[MagpieRepository]struct{})
	for index := range skills {
		item := &skills[index]
		if len(item.Name) > 256 || strings.ContainsFunc(item.Name, unsafeIdentifierRune) || skill.ValidateSlug(item.Name) != nil {
			return MagpiePreview{}, fmt.Errorf("invalid magpie skill record")
		}
		if _, exists := seen[item.Name]; exists {
			return MagpiePreview{}, fmt.Errorf("duplicate magpie skill name")
		}
		seen[item.Name] = struct{}{}
		if err := normalizeMagpieSource(item.Source); err != nil {
			return MagpiePreview{}, err
		}
		if source := item.Source; source != nil && source.Kind == "github" {
			repositories[MagpieRepository{Repo: source.Repo, Ref: source.Ref}] = struct{}{}
		}
	}
	encoded, err := json.Marshal(skills)
	if err != nil {
		return MagpiePreview{}, fmt.Errorf("encode magpie skill references")
	}
	var result MagpiePreview
	if err := plugin.RunJSON(ctx, magpieModule, "previewLibrary", encoded, &result); err != nil {
		return MagpiePreview{}, err
	}
	if !validMagpieResult(result, skills, repositories) {
		return MagpiePreview{}, fmt.Errorf("invalid magpie plugin result")
	}
	result.Found = true
	return result, nil
}

func decodeMagpieSkills(data jsontext.Value) ([]MagpieSkill, error) {
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() != '[' && token.Kind() != 'n' {
		return nil, fmt.Errorf("invalid magpie skill records")
	}
	skills := make([]MagpieSkill, 0)
	if token.Kind() == 'n' {
		return skills, nil
	}
	for decoder.PeekKind() != ']' {
		if len(skills) >= MaxMagpieSkills {
			return nil, fmt.Errorf("too many magpie skill records")
		}
		var item MagpieSkill
		if err := json.UnmarshalDecode(decoder, &item); err != nil {
			return nil, fmt.Errorf("invalid magpie skill record")
		}
		skills = append(skills, item)
	}
	if _, err := decoder.ReadToken(); err != nil {
		return nil, fmt.Errorf("invalid magpie skill records")
	}
	return skills, nil
}

func normalizeMagpieSource(source *MagpieSource) error {
	if source == nil {
		return nil
	}
	switch source.Kind {
	case "github":
		if strings.Count(source.Repo, "/") != 1 || strings.TrimSpace(source.Repo) != source.Repo || strings.ContainsFunc(source.Ref, unsafeIdentifierRune) || !validMagpieSkillPath(source.Path) {
			return fmt.Errorf("invalid magpie repository source")
		}
		reference, err := repository.ParseReference(source.Repo, source.Ref)
		if err != nil {
			return fmt.Errorf("invalid magpie repository source")
		}
		source.Repo = reference.NameWithOwner
		source.Ref = reference.Branch
	case "git", "folder":
		*source = MagpieSource{Kind: source.Kind}
	default:
		return fmt.Errorf("unsupported magpie source kind")
	}
	return nil
}

func validMagpieSkillPath(value string) bool {
	return len(value) <= 1024 && value != ".." && !strings.HasPrefix(value, "../") && !path.IsAbs(value) && !strings.ContainsAny(value, "\\:") && !strings.ContainsFunc(value, unsafeIdentifierRune) && (value == "" || path.Clean(value) == value)
}

func validMagpieResult(result MagpiePreview, skills []MagpieSkill, repositories map[MagpieRepository]struct{}) bool {
	if len(result.Skills) != len(skills) || len(result.Repositories) != len(repositories) {
		return false
	}
	for index, actual := range result.Skills {
		expected := skills[index]
		if actual.Name != expected.Name || (actual.Source == nil) != (expected.Source == nil) || actual.Source != nil && *actual.Source != *expected.Source {
			return false
		}
	}
	for _, item := range result.Repositories {
		if _, exists := repositories[item]; !exists {
			return false
		}
		delete(repositories, item)
	}
	return true
}
