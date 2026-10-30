package configclient

import (
	"bytes"
	"context"
	"errors"
	"slices"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/codexconfig"
	"github.com/ca-x/cognate/internal/configapply"
	"google.golang.org/protobuf/proto"
)

func codexAdmissionFailure(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, configapply.ErrCodexClosed):
		return ErrNativeCanceled
	case errors.Is(err, configapply.ErrCodexUnknown):
		return ErrNativeUnknown
	case errors.Is(err, configapply.ErrCodexUnsupported):
		return ErrNativeUnavailable
	default:
		return ErrNativeInvalid
	}
}

func codexAdmittedProjection(binding codexconfig.ProviderBinding, identity configapply.CodexProfileIdentity) (*nativev1.CodexLocalBindingV1, error) {
	id, manifest, profile, template := codexconfig.Native0166Manifest()
	if identity != (configapply.CodexProfileIdentity{ID: id, ManifestSHA256: manifest, ProfileDigest: profile, TemplateDigest: template}) {
		return nil, ErrNativeUnavailable
	}
	observation := binding.CopyObservation()
	if observation.ProfileID != identity.ID || observation.ProfileDigest != identity.ProfileDigest || observation.TemplateDigest != identity.TemplateDigest || observation.Scope != codexconfig.CompleteEffective {
		return nil, ErrNativeInvalid
	}
	ownership := func(value codexconfig.Ownership) nativev1.CodexOwnership {
		switch value {
		case codexconfig.Absent:
			return 1
		case codexconfig.Managed:
			return 2
		case codexconfig.Foreign:
			return 3
		case codexconfig.OwnershipUnknown:
			return 4
		default:
			return 0
		}
	}
	stores := map[codexconfig.AuthStore]nativev1.CodexAuthStore{codexconfig.File: 1, codexconfig.Keyring: 2, codexconfig.Auto: 3, codexconfig.Ephemeral: 4}
	keyrings := map[codexconfig.KeyringState]nativev1.CodexKeyringState{codexconfig.FoundCredentialLogin: 1, codexconfig.KeyringMissing: 2, codexconfig.KeyringUnknown: 3, codexconfig.KeyringNotObserved: 4}
	logins := map[codexconfig.LoginState]nativev1.CodexFileLoginState{codexconfig.CredentialLogin: 1, codexconfig.NoCredentialLogin: 2, codexconfig.LoginUnknown: 3, codexconfig.LoginNotObserved: 4}
	projection := &nativev1.CodexLocalBindingV1{BindingVersion: 1, NativeProfileId: identity.ID, NativeProfileSha256: bytes.Clone(identity.ManifestSHA256[:]), TemplateSha256: bytes.Clone(identity.TemplateDigest[:]), EffectiveScope: 1, ProviderOwnership: ownership(observation.ProviderOwnership), CatalogOwnership: ownership(observation.CatalogOwnership), ManagedModels: slices.Clone(observation.ManagedModels), AuthStore: stores[observation.AuthStore], KeyringState: keyrings[observation.Keyring], FileLoginState: logins[observation.FileLogin], LocalAuthPolicy: string(observation.Policy), RequiresOpenaiAuth: new(observation.RequiresOpenAIAuth), ObservationWitness: bytes.Clone(observation.Witness[:])}
	for index, baseline := range binding.CopyBaselines() {
		if baseline.Role != []codexconfig.Role{codexconfig.ConfigRole, codexconfig.AuthRole, codexconfig.CatalogRole}[index] {
			return nil, ErrNativeInvalid
		}
		entry := &nativev1.CodexRoleBaseline{Role: nativev1.CodexRole(index + 1)}
		switch baseline.Presence {
		case codexconfig.Missing:
			entry.State = &nativev1.CodexRoleBaseline_Missing{Missing: new(nativev1.CodexMissing)}
		case codexconfig.Present:
			entry.State = &nativev1.CodexRoleBaseline_Present{Present: &nativev1.CodexPresent{Sha256: bytes.Clone(baseline.Digest[:])}}
		case codexconfig.NotObserved:
			entry.State = &nativev1.CodexRoleBaseline_NotObserved{NotObserved: new(nativev1.CodexNotObserved)}
		default:
			return nil, ErrNativeInvalid
		}
		projection.Baselines = append(projection.Baselines, entry)
	}
	if !validCodexLocal(projection) {
		return nil, ErrNativeInvalid
	}
	return projection, nil
}

func (flow *NativeFlow) AuthorizeCodexAdmitted(caller context.Context, admission configapply.CodexAdmission, approval CodexNativeApproval) (CodexNativeAuthorization, error) {
	ctx, done, err := flow.start(caller)
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	defer done()
	if flow.agent != "codex" || flow.codex == nil || flow.codex.prepared == nil || flow.codex.authorizeAttempted || !approval.present() {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	constraints := admission.CopyAuthorizationConstraints()
	defer constraints.Destroy()
	consent := approval.box.value
	operation := constraints.OperationID()
	if operation == [16]byte{} || !bytes.Equal(operation[:], flow.codex.prepared.Binding.OperationId) || !slices.Equal(constraints.RequiredSlots(), []agentconfig.CodexSecretSlot{agentconfig.CodexProviderKeySecret}) || consent.revision != constraints.ConfirmationRevision() || consent.expires.UnixMilli() != constraints.ApprovedDeadline().UnixMilli() {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	if err := admission.Revalidate(ctx); err != nil {
		return CodexNativeAuthorization{}, codexAdmissionFailure(err)
	}
	prepared := admission.CopyPreparedBinding()
	if !proto.Equal(prepared, flow.codex.prepared.Binding) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	binding := admission.CopyProviderBinding()
	defer binding.Destroy()
	projection, err := codexAdmittedProjection(binding, admission.CopyProfileIdentity())
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	defer codexClearWire(projection)
	if !codexProjectionMatchesBinding(flow.codex.prepared, binding, projection) {
		return CodexNativeAuthorization{}, ErrNativeInvalid
	}
	if err := admission.BeginAuthorize(ctx, constraints); err != nil {
		return CodexNativeAuthorization{}, codexAdmissionFailure(err)
	}
	flow.codex.admitted = admission
	grant, err := flow.authorizeCodexStarted(ctx, caller, projection, approval)
	if err != nil {
		return CodexNativeAuthorization{}, err
	}
	if err := admission.Revalidate(ctx); err != nil {
		grant.Destroy()
		flow.grant.Destroy()
		return CodexNativeAuthorization{}, ErrNativeUnknown
	}
	return grant, nil
}
