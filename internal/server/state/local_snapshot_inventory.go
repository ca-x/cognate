package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"io"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/skill"
)

const snapshotKnownSchema = "34738287b1235358fc73bbefed6fabdde0968bf8340dee784fc0a9b448bc9235"
const snapshotKnownNormalizedSchema = "7e56e095b6943f47835a6a019ad99655b601224afa02726f2270312a5a404cfc"

type snapshotInventoryCount struct{ repositories, skills, packs, items, total uint64 }

func snapshotText(column string, maximum int) string {
	return "CASE WHEN length(CAST(" + column + " AS BLOB))<=" + strconv.Itoa(maximum) + " THEN " + column + " END"
}

func snapshotWriteInventory(ctx context.Context, pool *sql.DB, output io.Writer, limits LocalSnapshotLimits) (counts snapshotInventoryCount, size int64, digest [32]byte, returnedError error) {
	hash := sha256.New()
	for _, kind := range []string{"repository", "skill", "pack", "pack_item"} {
		var after int64
		for {
			query := ""
			switch kind {
			case "repository":
				query = "SELECT id," + snapshotText("public_id", 64) + ",user_id," + snapshotText("name_with_owner", 256) + "," + snapshotText("branch", 1024) + "," + snapshotText("archive_hash", 64) + ",skill_count,publication_revision FROM collected_repositories WHERE id>? ORDER BY id LIMIT 128"
			case "skill":
				query = "SELECT id," + snapshotText("public_id", 64) + ",repository_id," + snapshotText("path", 1024) + "," + snapshotText("content_hash", 64) + "," + snapshotText("name", 256) + "," + snapshotText("description", 4096) + " FROM collected_skills WHERE id>? ORDER BY id LIMIT 128"
			case "pack":
				query = "SELECT id," + snapshotText("public_id", 64) + ",user_id,revision,item_count FROM packs WHERE id>? ORDER BY id LIMIT 128"
			case "pack_item":
				query = "SELECT id,pack_id,skill_id," + snapshotText("slug", 256) + " FROM pack_items WHERE id>? ORDER BY id LIMIT 128"
			}
			rows, err := pool.QueryContext(ctx, query, after)
			if err != nil {
				return counts, size, digest, ErrLocalSnapshotInvalid
			}
			page := make([]snapshotReference, 0, 128)
			var failure error
			for rows.Next() {
				row := snapshotReference{Kind: kind}
				switch kind {
				case "repository":
					err = rows.Scan(&row.ID, &row.PublicID, &row.Owner, &row.Name, &row.Branch, &row.ArchiveHash, &row.SkillCount, &row.Revision)
				case "skill":
					err = rows.Scan(&row.ID, &row.PublicID, &row.Repository, &row.Path, &row.ContentHash, &row.skillName, &row.skillDescription)
				case "pack":
					err = rows.Scan(&row.ID, &row.PublicID, &row.Owner, &row.Revision, &row.ItemCount)
				case "pack_item":
					err = rows.Scan(&row.ID, &row.Pack, &row.Skill, &row.Slug)
				}
				if err != nil || row.ID <= after || len(page) >= 128 {
					failure = ErrLocalSnapshotInvalid
					break
				}
				page = append(page, row)
				after = row.ID
			}
			if rows.Err() != nil {
				failure = ErrLocalSnapshotInvalid
			}
			if rows.Close() != nil {
				return counts, size, digest, ErrLocalSnapshotUncertain
			}
			if failure != nil {
				return counts, size, digest, failure
			}
			for _, row := range page {
				if counts.total >= limits.MaxRecords {
					return counts, size, digest, ErrLocalSnapshotBudget
				}
				if err = snapshotValidateReference(ctx, pool, row); err != nil {
					return counts, size, digest, err
				}
				encoded, err := snapshotEncodeReference(row)
				if err != nil || len(encoded)+1 > 65536 || int64(len(encoded)+1) > limits.MaxInventoryBytes-size {
					return counts, size, digest, ErrLocalSnapshotBudget
				}
				encoded = append(encoded, '\n')
				if err = ctx.Err(); err != nil {
					return counts, size, digest, err
				}
				written, err := output.Write(encoded)
				if err != nil || written != len(encoded) {
					return counts, size, digest, ErrLocalSnapshotInvalid
				}
				hash.Write(encoded)
				size += int64(written)
				counts.total++
				switch kind {
				case "repository":
					counts.repositories++
				case "skill":
					counts.skills++
				case "pack":
					counts.packs++
				case "pack_item":
					counts.items++
				}
			}
			if len(page) < 128 {
				break
			}
		}
	}
	if err := snapshotValidateCategories(ctx, pool, limits.MaxRecords-counts.total); err != nil {
		return counts, size, digest, err
	}
	copy(digest[:], hash.Sum(nil))
	return counts, size, digest, nil
}

