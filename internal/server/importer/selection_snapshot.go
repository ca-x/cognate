package importer

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strconv"

	"github.com/ca-x/cognate/internal/repository"
)

func projectCCSwitchSelection(ctx context.Context, raw []byte, view *SelectionView) error {
	return readSelectionSnapshot(ctx, raw, func(ctx context.Context, connection *sql.Conn) error {
		var version, triggers int
		if connection.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version) != nil || version != 24 {
			return ErrSelectionUnsupported
		}
		if connection.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='trigger'`).Scan(&triggers) != nil || triggers != 0 {
			return ErrSelectionInvalid
		}
		for _, table := range []struct {
			name    string
			columns []string
		}{
			{"skill_repos", []string{"owner", "name", "branch", "enabled"}},
			{"skills", []string{"id", "name", "repo_owner", "repo_name", "repo_branch", "content_hash"}},
			{"providers", []string{"id", "app_type", "name", "settings_config", "meta", "website_url", "category", "created_at", "sort_index", "notes", "icon", "icon_color", "is_current", "in_failover_queue"}},
		} {
			if err := selectionTable(ctx, connection, table.name, table.columns); err != nil {
				return err
			}
		}
		knownRepositoryColumns, err := selectionColumnSet(ctx, connection, "skill_repos", []string{"owner", "name", "branch", "enabled"})
		if err != nil {
			return err
		}
		if !knownRepositoryColumns {
			return ErrSelectionUnsupported
		}
		repositories, err := readCCSwitchRepositoryRows(ctx, connection)
		if err != nil {
			return ErrSelectionInvalid
		}
		for _, item := range repositories {
			reference, err := selectionReference(item.Owner, item.Name, item.Branch)
			if err != nil {
				return err
			}
			before := len(view.Repositories)
			appendSelectionRepository(view, reference.NameWithOwner, reference.Branch, new(item.Enabled))
			if len(view.Repositories) == before {
				return ErrSelectionInvalid
			}
		}
		if err := readSelectionSkills(ctx, connection, view); err != nil {
			return err
		}
		if err := readSelectionProviders(ctx, connection, view); err != nil {
			return err
		}
		for _, item := range []struct{ table, kind string }{{"mcp_servers", "mcp"}, {"prompts", "prompt"}} {
			present, err := selectionTablePresent(ctx, connection, item.table)
			if err != nil {
				return err
			}
			if !present {
				continue
			}
			var count int
			query := `SELECT count(*) FROM mcp_servers`
			if item.table == "prompts" {
				query = `SELECT count(*) FROM prompts`
			}
			if connection.QueryRowContext(ctx, query).Scan(&count) != nil {
				return ErrSelectionInvalid
			}
			if count > 1000 {
				return ErrSelectionLimit
			}
			if count > 0 {
				view.Configurations = append(view.Configurations, SelectionConfiguration{ID: selectionID(view, "unsupported", item.table), Kind: item.kind, Status: SelectionUnsupportedKind, Slots: []SelectionSlot{}, Notices: []SelectionNotice{{Code: SelectionSourceDataExcluded, Count: count}}})
			}
		}
		var excluded int
		if connection.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' AND name NOT IN ('skill_repos','skills','providers','provider_endpoints','mcp_servers','prompts')`).Scan(&excluded) != nil {
			return ErrSelectionInvalid
		}
		if excluded > 128 {
			return ErrSelectionLimit
		}
		selectionNotice(&view.Notices, SelectionSourceDataExcluded, excluded)
		return ctx.Err()
	})
}

func selectionTablePresent(ctx context.Context, connection *sql.Conn, table string) (bool, error) {
	var count int
	if connection.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_list(?) WHERE schema='main' AND type='table'`, table).Scan(&count) != nil {
		return false, ErrSelectionInvalid
	}
	var objects int
	if connection.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_schema WHERE name=?`, table).Scan(&objects) != nil || objects != count {
		return false, ErrSelectionInvalid
	}
	return count == 1, nil
}

