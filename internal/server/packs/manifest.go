package packs

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
	"slices"

	"github.com/ca-x/cognate/internal/server/state"
)

type Manifest struct {
	PackID      string
	Name        string
	Description string
	Revision    int64
	Hash        string
	Skills      []state.PackSkill
}

func (service *Service) Manifest(ctx context.Context, raw string) (Manifest, error) {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return Manifest{}, err
	}
	defer done()
	credential, err := service.parse(raw)
	if err != nil {
		return Manifest{}, err
	}
	pack, err := service.store.SharedPack(ctx, credential.id, credential.digest[:])
	if err != nil {
		return Manifest{}, err
	}
	items := slices.Clone(pack.Items)
	slices.SortFunc(items, func(first, second state.PackSkill) int {
		return cmp.Or(cmp.Compare(first.Slug, second.Slug), cmp.Compare(first.ContentHash, second.ContentHash), cmp.Compare(first.SkillID, second.SkillID))
	})
	items = slices.CompactFunc(items, func(first, second state.PackSkill) bool {
		return first.Slug == second.Slug && first.ContentHash == second.ContentHash
	})
	manifest := Manifest{PackID: pack.ID, Name: pack.Name, Description: pack.Description, Revision: pack.Revision, Skills: items}
	digest := sha256.New()
	manifestStrings(digest, "skillhub/pack-manifest/v1", pack.ID, pack.Name, pack.Description)
	_, _ = digest.Write(binary.BigEndian.AppendUint64(nil, uint64(pack.Revision)))
	for _, item := range items {
		manifestStrings(digest, item.SkillID, item.Slug, item.Name, item.Description, item.ContentHash, item.RepositoryID, item.RepositoryName, item.Path)
	}
	manifest.Hash = hex.EncodeToString(digest.Sum(nil))
	return manifest, nil
}

func manifestStrings(destination hash.Hash, values ...string) {
	for _, value := range values {
		_, _ = destination.Write(binary.BigEndian.AppendUint64(nil, uint64(len(value))))
		_, _ = destination.Write([]byte(value))
	}
}