func snapshotValidText(value string, maximum int, empty bool) bool {
	return (empty || value != "") && len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsFunc(value, unicode.IsControl)
}

func snapshotValidateReference(ctx context.Context, pool *sql.DB, row snapshotReference) error {
	if row.ID <= 0 {
		return ErrLocalSnapshotInvalid
	}
	if row.Kind != "pack_item" && !snapshotValidText(row.PublicID, 64, false) {
		return ErrLocalSnapshotInvalid
	}
	var first, second, count int64
	switch row.Kind {
	case "repository":
		parsed, err := repository.ParseReference(row.Name, row.Branch)
		if err != nil || parsed.NameWithOwner != row.Name || parsed.Branch != row.Branch || !validContentHash(row.ArchiveHash) || row.SkillCount < 1 || row.SkillCount > 512 || row.Revision < 1 || row.Owner < 1 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=?", row.Owner).Scan(&count) != nil || count != 1 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM collected_skills WHERE repository_id=?", row.ID).Scan(&count) != nil || count != row.SkillCount {
			return ErrLocalSnapshotInvalid
		}
	case "skill":
		if row.Repository < 1 || row.Path == "" || len(row.Path) > 1024 || !utf8.ValidString(row.Path) || path.IsAbs(row.Path) || path.Clean(row.Path) != row.Path || row.Path == ".." || strings.HasPrefix(row.Path, "../") || strings.ContainsAny(row.Path, "\\\x00") || !validContentHash(row.ContentHash) || !snapshotValidText(row.skillName, 256, false) || !utf8.ValidString(row.skillDescription) || row.skillDescription == "" || len(row.skillDescription) > 4096 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM collected_repositories WHERE id=?", row.Repository).Scan(&count) != nil || count != 1 {
			return ErrLocalSnapshotInvalid
		}
	case "pack":
		if row.Owner < 1 || row.Revision < 1 || row.ItemCount < 0 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=?", row.Owner).Scan(&count) != nil || count != 1 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM pack_items WHERE pack_id=?", row.ID).Scan(&count) != nil || count != row.ItemCount {
			return ErrLocalSnapshotInvalid
		}
	case "pack_item":
		if row.Pack < 1 || row.Skill < 1 || skill.ValidatePortableSlug(row.Slug) != nil {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT p.user_id,r.user_id FROM packs p, collected_skills s LEFT JOIN collected_repositories r ON r.id=s.repository_id WHERE p.id=? AND s.id=?", row.Pack, row.Skill).Scan(&first, &second) != nil || first != second || first < 1 {
			return ErrLocalSnapshotInvalid
		}
		if pool.QueryRowContext(ctx, "SELECT count(*) FROM pack_items i JOIN collected_skills s ON s.id=i.skill_id WHERE i.pack_id=? AND i.slug=? AND s.content_hash<>(SELECT content_hash FROM collected_skills WHERE id=?)", row.Pack, row.Slug, row.Skill).Scan(&count) != nil || count != 0 {
			return ErrLocalSnapshotInvalid
		}
	default:
		return ErrLocalSnapshotInvalid
	}
	return nil
}

