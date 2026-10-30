package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json/v2"
	"errors"
	"fmt"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/ent"
	"github.com/ca-x/cognate/internal/server/ent/importreceipt"
	"github.com/ca-x/cognate/internal/skill"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

type importTicketFixture struct {
	store      *Store
	credential ImportOwnerCredential
	digest     [32]byte
	now        time.Time
	path       string
	key        []byte
}

func TestImportReceiptCheckpointPartialAndTicketReuse(test *testing.T) {
	fixture := newImportTicketFixture(test)
	origin := "https://cognate.invalid"
	raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, origin, raw)
	if err != nil {
		test.Fatal(err)
	}
	plan := ImportSelectionPlan{RepositoryIDs: []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}, ConfigurationCount: 1}
	reserved, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, origin, ticket, [16]byte{1}, sha256.Sum256([]byte("request")), plan)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, origin, ticket, [16]byte{2}, sha256.Sum256([]byte("new request")), plan); !errors.Is(err, ErrImportConflict) {
		test.Fatal("one ticket granted two executions")
	}
	outcomes := slices.Clone(reserved.Receipt.RepositoryOutcomes)
	outcomes[0].Code, outcomes[0].RepositoryID = 1, "ABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if _, err := fixture.store.CheckpointImportReceipt(test.Context(), fixture.credential, reserved.Execution, outcomes); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("unowned repository success persisted")
	}
	reference, err := repository.ParseReference("example/partial", "HEAD")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := fixture.store.CollectRepository(test.Context(), fixture.credential.Owner(), reference, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Partial", Description: "Synthetic", Path: "."}, ContentHash: strings.Repeat("b", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	outcomes[0].RepositoryID = collected.ID
	checkpoint, err := fixture.store.CheckpointImportReceipt(test.Context(), fixture.credential, reserved.Execution, outcomes)
	if err != nil || checkpoint.Receipt.Revision != 2 || checkpoint.Receipt.Phase != ImportCollecting {
		test.Fatal("checkpoint not persisted")
	}
	if _, err := fixture.store.CheckpointImportReceipt(test.Context(), fixture.credential, reserved.Execution, outcomes); !errors.Is(err, ErrImportConflict) {
		test.Fatal("stale checkpoint accepted")
	}
	outcomes[1].Code = 4
	partial, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, checkpoint.Execution, ImportFinishSelection{Phase: ImportPartial, RepositoryOutcomes: outcomes})
	if err != nil || partial.Phase != ImportPartial || partial.Revision != 3 || len(partial.CreatedAssets) != 0 {
		test.Fatal("partial repository effects misreported")
	}
	if count, err := fixture.store.state.client.ImportReceipt.Query().Count(test.Context()); err != nil || count != 1 {
		test.Fatal("ticket reuse persisted another receipt")
	}
}

func TestImportReceiptAADAndQuota(test *testing.T) {
	fixture := newImportTicketFixture(test)
	reserved := reserveImportForTest(test, fixture, ImportSelectionPlan{ConfigurationCount: 1})
	row, err := fixture.store.state.client.ImportReceipt.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	for _, mutate := range []func(*ent.ImportReceipt){
		func(row *ent.ImportReceipt) { row.OperationID = bytes.Repeat([]byte{7}, 16) },
		func(row *ent.ImportReceipt) { row.RequestDigest = bytes.Repeat([]byte{7}, 32) },
		func(row *ent.ImportReceipt) { row.OriginDigest = bytes.Repeat([]byte{7}, 32) },
		func(row *ent.ImportReceipt) { row.Revision++ },
		func(row *ent.ImportReceipt) { row.Phase = importreceipt.PhaseCollecting },
	} {
		altered := *row
		mutate(&altered)
		if _, err := fixture.store.importReadRecord(fixture.credential.Owner(), &altered); !errors.Is(err, ErrImportUnavailable) {
			test.Fatal("receipt AAD substitution accepted")
		}
	}
	for index := 2; index <= 128; index++ {
		raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
		if err != nil {
			test.Fatal(err)
		}
		ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw)
		if err != nil {
			test.Fatal(err)
		}
		operation := [16]byte{byte(index)}
		if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, operation, sha256.Sum256(operation[:]), ImportSelectionPlan{ConfigurationCount: 1}); err != nil {
			test.Fatal(err)
		}
	}
	raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, [16]byte{129}, sha256.Sum256([]byte("over quota")), ImportSelectionPlan{ConfigurationCount: 1}); !errors.Is(err, ErrImportQuota) {
		test.Fatal("owner receipt budget exceeded")
	}
	view, found, err := fixture.store.ReadImportReceipt(test.Context(), fixture.credential, reserved.Receipt.OperationID)
	if err != nil || !found || view.Revision != 1 {
		test.Fatal("quota made existing status inaccessible")
	}
}

