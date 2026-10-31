package importer

import (
	"context"
	_ "embed"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/repository"
)

//go:embed npxskills.mjs
var npxSkillsModule string

const MaxNPXSkillsLockBytes = 1 << 20
const MaxNPXSkills = 1000

func projectNPXSelection(ctx context.Context, raw []byte, view *SelectionView) error {
	root, err := sourceObject(raw)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer clearSourceObject(root)
	var version uint32
	if json.Unmarshal(root["version"], &version) != nil || version != 3 {
		return ErrSelectionUnsupported
	}
	var entries map[string]jsontext.Value
	if len(root["skills"]) == 0 || root["skills"][0] != '{' || json.Unmarshal(root["skills"], &entries) != nil || entries == nil {
		return ErrSelectionInvalid
	}
	defer clearSourceObject(entries)
	if len(entries) > MaxNPXSkills {
		return ErrSelectionLimit
	}
	for _, name := range slices.Sorted(maps.Keys(entries)) {
		if err := ctx.Err(); err != nil {
			return err
		}
		if !selectionText(name, 256) || strings.ContainsFunc(name, unsafeIdentifierRune) {
			return ErrSelectionInvalid
		}
		if err := projectNPXSkill(view, name, entries[name]); err != nil {
			return err
		}
		if len(view.Skills) > 128 || len(view.Repositories) > 16 {
			return ErrSelectionLimit
		}
	}
	for field := range root {
		if field != "version" && field != "skills" {
			selectionNotice(&view.Notices, SelectionSourceDataExcluded, 1)
		}
	}
	return nil
}

func projectNPXSkill(view *SelectionView, name string, raw []byte) error {
	object, err := sourceObject(raw)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer clearSourceObject(object)
	kind, err := npxSourceType(object)
	if err != nil {
		return err
	}
	if kind != "github" {
		selectionNotice(&view.Notices, SelectionSourceDataExcluded, 1)
		return nil
	}
	candidate := SelectionSkill{ID: selectionID(view, "skill", name), Name: name, Status: SelectionNeedsBinding, Notices: []SelectionNotice{}}
	if !sourceFields(object, "source", "sourceType", "source_type", "sourceUrl", "ref", "skillPath", "skillFolderHash", "installedAt", "updatedAt", "pluginName", "sourceBaseUrl", "wellKnownDigest", "installMode") {
		candidate.Status = SelectionUnsupportedField
		selectionNotice(&candidate.Notices, SelectionSourceDataExcluded, 1)
		view.Skills = append(view.Skills, candidate)
		return nil
	}
	source, err := sourceString(object["source"])
	if err != nil || strings.Count(source, "/") != 1 || strings.TrimSpace(source) != source || strings.ContainsAny(source, ":?#\\") {
		return ErrSelectionInvalid
	}
	ref := ""
	if value, present := object["ref"]; present {
		if ref, err = sourceString(value); err != nil {
			return ErrSelectionInvalid
		}
	}
	reference, err := repository.ParseReference(source, ref)
	if err != nil {
		return ErrSelectionInvalid
	}
	if value, present := object["sourceUrl"]; present {
		address, err := sourceString(value)
		if err != nil || !validNPXSourceURL(address, reference.NameWithOwner, ref) {
			return ErrSelectionInvalid
		}
	}
	if value, present := object["skillPath"]; present {
		location, err := sourceString(value)
		if err != nil || location == "" || !validMagpieSkillPath(location) {
			return ErrSelectionInvalid
		}
		if path.Base(location) == "SKILL.md" {
			location = path.Dir(location)
		}
		candidate.Path = new(location)
	}
	candidate.Repository, candidate.Branch = reference.NameWithOwner, reference.Branch
	for field := range object {
		switch field {
		case "source", "sourceType", "source_type", "sourceUrl", "ref", "skillPath":
		default:
			selectionNotice(&candidate.Notices, SelectionMetadataExcluded, 1)
		}
	}
	appendSelectionRepository(view, reference.NameWithOwner, reference.Branch, nil)
	view.Skills = append(view.Skills, candidate)
	return nil
}

func npxSourceType(object map[string]jsontext.Value) (string, error) {
	kind := ""
	for _, key := range []string{"sourceType", "source_type"} {
		if raw, present := object[key]; present {
			value, err := sourceString(raw)
			if err != nil || !selectionText(value, 64) || strings.ContainsFunc(value, unsafeIdentifierRune) || kind != "" && kind != value {
				return "", ErrSelectionInvalid
			}
			kind = value
		}
	}
	if kind == "" {
		return "", ErrSelectionInvalid
	}
	return kind, nil
}

func npxGlobalExclusions(raw []byte) ([3]int, error) {
	counts := [3]int{}
	root, err := sourceObject(raw)
	if err != nil {
		return counts, ErrSelectionInvalid
	}
	defer clearSourceObject(root)
	var entries map[string]jsontext.Value
	if json.Unmarshal(root["skills"], &entries) != nil {
		return counts, ErrSelectionInvalid
	}
	defer clearSourceObject(entries)
	for _, raw := range entries {
		entry, err := sourceObject(raw)
		if err != nil {
			return counts, ErrSelectionInvalid
		}
		kind, err := npxSourceType(entry)
		clearSourceObject(entry)
		if err != nil {
			return counts, err
		}
		if kind != "github" {
			counts[1]++
		}
	}
	for field := range root {
		switch field {
		case "version", "skills":
		case "dismissed", "lastSelectedAgents":
			counts[0]++
		default:
			counts[2]++
		}
	}
	return counts, nil
}

func validNPXSourceURL(raw, name, ref string) bool {
	if len(raw) > 2048 {
		return false
	}
	address, err := url.Parse(raw)
	if err != nil || address.Scheme != "https" || !strings.EqualFold(address.Host, "github.com") || address.User != nil || address.RawQuery != "" || address.ForceQuery || address.Fragment != "" || address.RawPath != "" {
		return false
	}
	parts := strings.Split(strings.Trim(address.Path, "/"), "/")
	if len(parts) < 2 || !strings.EqualFold(parts[0]+"/"+strings.TrimSuffix(parts[1], ".git"), name) {
		return false
	}
	if len(parts) == 2 {
		return true
	}
	if len(parts) < 4 || parts[2] != "tree" || ref == "" {
		return false
	}
	remaining := strings.Join(parts[3:], "/")
	return remaining == ref || strings.HasPrefix(remaining, ref+"/")
}
