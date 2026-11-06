package state

import (
	"context"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"

	"github.com/ca-x/cognate/internal/repository"
)

const snapshotSourceSkillsBytes = 2 + maxSkillSources*(6*(256+1024)+len(`{"name":"","path":""},`))

func snapshotValidateRepositorySources(ctx context.Context, pool *sql.DB, remaining uint64) (uint64, error) {
	var invalid bool
	if pool.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM repository_sources WHERE id<=0 OR user_id<=0)").Scan(&invalid) != nil || invalid {
		return remaining, ErrLocalSnapshotInvalid
	}
	var owner, after int64
	var sources, skills uint64
	for {
		if err := ctx.Err(); err != nil {
			return remaining, err
		}
		var identifier, userID int64
		var publicID, name, branch, encoded, registered string
		query := "SELECT id,user_id," + snapshotText("public_id", 64) + "," + snapshotText("name_with_owner", 256) + "," + snapshotText("branch", 256) + "," + snapshotText("skills", snapshotSourceSkillsBytes) + "," + snapshotText("registered_at", 128) + " FROM repository_sources WHERE user_id>? OR (user_id=? AND id>?) ORDER BY user_id,id LIMIT 1"
		err := pool.QueryRowContext(ctx, query, owner, owner, after).Scan(&identifier, &userID, &publicID, &name, &branch, &encoded, &registered)
		if errors.Is(err, sql.ErrNoRows) {
			return remaining, nil
		}
		if err != nil || identifier < 1 || userID < 1 || userID < owner || userID == owner && identifier <= after {
			return remaining, ErrLocalSnapshotInvalid
		}
		if userID != owner {
			sources, skills = 0, 0
		}
		if remaining == 0 || sources >= maxRepositorySources {
			return remaining, ErrLocalSnapshotBudget
		}
		remaining--
		parsed, err := repository.ParseReference(name, branch)
		if err != nil || parsed.NameWithOwner != name || parsed.Branch != branch || !snapshotValidText(publicID, 64, false) || !snapshotValidText(registered, 128, false) {
			return remaining, ErrLocalSnapshotInvalid
		}
		var owners int
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=?", userID).Scan(&owners) != nil || owners != 1 {
			return remaining, ErrLocalSnapshotInvalid
		}
		count, err := snapshotValidateSourceSkills(ctx, name, branch, encoded, min(remaining, maxSkillSources-skills))
		if err != nil {
			return remaining, err
		}
		remaining -= count
		skills += count
		sources++
		owner, after = userID, identifier
	}
}

func snapshotValidateSourceSkills(ctx context.Context, name, branch, encoded string, remaining uint64) (uint64, error) {
	decoder := jsontext.NewDecoder(strings.NewReader(encoded))
	token, err := decoder.ReadToken()
	if err != nil || token.Kind() != '[' {
		return 0, ErrLocalSnapshotInvalid
	}
	seen := make(map[repository.SkillSource]bool)
	var count uint64
	for decoder.PeekKind() != ']' {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		if count >= remaining {
			return 0, ErrLocalSnapshotBudget
		}
		var entry struct {
			Name *string `json:"name"`
			Path *string `json:"path"`
		}
		if json.UnmarshalDecode(decoder, &entry, json.RejectUnknownMembers(true)) != nil || entry.Name == nil || entry.Path == nil {
			return 0, ErrLocalSnapshotInvalid
		}
		source := repository.SkillSource{Name: *entry.Name, Path: *entry.Path}
		reference := ImportSkillSourceReference{Repository: name, Branch: branch, SafeName: source.Name, LogicalPath: source.Path}
		if seen[source] || !reference.Valid() {
			return 0, ErrLocalSnapshotInvalid
		}
		seen[source] = true
		count++
	}
	if _, err := decoder.ReadToken(); err != nil {
		return 0, ErrLocalSnapshotInvalid
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return 0, ErrLocalSnapshotInvalid
	}
	return count, nil
}