func TestImportReceiptCommitUncertaintyDoesNotReplay(test *testing.T) {
	fixture := newImportTicketFixture(test)
	commits := 0
	fixture.store.state.client.ImportReceipt.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			transaction, err := mutation.(*ent.ImportReceiptMutation).Tx()
			if err != nil {
				return nil, err
			}
			transaction.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
					commits++
					if err := next.Commit(ctx, transaction); err != nil {
						return err
					}
					return errors.New("synthetic uncertain commit")
				})
			})
			return next.Mutate(ctx, mutation)
		})
	})
	raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw)
	if err != nil {
		test.Fatal(err)
	}
	digest := sha256.Sum256([]byte("uncertain request"))
	plan := ImportSelectionPlan{ConfigurationCount: 1}
	if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, [16]byte{1}, digest, plan); !errors.Is(err, ErrImportOutcomeUnknown) {
		test.Fatal("uncertain commit not classified")
	}
	if commits != 1 {
		test.Fatal("uncertain commit automatically retried")
	}
	observed, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, [16]byte{1}, digest, plan)
	if err != nil || observed.Execution.Granted() || observed.Receipt.Revision != 1 || commits != 1 {
		test.Fatal("lost response replayed execution")
	}
}

func TestImportReceiptSessionTransactionFence(test *testing.T) {
	for _, stage := range []string{"reserve-before", "reserve-during", "final-before", "final-during", "final-expiry"} {
		test.Run(stage, func(test *testing.T) {
			fixture := newImportTicketFixture(test)
			peer, err := Open(test.Context(), fixture.path, fixture.key)
			if err != nil {
				test.Fatal(err)
			}
			defer peer.Close()
			plan := ImportSelectionPlan{ConfigurationCount: 1}
			if strings.HasPrefix(stage, "reserve") {
				raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
				if err != nil {
					test.Fatal(err)
				}
				ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw)
				if err != nil {
					test.Fatal(err)
				}
				if stage == "reserve-before" {
					if err := peer.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
						test.Fatal(err)
					}
				} else {
					fixture.store.state.client.ImportReceipt.Use(func(next ent.Mutator) ent.Mutator {
						return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
							if err := peer.RevokeSession(ctx, fixture.digest[:]); err != nil {
								return nil, err
							}
							return next.Mutate(ctx, mutation)
						})
					})
				}
				if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, [16]byte{1}, sha256.Sum256([]byte("request")), plan); err == nil {
					test.Fatal("logout completed before reservation write but write accepted")
				}
				if count, err := fixture.store.state.client.ImportReceipt.Query().Count(test.Context()); err != nil || count != 0 {
					test.Fatal("revoked reservation left receipt")
				}
				return
			}
			reserved := reserveImportForTest(test, fixture, plan)
			switch stage {
			case "final-before":
				if err := peer.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
					test.Fatal(err)
				}
			case "final-during":
				fixture.store.state.client.ConfigurationAsset.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						if err := peer.RevokeSession(ctx, fixture.digest[:]); err != nil {
							return nil, err
						}
						return next.Mutate(ctx, mutation)
					})
				})
			case "final-expiry":
				fixture.store.state.client.ImportReceipt.Use(func(next ent.Mutator) ent.Mutator {
					return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
						value, err := next.Mutate(ctx, mutation)
						fixture.now = fixture.now.Add(2 * time.Hour)
						return value, err
					})
				})
			}
			draft := ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "Fenced", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic"))}
			if _, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, ImportFinishSelection{Phase: ImportCompleted, Drafts: []ConfigurationAssetDraft{draft}}); err == nil {
				test.Fatal("revoked or expired final transaction accepted")
			}
			if count, err := fixture.store.state.client.ConfigurationAsset.Query().Count(test.Context()); err != nil || count != 0 {
				test.Fatal("unauthorized final transaction left asset")
			}
			row, err := fixture.store.state.client.ImportReceipt.Query().Only(test.Context())
			if err != nil || row.Revision != 1 || row.Phase != importreceipt.PhaseReserved {
				test.Fatal("unauthorized final transaction changed receipt")
			}
		})
	}
}