func snapshotValidateCategories(ctx context.Context, pool *sql.DB, remaining uint64) error {
	var after int64
	for {
		rows, err := pool.QueryContext(ctx, "SELECT id,"+snapshotText("name", 80)+","+snapshotText("normalized_name", 1024)+",user_id,revision FROM categories WHERE id>? ORDER BY id LIMIT 128", after)
		if err != nil {
			return ErrLocalSnapshotInvalid
		}
		type categoryRow struct {
			id, owner, revision int64
			name, normalized    string
		}
		page := make([]categoryRow, 0, 128)
		var failure error
		for rows.Next() {
			var row categoryRow
			if rows.Scan(&row.id, &row.name, &row.normalized, &row.owner, &row.revision) != nil || row.id <= after || len(page) >= 128 {
				failure = ErrLocalSnapshotInvalid
				break
			}
			page = append(page, row)
			after = row.id
		}
		if rows.Err() != nil {
			failure = ErrLocalSnapshotInvalid
		}
		if rows.Close() != nil {
			return ErrLocalSnapshotUncertain
		}
		if failure != nil {
			return failure
		}
		for _, row := range page {
			if remaining == 0 {
				return ErrLocalSnapshotBudget
			}
			remaining--
			name, normalized, err := categoryName(row.name)
			if err != nil || name != row.name || normalized != row.normalized || row.revision < 1 || row.owner < 1 {
				return ErrLocalSnapshotInvalid
			}
			var count int
			if pool.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE id=?", row.owner).Scan(&count) != nil || count != 1 {
				return ErrLocalSnapshotInvalid
			}
		}
		if len(page) < 128 {
			break
		}
	}
	var category, selected int64
	for {
		rows, err := pool.QueryContext(ctx, "SELECT category_id,collected_skill_id FROM category_skills WHERE category_id>? OR (category_id=? AND collected_skill_id>?) ORDER BY category_id,collected_skill_id LIMIT 128", category, category, selected)
		if err != nil {
			return ErrLocalSnapshotInvalid
		}
		page := make([][2]int64, 0, 128)
		var failure error
		for rows.Next() {
			var pair [2]int64
			if rows.Scan(&pair[0], &pair[1]) != nil || pair[0] < 1 || pair[1] < 1 || len(page) >= 128 {
				failure = ErrLocalSnapshotInvalid
				break
			}
			page = append(page, pair)
		}
		if rows.Err() != nil {
			failure = ErrLocalSnapshotInvalid
		}
		if rows.Close() != nil {
			return ErrLocalSnapshotUncertain
		}
		if failure != nil {
			return failure
		}
		for _, pair := range page {
			if remaining == 0 {
				return ErrLocalSnapshotBudget
			}
			remaining--
			var left, right int64
			if pool.QueryRowContext(ctx, "SELECT c.user_id,r.user_id FROM categories c, collected_skills s LEFT JOIN collected_repositories r ON r.id=s.repository_id WHERE c.id=? AND s.id=?", pair[0], pair[1]).Scan(&left, &right) != nil || left != right || left < 1 {
				return ErrLocalSnapshotInvalid
			}
			category, selected = pair[0], pair[1]
		}
		if len(page) < 128 {
			break
		}
	}
	return nil
}

func snapshotHash(data []byte) string {
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}

func snapshotEncodeReference(row snapshotReference) ([]byte, error) {
	switch row.Kind {
	case "repository":
		return json.Marshal(struct {
			Kind        string `json:"kind"`
			ID          int64  `json:"id"`
			PublicID    string `json:"public_id"`
			Owner       int64  `json:"owner"`
			Name        string `json:"name"`
			Branch      string `json:"branch"`
			ArchiveHash string `json:"archive_hash"`
			SkillCount  int64  `json:"skill_count"`
			Revision    int64  `json:"revision"`
		}{row.Kind, row.ID, row.PublicID, row.Owner, row.Name, row.Branch, row.ArchiveHash, row.SkillCount, row.Revision})
	case "skill":
		return json.Marshal(struct {
			Kind        string `json:"kind"`
			ID          int64  `json:"id"`
			PublicID    string `json:"public_id"`
			Repository  int64  `json:"repository"`
			Path        string `json:"path"`
			ContentHash string `json:"content_hash"`
		}{row.Kind, row.ID, row.PublicID, row.Repository, row.Path, row.ContentHash})
	case "pack":
		return json.Marshal(struct {
			Kind      string `json:"kind"`
			ID        int64  `json:"id"`
			PublicID  string `json:"public_id"`
			Owner     int64  `json:"owner"`
			Revision  int64  `json:"revision"`
			ItemCount int64  `json:"item_count"`
		}{row.Kind, row.ID, row.PublicID, row.Owner, row.Revision, row.ItemCount})
	case "pack_item":
		return json.Marshal(struct {
			Kind  string `json:"kind"`
			ID    int64  `json:"id"`
			Pack  int64  `json:"pack"`
			Skill int64  `json:"skill"`
			Slug  string `json:"slug"`
		}{row.Kind, row.ID, row.Pack, row.Skill, row.Slug})
	default:
		return nil, ErrLocalSnapshotInvalid
	}
}

func snapshotJSON(data []byte, value any) error {
	if len(data) == 0 || len(data) > 65536 || data[len(data)-1] != '\n' || bytes.Count(data, []byte{'\n'}) != 1 {
		return ErrLocalSnapshotInvalid
	}
	if json.Unmarshal(data[:len(data)-1], value, json.RejectUnknownMembers(true)) != nil {
		return ErrLocalSnapshotInvalid
	}
	return nil
}
