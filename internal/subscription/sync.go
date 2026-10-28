package subscription

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"slices"

	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/internal/packclient"
	"github.com/ca-x/cognate/internal/state"
)

type SyncResult struct {
	Subscriptions int
	Installed     int
	Updated       int
	Reused        int
	Removed       int
	Backups       []string
}

type desiredSkill struct {
	client   *packclient.Client
	manifest *packv1.GetManifestResponse
	item     *packv1.PackSkill
	existing state.Installation
	present  bool
}

func (manager *Manager) Sync(ctx context.Context) (result SyncResult, resultErr error) {
	manager.mutex.Lock()
	defer manager.mutex.Unlock()
	ctx, done, err := manager.requestContext(ctx)
	if err != nil {
		return result, err
	}
	defer done()
	pending, err := manager.install.PendingOperations(ctx)
	if err != nil {
		return result, err
	}
	if len(pending) != 0 {
		return result, fmt.Errorf("pending installation operations require recovery before subscription sync")
	}
	records, err := manager.store.Subscriptions(ctx)
	if err != nil || len(records) == 0 {
		return result, err
	}
	snapshots, desired, err := manager.desiredSubscriptions(ctx, records)
	if err != nil {
		return result, err
	}
	for _, record := range records {
		if record.Enabled {
			result.Subscriptions++
		}
	}
	orphans, err := manager.preflight(ctx, desired)
	if err != nil {
		return result, err
	}
	if err := manager.store.StageSubscriptionSync(ctx, snapshots); err != nil {
		return result, err
	}
	versions := make(map[string]int64, len(snapshots))
	for _, snapshot := range snapshots {
		versions[snapshot.ID] = snapshot.ExpectedVersion + 1
	}
	for _, slug := range sortedDesired(desired) {
		if err := manager.applySkill(ctx, desired[slug], &result); err != nil {
			return result, err
		}
	}
	for _, record := range orphans {
		backup, err := manager.install.UninstallLocal(ctx, record.Slug)
		if backup.ID != "" {
			result.Backups = append(result.Backups, backup.ID)
		}
		if err != nil {
			return result, err
		}
		result.Removed++
	}
	for _, slug := range sortedDesired(desired) {
		if err := manager.install.VerifyLocal(ctx, manager.home, slug); err != nil {
			return result, err
		}
	}
	return result, manager.store.CompleteSubscriptionSync(ctx, versions)
}

func (manager *Manager) desiredSubscriptions(ctx context.Context, records []state.Subscription) ([]state.SubscriptionSnapshot, map[string]*desiredSkill, error) {
	snapshots := make([]state.SubscriptionSnapshot, 0, len(records))
	desired := make(map[string]*desiredSkill)
	for _, record := range records {
		snapshot := state.SubscriptionSnapshot{ID: record.ID, ExpectedVersion: record.Version}
		if !record.Enabled {
			snapshots = append(snapshots, snapshot)
			continue
		}
		client, err := manager.subscriptionClient(ctx, record)
		if err != nil {
			return nil, nil, err
		}
		manifest, err := client.Manifest(ctx)
		if err != nil {
			return nil, nil, err
		}
		if manifest.PackId != record.PackID {
			return nil, nil, fmt.Errorf("subscription credential refers to a different pack")
		}
		snapshot.Name, snapshot.Revision, snapshot.ManifestHash = manifest.Name, manifest.Revision, manifest.ManifestHash
		for _, item := range manifest.Skills {
			if previous, exists := desired[item.Slug]; exists && previous.item.ContentHash != item.ContentHash {
				return nil, nil, state.ErrSubscriptionConflict
			}
			snapshot.Skills = append(snapshot.Skills, state.SubscriptionSkill{Slug: item.Slug, DesiredHash: item.ContentHash})
			if _, exists := desired[item.Slug]; !exists {
				desired[item.Slug] = &desiredSkill{client: client, manifest: manifest, item: item}
			}
			if len(desired) > state.MaxSubscriptionSkills {
				return nil, nil, fmt.Errorf("subscription skill limit reached")
			}
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, desired, nil
}

func (manager *Manager) subscriptionClient(ctx context.Context, record state.Subscription) (*packclient.Client, error) {
	var client *packclient.Client
	err := manager.store.WithSubscriptionCredential(ctx, record.ID, func(sealed []byte) error {
		plaintext, err := manager.openCredential(record, sealed)
		if err != nil {
			return err
		}
		defer clear(plaintext)
		client, err = packclient.New(record.Server, string(plaintext), manager.client)
		return err
	})
	return client, err
}

func (manager *Manager) preflight(ctx context.Context, desired map[string]*desiredSkill) ([]state.Installation, error) {
	for _, slug := range sortedDesired(desired) {
		selected := desired[slug]
		record, present, err := manager.install.PreflightSubscription(ctx, manager.home, slug, selected.item.ContentHash)
		if err != nil {
			return nil, err
		}
		selected.existing, selected.present = record, present
	}
	managed, err := manager.store.SubscriptionInstallations(ctx)
	if err != nil {
		return nil, err
	}
	var orphans []state.Installation
	for _, record := range managed {
		if _, needed := desired[record.Slug]; needed {
			continue
		}
		if err := manager.install.VerifyLocal(ctx, manager.home, record.Slug); err != nil {
			return nil, err
		}
		orphans = append(orphans, record)
	}
	return orphans, nil
}

func (manager *Manager) applySkill(ctx context.Context, selected *desiredSkill, result *SyncResult) (resultErr error) {
	if selected.present && selected.existing.ContentHash == selected.item.ContentHash {
		result.Reused++
		return nil
	}
	snapshot, err := selected.client.Download(ctx, selected.manifest, selected.item)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	if selected.present {
		backup, err := manager.install.UninstallLocal(ctx, selected.item.Slug)
		if backup.ID != "" {
			result.Backups = append(result.Backups, backup.ID)
		}
		if err != nil {
			return err
		}
	}
	matched, err := manager.install.InstallSubscription(ctx, manager.home, selected.item.Slug, snapshot.Path(), selected.item.ContentHash)
	if err != nil {
		return err
	}
	if matched {
		result.Reused++
	} else if selected.present {
		result.Updated++
	} else {
		result.Installed++
	}
	return nil
}

func sortedDesired(desired map[string]*desiredSkill) []string {
	return slices.Sorted(maps.Keys(desired))
}