func TestImportReceiptGlobalBudget(test *testing.T) {
	fixture := newImportTicketFixture(test)
	originDigest := sha256.Sum256([]byte("https://cognate.invalid"))
	requestDigest := sha256.Sum256([]byte("synthetic seeded receipt"))
	for ownerIndex := range 32 {
		owner := fmt.Sprintf("quota-owner-%d", ownerIndex)
		if err := fixture.store.CreateUser(test.Context(), owner); err != nil {
			test.Fatal(err)
		}
		account, err := findUser(test.Context(), fixture.store.state.client, owner)
		if err != nil {
			test.Fatal(err)
		}
		builders := make([]*ent.ImportReceiptCreate, 0, 128)
		for number := 1; number <= 128; number++ {
			operation := [16]byte{byte(number)}
			row := &ent.ImportReceipt{UserID: account.ID, OperationID: operation[:], TicketID: operation[:], RequestDigest: requestDigest[:], OriginDigest: originDigest[:], Revision: 1, Phase: importreceipt.PhaseReserved, ExecutionID: bytes.Repeat([]byte{7}, 16), CreatedAt: fixture.now, UpdatedAt: fixture.now, Charge: 131072}
			record := importReceiptRecord{Version: 1, Plan: ImportSelectionPlan{ConfigurationCount: 1}, View: ImportReceiptView{OperationID: operation, Revision: 1, Phase: ImportReserved}}
			ciphertext, err := fixture.store.importSealRecord(owner, row, record)
			if err != nil {
				test.Fatal(err)
			}
			builders = append(builders, fixture.store.state.client.ImportReceipt.Create().SetUserID(row.UserID).SetOperationID(row.OperationID).SetTicketID(row.TicketID).SetRequestDigest(row.RequestDigest).SetOriginDigest(row.OriginDigest).SetRevision(1).SetPhase(row.Phase).SetExecutionID(row.ExecutionID).SetCreatedAt(row.CreatedAt).SetUpdatedAt(row.UpdatedAt).SetCiphertext(ciphertext).SetCharge(row.Charge))
		}
		if err := fixture.store.state.client.ImportReceipt.CreateBulk(builders...).Exec(test.Context()); err != nil {
			test.Fatal(err)
		}
	}
	if count, err := fixture.store.state.client.ImportReceipt.Query().Count(test.Context()); err != nil || count != 4096 {
		test.Fatal("global budget fixture count invalid")
	}
	raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ticket, [16]byte{1}, sha256.Sum256([]byte("request")), ImportSelectionPlan{ConfigurationCount: 1}); !errors.Is(err, ErrImportQuota) {
		test.Fatal("global receipt budget exceeded")
	}
}

