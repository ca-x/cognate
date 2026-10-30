package configuration

import (
	"context"
	"errors"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/state"
	"math"
	"strings"
)

func (service *MCPService) Create(ctx context.Context, owner string, input MCPDraft, mutations []MCPSecretMutation) (MCPView, error) {
	defer clearMCPMutations(mutations)
	if err := service.ready(ctx); err != nil {
		return MCPView{}, err
	}
	input, err := validateMCPDraft(input)
	if err != nil {
		return MCPView{}, err
	}
	envelope, err := mergeMCPEnvelope(mcpEnvelope{}, input.Document, mutations)
	if err != nil {
		return MCPView{}, err
	}
	defer envelope.Close()
	if _, err := service.plan(ctx, envelope); err != nil {
		return MCPView{}, err
	}
	encoded, err := envelope.encode()
	if err != nil {
		return MCPView{}, err
	}
	metadata, err := service.store.CreateConfigurationAsset(ctx, owner, mcpStateDraft(input, encoded))
	if err := failure(ctx, err); err != nil {
		return MCPView{}, err
	}
	if err := mcpWritten(metadata, input, "", 1); err != nil {
		return MCPView{}, err
	}
	return mcpView(metadata, envelope), nil
}

func validateMCPDraft(input MCPDraft) (MCPDraft, error) {
	if input.AgentID != "claude" {
		return MCPDraft{}, ErrUnsupported
	}
	if !validText(input.Name, 128) || input.Name != strings.TrimSpace(input.Name) {
		return MCPDraft{}, ErrInvalid
	}
	document, err := agentconfig.CloneMCPDocument(input.Document)
	if err != nil {
		return MCPDraft{}, mcpShapeFailure(err)
	}
	input.Document = document
	return input, nil
}

func mcpStateDraft(input MCPDraft, payload state.ConfigurationPayload) state.ConfigurationAssetDraft {
	return state.ConfigurationAssetDraft{AgentID: input.AgentID, Name: input.Name, Kind: state.ConfigurationKindMCP, SchemaVersion: 1, Payload: payload}
}

func mcpWritten(metadata state.ConfigurationAsset, input MCPDraft, id string, revision int64) error {
	if validateMetadata(metadata) != nil || metadata.Kind != state.ConfigurationKindMCP || metadata.AgentID != input.AgentID || metadata.Name != input.Name || metadata.SchemaVersion != 1 || metadata.Revision != revision || (id != "" && metadata.ID != id) {
		return ErrUnavailable
	}
	return nil
}

func mcpView(metadata state.ConfigurationAsset, envelope mcpEnvelope) MCPView {
	data := envelope.data()
	document, _ := agentconfig.CloneMCPDocument(data.document)
	view := MCPView{Metadata: metadata, Draft: MCPDraft{AgentID: metadata.AgentID, Name: metadata.Name, Document: document}, Secrets: []agentconfig.MCPSlotStatus{}, Readiness: CredentialComplete}
	slots, _ := agentconfig.ValidateMCPDocument(document)
	for _, slot := range slots {
		secret, present := data.slots[slot.Ref]
		present = present && secret.bytes() != nil
		view.Secrets = append(view.Secrets, agentconfig.MCPSlotStatus{Ref: slot.Ref, Present: present})
		if !present {
			view.Readiness = NeedsCredential
		}
	}
	return view
}

func (service *MCPService) plan(ctx context.Context, envelope mcpEnvelope) (agentconfig.MCPDeclarationV1, error) {
	data := envelope.data()
	if data == nil {
		return agentconfig.MCPDeclarationV1{}, ErrInvalid
	}
	view := mcpView(state.ConfigurationAsset{}, envelope)
	for _, descriptor := range service.registry.List() {
		if descriptor.Manifest.PluginID != agentconfig.MCPPluginID {
			continue
		}
		result, err := service.registry.PlanMCP(ctx, agentconfig.MCPPlanRequest{APIVersion: 1, PluginID: agentconfig.MCPPluginID, PluginVersion: agentconfig.MCPPluginVersion, DefinitionDigest: descriptor.DefinitionDigest, Target: agentconfig.ClaudeMCP, Capabilities: []agentconfig.Capability{agentconfig.ClaudeMCPPlanV1}, Document: data.document, Secrets: view.Secrets})
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return agentconfig.MCPDeclarationV1{}, err
			}
			return agentconfig.MCPDeclarationV1{}, ErrUnavailable
		}
		return result, nil
	}
	return agentconfig.MCPDeclarationV1{}, ErrUnavailable
}

func (service *MCPService) Get(ctx context.Context, owner, id string) (state.ConfigurationAsset, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationAsset{}, err
	}
	metadata, err := service.store.ConfigurationAsset(ctx, owner, id)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationAsset{}, err
	}
	if validateMetadata(metadata) != nil || metadata.ID != id {
		return state.ConfigurationAsset{}, ErrUnavailable
	}
	if metadata.Kind != state.ConfigurationKindMCP {
		return state.ConfigurationAsset{}, ErrNotFound
	}
	return metadata, nil
}

func (service *MCPService) List(ctx context.Context, owner string) ([]state.ConfigurationAsset, error) {
	if err := service.ready(ctx); err != nil {
		return nil, err
	}
	all, err := New(service.store).List(ctx, owner)
	if err != nil {
		return nil, err
	}
	result := make([]state.ConfigurationAsset, 0)
	for _, metadata := range all {
		if metadata.Kind == state.ConfigurationKindMCP {
			result = append(result, metadata)
		}
	}
	return result, nil
}

