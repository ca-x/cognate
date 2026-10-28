package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/netip"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ca-x/skillhub/internal/state/ent"
	"github.com/ca-x/skillhub/internal/state/ent/subscription"
	"github.com/ca-x/skillhub/internal/state/ent/subscriptionskill"
)

const MaxSubscriptions = 32
const MaxSubscriptionSkills = 1024

var ErrSubscriptionConflict = errors.New("subscription state changed or has conflicting skills")

type Subscription struct {
	ID                  string
	Server              string
	PackID              string
	Name                string
	Version             int64
	Enabled             bool
	DesiredRevision     int64
	DesiredManifestHash string
	AppliedRevision     int64
	AppliedManifestHash string
	Skills              []SubscriptionSkill
}

type SubscriptionSkill struct {
	Slug        string
	DesiredHash string
	AppliedHash string
}

type SubscriptionDraft struct {
	ID               string
	Server           string
	PackID           string
	Name             string
	SealedCredential []byte
}

type SubscriptionSnapshot struct {
	ID              string
	ExpectedVersion int64
	Name            string
	Revision        int64
	ManifestHash    string
	Skills          []SubscriptionSkill
}

func (store *Store) CreateSubscription(ctx context.Context, draft SubscriptionDraft) (Subscription, error) {
	if !validSubscriptionKey(draft.ID) || !validSubscriptionKey(draft.PackID) || !validSubscriptionName(draft.Name) || !validSubscriptionServer(draft.Server) || !validSealedCredential(draft.SealedCredential) {
		return Subscription{}, fmt.Errorf("invalid subscription metadata")
	}
	tx, err := store.client.Tx(ctx)
	if err != nil {
		return Subscription{}, err
	}
	defer tx.Rollback()
	entry, err := tx.Subscription.Create().SetSubscriptionKey(draft.ID).SetServer(draft.Server).SetPackID(draft.PackID).SetName(draft.Name).SetSealedCredential(draft.SealedCredential).Save(ctx)
	if ent.IsConstraintError(err) {
		return Subscription{}, ErrSubscriptionConflict
	}
	if err != nil {
		return Subscription{}, fmt.Errorf("create subscription")
	}
	count, err := tx.Subscription.Query().Count(ctx)
	if err != nil {
		return Subscription{}, err
	}
	if count > MaxSubscriptions {
		return Subscription{}, fmt.Errorf("subscription limit reached")
	}
	if err := tx.Commit(); err != nil {
		return Subscription{}, fmt.Errorf("commit subscription")
	}
	return subscriptionRecord(entry), nil
}

func (store *Store) Subscriptions(ctx context.Context) ([]Subscription, error) {
	entries, err := store.client.Subscription.Query().Order(ent.Asc(subscription.FieldSubscriptionKey)).WithSkills(func(query *ent.SubscriptionSkillQuery) {
		query.Order(ent.Asc(subscriptionskill.FieldSlug))
	}).All(ctx)
	if err != nil {
		return nil, fmt.Errorf("read subscriptions")
	}
	results := make([]Subscription, 0, len(entries))
	for _, entry := range entries {
		results = append(results, subscriptionRecord(entry))
	}
	return results, nil
}

func subscriptionRecord(entry *ent.Subscription) Subscription {
	record := Subscription{ID: entry.SubscriptionKey, Server: entry.Server, PackID: entry.PackID, Name: entry.Name, Version: entry.Version, Enabled: entry.Enabled, DesiredRevision: entry.DesiredRevision, DesiredManifestHash: entry.DesiredManifestHash, AppliedRevision: entry.AppliedRevision, AppliedManifestHash: entry.AppliedManifestHash}
	for _, item := range entry.Edges.Skills {
		record.Skills = append(record.Skills, SubscriptionSkill{Slug: item.Slug, DesiredHash: item.DesiredHash, AppliedHash: item.AppliedHash})
	}
	return record
}

func (store *Store) WithSubscriptionCredential(ctx context.Context, id string, consume func([]byte) error) error {
	if consume == nil || !validSubscriptionKey(id) {
		return fmt.Errorf("invalid subscription credential request")
	}
	entry, err := store.client.Subscription.Query().Where(subscription.SubscriptionKeyEQ(id)).Only(ctx)
	if err != nil {
		return fmt.Errorf("subscription credential unavailable")
	}
	defer clear(entry.SealedCredential)
	return consume(entry.SealedCredential)
}

func (store *Store) RotateSubscriptionCredential(ctx context.Context, id string, version int64, sealed []byte) error {
	if !validSubscriptionKey(id) || version <= 0 || version == math.MaxInt64 || !validSealedCredential(sealed) {
		return fmt.Errorf("invalid subscription credential update")
	}
	count, err := store.client.Subscription.Update().Where(subscription.SubscriptionKeyEQ(id), subscription.VersionEQ(version)).SetSealedCredential(sealed).SetEnabled(true).AddVersion(1).Save(ctx)
	if err != nil {
		return fmt.Errorf("update subscription credential")
	}
	if count != 1 {
		return ErrSubscriptionConflict
	}
	return nil
}

func (store *Store) DisableSubscription(ctx context.Context, id string, version int64) error {
	if !validSubscriptionKey(id) || version <= 0 || version == math.MaxInt64 {
		return ErrSubscriptionConflict
	}
	count, err := store.client.Subscription.Update().Where(subscription.SubscriptionKeyEQ(id), subscription.VersionEQ(version)).SetEnabled(false).AddVersion(1).Save(ctx)
	if err != nil {
		return fmt.Errorf("disable subscription")
	}
	if count != 1 {
		return ErrSubscriptionConflict
	}
	return nil
}

func validSubscriptionKey(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	return !strings.ContainsFunc(value, func(character rune) bool {
		return !(character >= '0' && character <= '9' || character >= 'A' && character <= 'Z' || character >= 'a' && character <= 'z' || character == '-' || character == '_')
	})
}

func validSubscriptionName(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256 && utf8.ValidString(value) && !strings.ContainsFunc(value, func(character rune) bool {
		return unicode.IsControl(character) || unicode.Is(unicode.Bidi_Control, character)
	})
}

func validSealedCredential(value []byte) bool {
	return len(value) >= 29 && len(value) <= 2048
}

func validSubscriptionServer(value string) bool {
	origin, err := url.Parse(value)
	if err != nil || origin.Host == "" || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.ForceQuery || origin.Fragment != "" || origin.Opaque != "" || origin.String() != value {
		return false
	}
	if origin.Scheme == "https" {
		return true
	}
	address, err := netip.ParseAddr(origin.Hostname())
	return origin.Scheme == "http" && (origin.Hostname() == "localhost" || err == nil && address.IsLoopback())
}