func TestImportReceiptFinalUnknownReopenAndOwnerIsolation(test *testing.T) {
	fixture := newImportTicketFixture(test)
	reserved := reserveImportForTest(test, fixture, ImportSelectionPlan{ConfigurationCount: 1})
	bobDigest := sha256.Sum256([]byte("synthetic bob session"))
	if _, err := fixture.store.CreateOIDCSession(test.Context(), OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "bob", TokenHash: bobDigest[:], IssuedAt: fixture.now.Add(-time.Minute), ExpiresAt: fixture.now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	bob, err := fixture.store.ImportCredentialForSession(test.Context(), bobDigest)
	if err != nil {
		test.Fatal(err)
	}
	if _, found, err := fixture.store.ReadImportReceipt(test.Context(), bob, [16]byte{1}); err != nil || found {
		test.Fatal("foreign receipt was visible")
	}
	draft := ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "Uncertain final", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic secret-free envelope"))}
	finish := ImportFinishSelection{Phase: ImportCompleted, Drafts: []ConfigurationAssetDraft{draft}}
	if _, err := fixture.store.FinishImportSelection(test.Context(), bob, reserved.Execution, finish); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("foreign owner used execution capability")
	}
	commits := 0
	fixture.store.state.client.ImportReceipt.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			transaction, err := mutation.(*ent.ImportReceiptMutation).Tx()
			if err != nil {
				return nil, err
			}
			transaction.OnCommit(func(next ent.Committer) ent.Committer {
				return ent.CommitFunc(func(ctx context.Context, transaction *ent.Tx) error {
					commits++
					if err := next.Commit(ctx, transaction); err != nil {
						return err
					}
					return errors.New("synthetic final response lost")
				})
			})
			return next.Mutate(ctx, mutation)
		})
	})
	if _, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, finish); !errors.Is(err, ErrImportOutcomeUnknown) {
		test.Fatal("uncertain final commit falsely reported a known result")
	}
	if commits != 1 {
		test.Fatal("uncertain final commit retried")
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = reopened
	reopened.state.now = func() time.Time { return fixture.now }
	view, found, err := reopened.ReadImportReceipt(test.Context(), fixture.credential, [16]byte{1})
	if err != nil || !found || view.Phase != ImportCompleted || view.Revision != 2 || len(view.CreatedAssets) != 1 {
		test.Fatal("restart lost known final commit")
	}
	if count, err := reopened.state.client.ConfigurationAsset.Query().Count(test.Context()); err != nil || count != 1 {
		test.Fatal("uncertain final assets not atomic")
	}
	observed, err := reopened.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ImportTicket{}, [16]byte{1}, sha256.Sum256([]byte("request")), ImportSelectionPlan{ConfigurationCount: 1})
	if err != nil || observed.Execution.Granted() || observed.Receipt.Phase != ImportCompleted {
		test.Fatal("restart resumed an already committed operation")
	}
}

func TestImportReceiptRollbackErrorIsUnknown(test *testing.T) {
	fixture := newImportTicketFixture(test)
	reserved := reserveImportForTest(test, fixture, ImportSelectionPlan{ConfigurationCount: 2})
	rollbacks := 0
	fixture.store.state.client.ConfigurationAsset.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, mutation ent.Mutation) (ent.Value, error) {
			transaction, err := mutation.(*ent.ConfigurationAssetMutation).Tx()
			if err != nil {
				return nil, err
			}
			transaction.OnRollback(func(next ent.Rollbacker) ent.Rollbacker {
				return ent.RollbackFunc(func(ctx context.Context, transaction *ent.Tx) error {
					rollbacks++
					if err := next.Rollback(ctx, transaction); err != nil {
						return err
					}
					return errors.New("synthetic rollback uncertainty")
				})
			})
			return next.Mutate(ctx, mutation)
		})
	})
	draft := ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "Duplicate", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic"))}
	_, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, ImportFinishSelection{Phase: ImportCompleted, Drafts: []ConfigurationAssetDraft{draft, draft}})
	if !errors.Is(err, ErrImportOutcomeUnknown) || rollbacks != 1 {
		test.Fatal("rollback error was hidden behind a definite failure")
	}
}