func (service *MCPService) load(ctx context.Context, owner, id string) (state.ConfigurationAsset, mcpEnvelope, error) {
	if err := service.ready(ctx); err != nil {
		return state.ConfigurationAsset{}, mcpEnvelope{}, err
	}
	metadata, payload, err := service.store.ReadConfigurationAssetVersion(ctx, owner, id)
	if err := failure(ctx, err); err != nil {
		return state.ConfigurationAsset{}, mcpEnvelope{}, err
	}
	if validateMetadata(metadata) != nil || metadata.ID != id {
		return state.ConfigurationAsset{}, mcpEnvelope{}, ErrUnavailable
	}
	if metadata.Kind != state.ConfigurationKindMCP {
		return state.ConfigurationAsset{}, mcpEnvelope{}, ErrNotFound
	}
	if metadata.AgentID != "claude" || metadata.SchemaVersion != 1 {
		return metadata, mcpEnvelope{}, ErrUnsupported
	}
	envelope, err := decodeMCPEnvelope(payload)
	return metadata, envelope, err
}

func (service *MCPService) ReadDraft(ctx context.Context, owner, id string) (MCPView, error) {
	metadata, envelope, err := service.load(ctx, owner, id)
	if errors.Is(err, ErrUnsupported) {
		return MCPView{Metadata: metadata, Draft: MCPDraft{AgentID: metadata.AgentID, Name: metadata.Name}, Secrets: []agentconfig.MCPSlotStatus{}, Readiness: NeedsCredential, HasPreservedExtras: true}, nil
	}
	if err != nil {
		return MCPView{}, err
	}
	defer envelope.Close()
	if err := ctx.Err(); err != nil {
		return MCPView{}, err
	}
	return mcpView(metadata, envelope), nil
}

func (service *MCPService) Update(ctx context.Context, owner, id string, expectedRevision int64, input MCPDraft, mutations []MCPSecretMutation) (MCPView, error) {
	defer clearMCPMutations(mutations)
	if expectedRevision < 1 || expectedRevision == math.MaxInt64 {
		return MCPView{}, ErrInvalid
	}
	metadata, current, err := service.load(ctx, owner, id)
	if err != nil {
		return MCPView{}, err
	}
	defer current.Close()
	if metadata.Revision != expectedRevision {
		return MCPView{}, ErrConflict
	}
	input, err = validateMCPDraft(input)
	if err != nil {
		return MCPView{}, err
	}
	if metadata.AgentID != input.AgentID {
		return MCPView{}, ErrInvalid
	}
	envelope, err := mergeMCPEnvelope(current, input.Document, mutations)
	if err != nil {
		return MCPView{}, err
	}
	defer envelope.Close()
	if _, err := service.plan(ctx, envelope); err != nil {
		return MCPView{}, err
	}
	encoded, err := envelope.encode()
	if err != nil {
		return MCPView{}, err
	}
	updated, err := service.store.UpdateConfigurationAsset(ctx, owner, id, expectedRevision, mcpStateDraft(input, encoded))
	if err := failure(ctx, err); err != nil {
		return MCPView{}, err
	}
	if err := mcpWritten(updated, input, id, expectedRevision+1); err != nil {
		return MCPView{}, err
	}
	return mcpView(updated, envelope), nil
}

func (service *MCPService) Copy(ctx context.Context, owner, id string, expectedRevision int64, name string) (MCPView, error) {
	if expectedRevision < 1 {
		return MCPView{}, ErrInvalid
	}
	metadata, envelope, err := service.load(ctx, owner, id)
	if err != nil {
		return MCPView{}, err
	}
	defer envelope.Close()
	if metadata.Revision != expectedRevision {
		return MCPView{}, ErrConflict
	}
	return service.Create(ctx, owner, MCPDraft{AgentID: metadata.AgentID, Name: name, Document: envelope.data().document}, nil)
}

func (service *MCPService) Delete(ctx context.Context, owner, id string, expectedRevision int64) error {
	if expectedRevision < 1 {
		return ErrInvalid
	}
	metadata, err := service.Get(ctx, owner, id)
	if err != nil {
		return err
	}
	if metadata.Revision != expectedRevision {
		return ErrConflict
	}
	return failure(ctx, service.store.DeleteConfigurationAsset(ctx, owner, id, expectedRevision))
}

func (service *MCPService) Preview(ctx context.Context, owner, id string, expectedRevision int64, input MCPDraft, mutations []MCPSecretMutation) (MCPPreview, error) {
	defer clearMCPMutations(mutations)
	if err := service.ready(ctx); err != nil {
		return MCPPreview{}, err
	}
	if owner == "" {
		return MCPPreview{}, ErrInvalid
	}
	input, err := validateMCPDraft(input)
	if err != nil {
		return MCPPreview{}, err
	}
	var metadata state.ConfigurationAsset
	var current mcpEnvelope
	if id != "" {
		if expectedRevision < 1 {
			return MCPPreview{}, ErrInvalid
		}
		metadata, current, err = service.load(ctx, owner, id)
		if err != nil {
			return MCPPreview{}, err
		}
		defer current.Close()
		if metadata.Revision != expectedRevision {
			return MCPPreview{}, ErrConflict
		}
	} else if expectedRevision != 0 {
		return MCPPreview{}, ErrInvalid
	}
	envelope, err := mergeMCPEnvelope(current, input.Document, mutations)
	if err != nil {
		return MCPPreview{}, err
	}
	defer envelope.Close()
	declaration, err := service.plan(ctx, envelope)
	if err != nil {
		return MCPPreview{}, err
	}
	if _, err := envelope.encode(); err != nil {
		return MCPPreview{}, err
	}
	metadata.AgentID = input.AgentID
	metadata.Name = input.Name
	return MCPPreview{View: mcpView(metadata, envelope), Declaration: declaration, Warnings: mcpWarnings(input.Document)}, nil
}