func selectionTable(ctx context.Context, connection *sql.Conn, table string, columns []string) error {
	present, err := selectionTablePresent(ctx, connection, table)
	if err != nil || !present {
		return ErrSelectionInvalid
	}
	rows, err := connection.QueryContext(ctx, `SELECT name,hidden FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer rows.Close()
	seen := make(map[string]bool)
	for rows.Next() {
		var name string
		var hidden int
		if rows.Scan(&name, &hidden) != nil || hidden != 0 || len(seen) >= 128 {
			return ErrSelectionInvalid
		}
		seen[name] = true
	}
	if rows.Err() != nil {
		return ErrSelectionInvalid
	}
	for _, column := range columns {
		if !seen[column] {
			return ErrSelectionInvalid
		}
	}
	return nil
}

func readSelectionSkills(ctx context.Context, connection *sql.Conn, view *SelectionView) error {
	knownColumns, err := selectionColumnSet(ctx, connection, "skills", []string{"id", "name", "description", "directory", "repo_owner", "repo_name", "repo_branch", "readme_url", "content_hash", "installed_at", "updated_at", "enabled_claude", "enabled_codex", "enabled_gemini", "enabled_grokbuild", "enabled_opencode", "enabled_mcode", "enabled_agy", "enabled_kimi", "enabled_dsh", "enabled_hermes", "enabled_pi", "enabled_omp"})
	if err != nil {
		return err
	}
	rows, err := connection.QueryContext(ctx, `SELECT id,name,repo_owner,repo_name,repo_branch FROM skills ORDER BY id LIMIT 1001`)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(view.Skills) >= 1000 {
			return ErrSelectionLimit
		}
		var identifier, name string
		var owner, repositoryName, branch sql.NullString
		if rows.Scan(&identifier, &name, &owner, &repositoryName, &branch) != nil || !selectionText(identifier, 256) || !selectionText(name, 256) {
			return ErrSelectionInvalid
		}
		candidate := SelectionSkill{ID: selectionID(view, "skill", identifier), Name: name, Status: SelectionUnsupportedKind, Notices: []SelectionNotice{{Code: SelectionMetadataExcluded, Count: 1}}}
		if owner.Valid && repositoryName.Valid {
			reference, err := selectionReference(owner.String, repositoryName.String, branch.String)
			if err != nil {
				return err
			}
			candidate.Repository, candidate.Branch, candidate.Status = reference.NameWithOwner, reference.Branch, SelectionNeedsBinding
		}
		if !knownColumns {
			candidate.Status = SelectionUnsupportedField
		}
		view.Skills = append(view.Skills, candidate)
	}
	if rows.Err() != nil {
		return ErrSelectionInvalid
	}
	return nil
}

func readSelectionProviders(ctx context.Context, connection *sql.Conn, view *SelectionView) error {
	knownColumns, err := selectionColumnSet(ctx, connection, "providers", []string{"id", "app_type", "name", "settings_config", "meta", "website_url", "category", "created_at", "sort_index", "notes", "icon", "icon_color", "is_current", "in_failover_queue"})
	if err != nil {
		return err
	}
	endpoints, err := selectionTablePresent(ctx, connection, "provider_endpoints")
	if err != nil {
		return err
	}
	if endpoints {
		if err := selectionTable(ctx, connection, "provider_endpoints", []string{"provider_id", "app_type", "url"}); err != nil {
			return err
		}
	}
	rows, err := connection.QueryContext(ctx, `SELECT id,app_type,name,settings_config,meta FROM providers ORDER BY app_type,id LIMIT 33`)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer rows.Close()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if len(view.Configurations) >= MaxSelectionProviders {
			return ErrSelectionLimit
		}
		var identifier, agent, name string
		var settings, meta []byte
		if rows.Scan(&identifier, &agent, &name, &settings, &meta) != nil || !selectionText(identifier, 256) {
			return ErrSelectionInvalid
		}
		if len(settings) > 256<<10 {
			clear(settings)
			clear(meta)
			return ErrSelectionLimit
		}
		candidate := SelectionConfiguration{ID: selectionID(view, "provider", agent+"\x00"+identifier), Kind: "provider", Status: SelectionUnsupportedAgent, Slots: []SelectionSlot{}, Notices: []SelectionNotice{{Code: SelectionMetadataExcluded, Count: 1}}}
		if slices.Contains([]string{"claude", "claude-desktop", "codex", "gemini", "grokbuild", "opencode", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp"}, agent) {
			candidate.AgentID = agent
		}
		if selectionText(name, 128) {
			candidate.Name = name
		} else {
			candidate.Status = SelectionUnsupportedField
		}
		if !knownColumns {
			candidate.Status = SelectionUnsupportedField
		}
		metadata, metadataErr := sourceObject(meta)
		metadataEmpty := metadataErr == nil && len(metadata) == 0
		clearSourceObject(metadata)
		clear(meta)
		var endpointCount int
		if endpoints && connection.QueryRowContext(ctx, `SELECT count(*) FROM provider_endpoints WHERE provider_id=? AND app_type=?`, identifier, agent).Scan(&endpointCount) != nil {
			clear(settings)
			return ErrSelectionInvalid
		}
		if agent == "claude" || agent == "codex" || agent == "gemini" {
			candidate.Status = SelectionUnsupportedField
			if metadataEmpty && endpointCount == 0 && knownColumns {
				var parsedDraft = candidate.Draft
				var parsedErr error
				switch agent {
				case "claude":
					draft, slots, err := parseClaudeSelection(ctx, candidate.Name, settings)
					parsedDraft, candidate.Slots, parsedErr = &draft, slots, err
				case "codex":
					draft, slots, err := parseCodexSelection(ctx, candidate.Name, settings)
					parsedDraft, candidate.Slots, parsedErr = &draft, slots, err
				case "gemini":
					draft, slots, err := parseGeminiSelection(ctx, candidate.Name, settings)
					parsedDraft, candidate.Slots, parsedErr = &draft, slots, err
				}
				if parsedErr == nil {
					candidate.Draft, candidate.Status = parsedDraft, SelectionNeedsChoice
					if agent == "codex" && selectionName(candidate.Name) {
						candidate.Status = SelectionReady
					}
					selectionNotice(&candidate.Notices, SelectionCredentialsOmitted, 1)
				} else if ctx.Err() != nil {
					clear(settings)
					return ctx.Err()
				}
			}
		}
		clear(settings)
		view.Configurations = append(view.Configurations, candidate)
	}
	if rows.Err() != nil {
		return ErrSelectionInvalid
	}
	return nil
}

func selectionColumnSet(ctx context.Context, connection *sql.Conn, table string, allowed []string) (bool, error) {
	rows, err := connection.QueryContext(ctx, `SELECT name FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return false, ErrSelectionInvalid
	}
	defer rows.Close()
	known := true
	for rows.Next() {
		var name string
		if rows.Scan(&name) != nil {
			return false, ErrSelectionInvalid
		}
		if !slices.Contains(allowed, name) {
			known = false
		}
	}
	if rows.Err() != nil {
		return false, ErrSelectionInvalid
	}
	return known, nil
}