func reserveImportForTest(test *testing.T, fixture *importTicketFixture, plan ImportSelectionPlan) ImportReceiptReservation {
	test.Helper()
	origin := "https://cognate.invalid"
	raw, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, origin, raw)
	if err != nil {
		test.Fatal(err)
	}
	reserved, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, origin, ticket, [16]byte{1}, sha256.Sum256([]byte("request")), plan)
	if err != nil || !reserved.Execution.Granted() || reserved.Receipt.Revision != 1 || reserved.Receipt.Phase != ImportReserved {
		test.Fatal("reservation did not grant exactly one execution")
	}
	return reserved
}

func TestImportReceiptIdempotencyAndRestart(test *testing.T) {
	fixture := newImportTicketFixture(test)
	plan := ImportSelectionPlan{ConfigurationCount: 1}
	reserved := reserveImportForTest(test, fixture, plan)
	for range 2 {
		repeated, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ImportTicket{}, [16]byte{1}, sha256.Sum256([]byte("request")), plan)
		if err != nil || repeated.Execution.Granted() || repeated.Receipt.OperationID != reserved.Receipt.OperationID || repeated.Receipt.Revision != 1 {
			test.Fatal("same operation replayed instead of observing")
		}
	}
	if _, err := fixture.store.ReserveImportReceipt(test.Context(), fixture.credential, "https://cognate.invalid", ImportTicket{}, [16]byte{1}, sha256.Sum256([]byte("different request")), plan); !errors.Is(err, ErrImportConflict) {
		test.Fatal("same operation different input accepted")
	}
	row, err := fixture.store.state.client.ImportReceipt.Query().Only(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	if row.Charge != 131072 || bytes.Contains(row.Ciphertext, []byte("configurationCount")) {
		test.Fatal("receipt is not encrypted or charged")
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = reopened
	reopened.state.now = func() time.Time { return fixture.now }
	view, found, err := reopened.ReadImportReceipt(test.Context(), fixture.credential, [16]byte{1})
	if err != nil || !found || view.Phase != ImportReserved || view.Revision != 1 {
		test.Fatal("nonterminal receipt was not retained for unknown status")
	}
	if _, found, err := reopened.ReadImportReceipt(test.Context(), fixture.credential, [16]byte{2}); err != nil || found {
		test.Fatal("unknown operation falsely observed")
	}
	if err := reopened.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, _, err := reopened.ReadImportReceipt(test.Context(), fixture.credential, [16]byte{1}); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("revoked session read receipt")
	}
}

func TestImportReceiptAtomicAssetsAndSkillPins(test *testing.T) {
	fixture := newImportTicketFixture(test)
	reference, err := repository.ParseReference("example/toolkit", "HEAD")
	if err != nil {
		test.Fatal(err)
	}
	archiveHash, contentHash := strings.Repeat("a", 64), strings.Repeat("b", 64)
	collected, err := fixture.store.CollectRepository(test.Context(), fixture.credential.Owner(), reference, archiveHash, []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Example", Description: "Synthetic", Path: "skills/example"}, ContentHash: contentHash}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := fixture.store.CollectedRepository(test.Context(), fixture.credential.Owner(), collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	binding := ImportSkillBinding{RepositoryID: detail.ID, ArchiveHash: archiveHash, SkillID: detail.Skills[0].ID, LogicalPath: detail.Skills[0].Path, ContentHash: contentHash, Repository: detail.NameWithOwner, Branch: detail.Branch}
	reserved := reserveImportForTest(test, fixture, ImportSelectionPlan{SkillCount: 1, ConfigurationCount: 2})
	draft := ConfigurationAssetDraft{AgentID: "claude", Kind: ConfigurationKindProvider, Name: "Imported one", SchemaVersion: 1, Payload: NewConfigurationPayload([]byte("synthetic-secret-free-envelope"))}
	other := draft
	other.Name = "Imported two"
	badBinding := binding
	badBinding.ContentHash = strings.Repeat("c", 64)
	finish := ImportFinishSelection{Phase: ImportCompleted, Skills: []ImportSkillBinding{badBinding}, Drafts: []ConfigurationAssetDraft{draft, other}}
	if _, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, finish); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("stale skill pin accepted")
	}
	if rows, err := fixture.store.state.client.ConfigurationAsset.Query().Count(test.Context()); err != nil || rows != 0 {
		test.Fatal("failed final transaction left an asset")
	}
	finish.Skills = []ImportSkillBinding{binding}
	finish.Drafts[1].Name = draft.Name
	if _, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, finish); !errors.Is(err, ErrImportConflict) {
		test.Fatal("duplicate imported name accepted")
	}
	if rows, err := fixture.store.state.client.ConfigurationAsset.Query().Count(test.Context()); err != nil || rows != 0 {
		test.Fatal("last asset failure was not atomic")
	}
	finish.Drafts[1] = other
	completed, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, finish)
	if err != nil || completed.Phase != ImportCompleted || completed.Revision != 2 || len(completed.CreatedAssets) != 2 || len(completed.SelectedSkills) != 1 {
		test.Fatal("final asset/reference/receipt transaction failed")
	}
	for _, asset := range completed.CreatedAssets {
		payload, err := fixture.store.ReadConfigurationAsset(test.Context(), fixture.credential.Owner(), asset.ID)
		if err != nil || !bytes.Equal(payload.Reveal(), draft.Payload.Reveal()) {
			test.Fatal("stored asset envelope changed")
		}
	}
	if _, err := fixture.store.FinishImportSelection(test.Context(), fixture.credential, reserved.Execution, finish); !errors.Is(err, ErrImportConflict) {
		test.Fatal("stale execution finalized twice")
	}
	view, found, err := fixture.store.ReadImportReceipt(test.Context(), fixture.credential, [16]byte{1})
	if err != nil || !found || len(view.CreatedAssets) != 2 || view.SelectedSkills[0] != binding {
		test.Fatal("durable final receipt lost references")
	}
}

