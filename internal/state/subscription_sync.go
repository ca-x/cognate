package state

import (
	"context"
	"encoding/hex"
	"fmt"
	"math"
	"strings"

	"github.com/ca-x/skillhub/internal/state/ent"
	"github.com/ca-x/skillhub/internal/state/ent/installedskill"
	"github.com/ca-x/skillhub/internal/state/ent/subscription"
	"github.com/ca-x/skillhub/internal/state/ent/subscriptionskill"
)

func (store *Store) StageSubscriptionSync(ctx context.Context, snapshots []SubscriptionSnapshot) error {
	if len(snapshots) == 0 || len(snapshots) > MaxSubscriptions {
		return ErrSubscriptionConflict
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := tx.Subscription.Query().WithSkills().All(ctx)
	if err != nil {
		return err
	}
	if err := validateSubscriptionSnapshots(entries, snapshots); err != nil {
		return err
	}
	byID := make(map[string]*ent.Subscription, len(entries))
	for _, entry := range entries {
		byID[entry.SubscriptionKey] = entry
	}
	for _, snapshot := range snapshots {
		entry := byID[snapshot.ID]
		update := tx.Subscription.Update().Where(subscription.IDEQ(entry.ID), subscription.VersionEQ(snapshot.ExpectedVersion)).SetDesiredRevision(snapshot.Revision).SetDesiredManifestHash(snapshot.ManifestHash).AddVersion(1)
		if entry.Enabled {
			update.SetName(snapshot.Name)
		}
		count, err := update.Save(ctx)
		if err != nil {
			return fmt.Errorf("stage subscription state")
		}
		if count != 1 {
			return ErrSubscriptionConflict
		}
		if err := stageSubscriptionSkills(ctx, tx, entry, snapshot.Skills); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit desired subscriptions")
	}
	return nil
}

func validateSubscriptionSnapshots(entries []*ent.Subscription, snapshots []SubscriptionSnapshot) error {
	if len(entries) != len(snapshots) {
		return ErrSubscriptionConflict
	}
	byID := make(map[string]*ent.Subscription, len(entries))
	for _, entry := range entries {
		byID[entry.SubscriptionKey] = entry
	}
	union := make(map[string]string)
	for _, snapshot := range snapshots {
		entry := byID[snapshot.ID]
		if entry == nil || snapshot.ExpectedVersion != entry.Version || entry.Version == math.MaxInt64 {
			return ErrSubscriptionConflict
		}
		delete(byID, snapshot.ID)
		if !entry.Enabled {
			if snapshot.Revision != 0 || snapshot.ManifestHash != "" || len(snapshot.Skills) != 0 {
				return ErrSubscriptionConflict
			}
			continue
		}
		if !validSubscriptionName(snapshot.Name) || snapshot.Revision < max(1, entry.AppliedRevision, entry.DesiredRevision) || !validSubscriptionHash(snapshot.ManifestHash) || len(snapshot.Skills) == 0 || len(snapshot.Skills) > 256 {
			return fmt.Errorf("invalid subscription snapshot")
		}
		if snapshot.Revision == entry.AppliedRevision && snapshot.ManifestHash != entry.AppliedManifestHash || snapshot.Revision == entry.DesiredRevision && snapshot.ManifestHash != entry.DesiredManifestHash {
			return ErrSubscriptionConflict
		}
		seen := make(map[string]bool, len(snapshot.Skills))
		for _, item := range snapshot.Skills {
			if ValidateSlug(item.Slug) != nil || !validSubscriptionHash(item.DesiredHash) || item.AppliedHash != "" || seen[item.Slug] {
				return fmt.Errorf("invalid subscription skill")
			}
			seen[item.Slug] = true
			if prior, exists := union[item.Slug]; exists && prior != item.DesiredHash {
				return ErrSubscriptionConflict
			}
			union[item.Slug] = item.DesiredHash
			if len(union) > MaxSubscriptionSkills {
				return fmt.Errorf("subscription skill limit reached")
			}
		}
	}
	return nil
}

func stageSubscriptionSkills(ctx context.Context, tx *ent.Tx, entry *ent.Subscription, skills []SubscriptionSkill) error {
	if _, err := tx.SubscriptionSkill.Update().Where(subscriptionskill.SubscriptionIDEQ(entry.ID)).SetDesiredHash("").Save(ctx); err != nil {
		return err
	}
	existing := make(map[string]int, len(entry.Edges.Skills))
	for _, item := range entry.Edges.Skills {
		existing[item.Slug] = item.ID
	}
	for _, item := range skills {
		if id, exists := existing[item.Slug]; exists {
			if err := tx.SubscriptionSkill.UpdateOneID(id).SetDesiredHash(item.DesiredHash).Exec(ctx); err != nil {
				return err
			}
		} else if err := tx.SubscriptionSkill.Create().SetSubscriptionID(entry.ID).SetSlug(item.Slug).SetDesiredHash(item.DesiredHash).Exec(ctx); err != nil {
			return err
		}
	}
	_, err := tx.SubscriptionSkill.Delete().Where(subscriptionskill.SubscriptionIDEQ(entry.ID), subscriptionskill.DesiredHashEQ(""), subscriptionskill.AppliedHashEQ("")).Exec(ctx)
	return err
}

func (store *Store) CompleteSubscriptionSync(ctx context.Context, versions map[string]int64) error {
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	entries, err := tx.Subscription.Query().WithSkills().All(ctx)
	if err != nil {
		return err
	}
	if len(entries) == 0 || len(entries) != len(versions) {
		return ErrSubscriptionConflict
	}
	if err := verifySubscriptionCompletion(ctx, tx, entries, versions); err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.Enabled {
			if _, err := tx.SubscriptionSkill.Delete().Where(subscriptionskill.SubscriptionIDEQ(entry.ID)).Exec(ctx); err != nil {
				return err
			}
			if err := tx.Subscription.DeleteOneID(entry.ID).Exec(ctx); err != nil {
				return err
			}
			continue
		}
		count, err := tx.Subscription.Update().Where(subscription.IDEQ(entry.ID), subscription.VersionEQ(entry.Version)).SetAppliedRevision(entry.DesiredRevision).SetAppliedManifestHash(entry.DesiredManifestHash).AddVersion(1).Save(ctx)
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrSubscriptionConflict
		}
		for _, item := range entry.Edges.Skills {
			if item.DesiredHash == "" {
				if err := tx.SubscriptionSkill.DeleteOneID(item.ID).Exec(ctx); err != nil {
					return err
				}
			} else if err := tx.SubscriptionSkill.UpdateOneID(item.ID).SetAppliedHash(item.DesiredHash).Exec(ctx); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func verifySubscriptionCompletion(ctx context.Context, tx *ent.Tx, entries []*ent.Subscription, versions map[string]int64) error {
	desired := make(map[string]string)
	for _, entry := range entries {
		if versions[entry.SubscriptionKey] != entry.Version || entry.Version == math.MaxInt64 || entry.Enabled && entry.DesiredRevision == 0 {
			return ErrSubscriptionConflict
		}
		for _, item := range entry.Edges.Skills {
			if item.DesiredHash != "" {
				if hash, exists := desired[item.Slug]; exists && hash != item.DesiredHash {
					return ErrSubscriptionConflict
				}
				desired[item.Slug] = item.DesiredHash
			}
		}
	}
	installed, err := tx.InstalledSkill.Query().All(ctx)
	if err != nil {
		return err
	}
	for _, entry := range installed {
		hash, needed := desired[entry.Slug]
		if needed && hash != entry.ContentHash || !needed && entry.SubscriptionManaged {
			return fmt.Errorf("subscription installations do not match desired state")
		}
		delete(desired, entry.Slug)
	}
	if len(desired) != 0 {
		return fmt.Errorf("subscription installations are incomplete")
	}
	return nil
}

func validSubscriptionHash(value string) bool {
	if len(value) != 64 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func requireDesiredSubscriptionSkill(ctx context.Context, tx *ent.Tx, slug, hash string) error {
	exists, err := tx.SubscriptionSkill.Query().Where(subscriptionskill.SlugEQ(slug), subscriptionskill.DesiredHashEQ(hash), subscriptionskill.HasSubscriptionWith(subscription.EnabledEQ(true))).Exist(ctx)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("installation no longer belongs to a desired subscription")
	}
	return nil
}

func (store *Store) SubscriptionInstallations(ctx context.Context) ([]Installation, error) {
	entries, err := store.client.InstalledSkill.Query().Where(installedskill.SubscriptionManagedEQ(true)).WithProjections().Order(ent.Asc(installedskill.FieldSlug)).All(ctx)
	if err != nil {
		return nil, err
	}
	results := make([]Installation, 0, len(entries))
	for _, entry := range entries {
		results = append(results, installationRecord(entry))
	}
	return results, nil
}