func projectMagpieSelection(ctx context.Context, raw []byte, view *SelectionView) error {
	root, err := sourceObject(raw)
	if err != nil {
		return ErrSelectionInvalid
	}
	defer clearSourceObject(root)
	var skills []jsontext.Value
	if value, present := root["skills"]; !present || len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &skills) != nil {
		return ErrSelectionInvalid
	}
	defer func() {
		for _, skill := range skills {
			clear(skill)
		}
	}()
	if len(skills) > MaxMagpieSkills {
		return ErrSelectionLimit
	}
	seen := make(map[string]bool)
	for index, rawSkill := range skills {
		if err := ctx.Err(); err != nil {
			return err
		}
		object, err := sourceObject(rawSkill)
		if err != nil {
			return ErrSelectionInvalid
		}
		name, err := sourceString(object["name"])
		if err != nil || !selectionText(name, 256) || seen[name] {
			clearSourceObject(object)
			return ErrSelectionInvalid
		}
		seen[name] = true
		candidate := SelectionSkill{ID: selectionID(view, "skill", strconv.Itoa(index)), Name: name, Status: SelectionUnsupportedKind, Notices: []SelectionNotice{}}
		if sourceFields(object, "name", "source", "agents", "hash") {
			if source, present := object["source"]; present {
				parsed, parseErr := sourceObject(source)
				if parseErr == nil {
					kind, _ := sourceString(parsed["kind"])
					if kind == "github" && sourceFields(parsed, "kind", "repo", "ref", "path") {
						var item MagpieSource
						if json.Unmarshal(source, &item) != nil || normalizeMagpieSource(&item) != nil {
							clearSourceObject(parsed)
							clearSourceObject(object)
							return ErrSelectionInvalid
						}
						candidate.Repository, candidate.Branch = item.Repo, item.Ref
						candidate.Status = SelectionNeedsBinding
						if _, present := parsed["path"]; present {
							candidate.Path = new(item.Path)
						}
						appendSelectionRepository(view, item.Repo, item.Ref, nil)
					}
					clearSourceObject(parsed)
				}
			}
		} else {
			candidate.Status = SelectionUnsupportedField
		}
		if len(object["agents"]) > 0 || len(object["hash"]) > 0 {
			selectionNotice(&candidate.Notices, SelectionMetadataExcluded, 1)
		}
		clearSourceObject(object)
		view.Skills = append(view.Skills, candidate)
	}
	for _, item := range []struct{ field, kind string }{{"mcp", "mcp"}, {"instructions", "prompt"}} {
		value, present := root[item.field]
		if !present {
			continue
		}
		count := 0
		if item.field == "mcp" {
			var entries []jsontext.Value
			if len(value) == 0 || value[0] != '[' || json.Unmarshal(value, &entries) != nil {
				return ErrSelectionInvalid
			}
			count = len(entries)
			for _, entry := range entries {
				clear(entry)
			}
		} else {
			object, err := sourceObject(value)
			if err != nil {
				return ErrSelectionInvalid
			}
			for _, field := range []string{"sets", "agents"} {
				if rawEntries, present := object[field]; present {
					var entries []jsontext.Value
					if len(rawEntries) == 0 || rawEntries[0] != '[' || json.Unmarshal(rawEntries, &entries) != nil {
						clearSourceObject(object)
						return ErrSelectionInvalid
					}
					count += len(entries)
					for _, entry := range entries {
						clear(entry)
					}
				}
			}
			if !sourceFields(object, "sets", "agents") {
				selectionNotice(&view.Notices, SelectionSourceDataExcluded, 1)
			}
			clearSourceObject(object)
		}
		if count > 1000 {
			return ErrSelectionLimit
		}
		if count > 0 {
			view.Configurations = append(view.Configurations, SelectionConfiguration{ID: selectionID(view, "unsupported", item.field), Kind: item.kind, Status: SelectionUnsupportedKind, Slots: []SelectionSlot{}, Notices: []SelectionNotice{{Code: SelectionSourceDataExcluded, Count: count}}})
		}
	}
	for field := range root {
		switch field {
		case "skills", "mcp", "instructions":
			continue
		default:
			selectionNotice(&view.Notices, SelectionSourceDataExcluded, 1)
		}
	}
	return nil
}

func appendSelectionRepository(view *SelectionView, name, branch string, enabled *bool) {
	for _, candidate := range view.Repositories {
		if candidate.Repository == name && candidate.Branch == branch {
			return
		}
	}
	view.Repositories = append(view.Repositories, SelectionRepository{ID: selectionID(view, "repository", name+"\x00"+branch), Repository: name, Branch: branch, SourceEnabled: enabled})
}

func selectionReference(owner, name, branch string) (repository.Reference, error) {
	reference, err := repository.ParseReference(owner+"/"+name, branch)
	if err != nil {
		return repository.Reference{}, ErrSelectionInvalid
	}
	return reference, nil
}