func newImportTicketFixture(test *testing.T) *importTicketFixture {
	test.Helper()
	directory := test.TempDir()
	if err := os.Chmod(directory, 0700); err != nil {
		test.Fatal(err)
	}
	fixture := &importTicketFixture{path: filepath.Join(directory, "imports.db"), key: bytes.Repeat([]byte{67}, 32), now: time.Date(2026, 10, 30, 1, 0, 0, 0, time.UTC), digest: sha256.Sum256([]byte("synthetic-session-digest"))}
	store, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = store
	test.Cleanup(func() { _ = fixture.store.Close() })
	store.state.now = func() time.Time { return fixture.now }
	if _, err := store.CreateOIDCSession(test.Context(), OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "alice", TokenHash: fixture.digest[:], IssuedAt: fixture.now.Add(-time.Minute), ExpiresAt: fixture.now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	fixture.credential, err = store.ImportCredentialForSession(test.Context(), fixture.digest)
	if err != nil {
		test.Fatal(err)
	}
	return fixture
}

func importTestClaims() ImportTicketClaims {
	return ImportTicketClaims{SourceProfile: ImportSourceCCSwitch24, SourceDigest: sha256.Sum256([]byte("source")), ProjectionDigest: sha256.Sum256([]byte("projection")), AdapterDigest: sha256.Sum256([]byte("adapter-and-host-rule")), ResolutionDigest: sha256.Sum256([]byte("resolution")), OptionsDigest: sha256.Sum256([]byte("options"))}
}

func TestImportTicketBindingAndBounds(test *testing.T) {
	fixture := newImportTicketFixture(test)
	claims := importTestClaims()
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", claims)
	if err != nil || len(sealed) != 290 || len(base64.StdEncoding.EncodeToString(sealed)) != 388 {
		test.Fatal("ticket shape invalid")
	}
	ticket, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed)
	actual, present := ticket.Claims()
	if err != nil || !present || actual != claims || !ticket.ExpiresAt().Equal(fixture.now.Add(5*time.Minute)) {
		test.Fatal("ticket claims or expiry changed")
	}
	for index := range sealed {
		tampered := bytes.Clone(sealed)
		tampered[index] ^= 1
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", tampered); err == nil {
			test.Fatalf("tampered byte%d accepted", index)
		}
	}
	for _, raw := range [][]byte{nil, sealed[:289], append(bytes.Clone(sealed), 0)} {
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", raw); !errors.Is(err, ErrImportInvalid) {
			test.Fatal("ticket length not rejected")
		}
	}
	for _, origin := range []string{"", "http://cognate.invalid", "https://cognate.invalid/", "https://user:synthetic@cognate.invalid", "https://COGNATE.invalid", "https://cognate.invalid?query=1"} {
		if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, claims); !errors.Is(err, ErrImportPrecondition) {
			test.Fatal("noncanonical origin accepted")
		}
	}
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://other.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("different origin accepted")
	}
	if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "http://127.0.0.1:8888", claims); err != nil {
		test.Fatal("explicit loopback origin rejected")
	}
	fixture.now = fixture.now.Add(-time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("clock rollback accepted")
	}
	fixture.now = fixture.now.Add(5*time.Minute + time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("expiry boundary accepted")
	}
	fixture.now = fixture.now.Add(-time.Millisecond)
	if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); err != nil {
		test.Fatal("unexpired ticket rejected")
	}
	if rows, err := fixture.store.state.client.ImportReceipt.Query().Count(test.Context()); err != nil || rows != 0 {
		test.Fatal("ticket persisted a receipt")
	}
}

func TestImportTicketSessionRestartAndPrivacy(test *testing.T) {
	fixture := newImportTicketFixture(test)
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.ImportCredentialForSession(test.Context(), [32]byte{}); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("unobserved session authorized")
	}
	bobHash := sha256.Sum256([]byte("other-synthetic-session"))
	if _, err := fixture.store.CreateOIDCSession(test.Context(), OIDCSessionPlan{Issuer: "https://issuer.invalid", Subject: "bob", TokenHash: bobHash[:], IssuedAt: fixture.now.Add(-time.Minute), ExpiresAt: fixture.now.Add(time.Hour)}); err != nil {
		test.Fatal(err)
	}
	bob, err := fixture.store.ImportCredentialForSession(test.Context(), bobHash)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.store.OpenImportTicket(test.Context(), bob, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("cross owner ticket accepted")
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	reopened, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = reopened
	reopened.state.now = func() time.Time { return fixture.now }
	ticket, err := reopened.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed)
	if err != nil {
		test.Fatal("same-key restart rejected valid ticket")
	}
	for _, value := range []any{fixture.credential, ticket} {
		encoded, err := json.Marshal(value)
		if err != nil || string(encoded) != `"[redacted import state]"` {
			test.Fatal("opaque JSON exposed")
		}
		for _, rendered := range []string{fmt.Sprintf("%v", value), fmt.Sprintf("%+v", value), fmt.Sprintf("%#v", value), fmt.Sprintf("%s", value)} {
			if rendered != "[redacted import state]" || strings.Contains(rendered, fixture.credential.Owner()) {
				test.Fatal("opaque formatting exposed")
			}
		}
	}
	if err := reopened.RevokeSession(test.Context(), fixture.digest[:]); err != nil {
		test.Fatal(err)
	}
	if _, err := reopened.OpenImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("revoked session ticket accepted")
	}
	canceled, cancel := context.WithCancel(test.Context())
	cancel()
	if _, err := reopened.ImportCredentialForSession(canceled, bobHash); !errors.Is(err, context.Canceled) {
		test.Fatal("cancellation ignored")
	}
}

func TestImportTicketAuthenticatedPlaintextAndPurpose(test *testing.T) {
	fixture := newImportTicketFixture(test)
	origin := "https://cognate.invalid"
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	plaintext, err := fixture.store.state.configurationAEAD.Open(nil, nil, sealed[4:], importTicketAAD(fixture.store.state.keyID, fixture.credential.Owner(), origin))
	if err != nil {
		test.Fatal(err)
	}
	defer clear(plaintext)
	for _, mutate := range []func([]byte){
		func(data []byte) { data[0] = 2 },
		func(data []byte) { data[1] = 0 },
		func(data []byte) { data[1] = 99 },
		func(data []byte) { binary.BigEndian.PutUint64(data[2:10], 0) },
		func(data []byte) {
			binary.BigEndian.PutUint64(data[10:18], uint64(fixture.now.Add(time.Minute).UnixMilli()))
		},
		func(data []byte) { data[34] ^= 1 },
		func(data []byte) { data[66] ^= 1 },
	} {
		altered := bytes.Clone(plaintext)
		mutate(altered)
		sealed := fixture.store.state.configurationAEAD.Seal([]byte("CIP1"), nil, altered, importTicketAAD(fixture.store.state.keyID, fixture.credential.Owner(), origin))
		if _, err := fixture.store.OpenImportTicket(test.Context(), fixture.credential, origin, sealed); !errors.Is(err, ErrImportPrecondition) {
			test.Fatal("invalid authenticated ticket claims accepted")
		}
	}
	if _, err := fixture.store.state.configurationAEAD.Open(nil, nil, sealed[4:], []byte("cognate/configuration-assets/v1/payload")); err == nil {
		test.Fatal("ticket accepted for another AEAD purpose")
	}
}

func TestImportTicketRejectIncompleteClaims(test *testing.T) {
	fixture := newImportTicketFixture(test)
	for _, mutate := range []func(*ImportTicketClaims){
		func(claims *ImportTicketClaims) { claims.SourceProfile = 0 },
		func(claims *ImportTicketClaims) { claims.SourceProfile = 99 },
		func(claims *ImportTicketClaims) { claims.SourceDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.ProjectionDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.AdapterDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.ResolutionDigest = [32]byte{} },
		func(claims *ImportTicketClaims) { claims.OptionsDigest = [32]byte{} },
	} {
		claims := importTestClaims()
		mutate(&claims)
		if _, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, "https://cognate.invalid", claims); !errors.Is(err, ErrImportInvalid) {
			test.Error("incomplete claims accepted")
		}
	}
}

func TestImportTicketKeyAndSessionExpiry(test *testing.T) {
	fixture := newImportTicketFixture(test)
	origin := "https://cognate.invalid"
	sealed, err := fixture.store.SealImportTicket(test.Context(), fixture.credential, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	if err := fixture.store.Close(); err != nil {
		test.Fatal(err)
	}
	if unexpected, err := Open(test.Context(), fixture.path, bytes.Repeat([]byte{68}, 32)); err == nil {
		_ = unexpected.Close()
		test.Fatal("database accepted a different master key")
	}
	rekeyed, err := Open(test.Context(), fixture.path, fixture.key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = rekeyed
	rekeyed.state.now = func() time.Time { return fixture.now }
	current, err := rekeyed.ImportCredentialForSession(test.Context(), fixture.digest)
	if err != nil {
		test.Fatal(err)
	}
	correctAEAD := rekeyed.state.configurationAEAD
	rekeyed.state.configurationAEAD, err = newConfigurationAEAD(bytes.Repeat([]byte{68}, 32))
	if err != nil {
		test.Fatal(err)
	}
	if _, err := rekeyed.OpenImportTicket(test.Context(), current, origin, sealed); !errors.Is(err, ErrImportPrecondition) {
		test.Fatal("different key accepted old ticket")
	}
	rekeyed.state.configurationAEAD = correctAEAD
	otherKey := current.box.value
	otherKey.keyID = "different-key"
	if _, err := rekeyed.OpenImportTicket(test.Context(), ImportOwnerCredential{importWrap(otherKey)}, origin, sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("credential from other key accepted")
	}
	sealed, err = rekeyed.SealImportTicket(test.Context(), current, origin, importTestClaims())
	if err != nil {
		test.Fatal(err)
	}
	fixture.now = fixture.now.Add(time.Hour)
	if _, err := rekeyed.OpenImportTicket(test.Context(), current, origin, sealed); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("expired session accepted ticket")
	}
	if _, err := rekeyed.ImportCredentialForSession(test.Context(), fixture.digest); !errors.Is(err, ErrImportUnauthorized) {
		test.Fatal("expired session produced credential")
	}
}
