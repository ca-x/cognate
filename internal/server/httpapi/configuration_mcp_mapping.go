package httpapi

import (
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/configuration/mcp/v1"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
)

func mcpConfigurationInput(draft *mcpv1.Draft, mutations []*mcpv1.SecretMutation) (_ configuration.MCPDraft, owned []configuration.MCPSecretMutation, failure error) {
	defer func() {
		for _, mutation := range mutations {
			if mutation != nil {
				clear(mutation.GetReplace())
			}
		}
		if failure != nil {
			mcpConfigurationClear(owned)
		}
	}()
	if draft == nil || draft.Document == nil || len(mutations) > 1024 {
		return configuration.MCPDraft{}, nil, configuration.ErrInvalid
	}
	document := agentconfig.MCPDocumentV1{Servers: make([]agentconfig.MCPServer, 0, len(draft.Document.Servers))}
	for _, server := range draft.Document.Servers {
		if server == nil || server.Enabled == nil {
			return configuration.MCPDraft{}, nil, configuration.ErrInvalid
		}
		typed := agentconfig.MCPServer{ID: server.Id, Name: server.Name, Enabled: new(*server.Enabled)}
		switch connection := server.Connection.(type) {
		case *mcpv1.Server_Stdio:
			if connection.Stdio == nil {
				return configuration.MCPDraft{}, nil, configuration.ErrInvalid
			}
			value := connection.Stdio
			typed.Stdio = &agentconfig.MCPStdio{Command: value.Command}
			if value.Args != nil {
				typed.Stdio.Args = &agentconfig.MCPArgs{Values: []agentconfig.MCPArg{}}
				for _, arg := range value.Args.Values {
					if arg == nil {
						return configuration.MCPDraft{}, nil, configuration.ErrInvalid
					}
					typed.Stdio.Args.Values = append(typed.Stdio.Args.Values, agentconfig.MCPArg{ID: arg.Id, ValueRef: arg.SlotRef})
				}
			}
			values, err := mcpConfigurationNamedInput(value.Env)
			if err != nil {
				return configuration.MCPDraft{}, nil, err
			}
			typed.Stdio.Env = values
		case *mcpv1.Server_Http:
			remote, err := mcpConfigurationRemoteInput(connection.Http)
			if err != nil {
				return configuration.MCPDraft{}, nil, err
			}
			typed.HTTP = remote
		case *mcpv1.Server_Sse:
			remote, err := mcpConfigurationRemoteInput(connection.Sse)
			if err != nil {
				return configuration.MCPDraft{}, nil, err
			}
			typed.SSE = remote
		default:
			return configuration.MCPDraft{}, nil, configuration.ErrInvalid
		}
		document.Servers = append(document.Servers, typed)
	}
	cloned, err := agentconfig.CloneMCPDocument(document)
	if err != nil {
		return configuration.MCPDraft{}, nil, mcpConfigurationShapeError(err)
	}
	owned = make([]configuration.MCPSecretMutation, 0, len(mutations))
	total := 0
	for _, mutation := range mutations {
		if mutation == nil || len(mutation.SlotId) > 128 {
			return configuration.MCPDraft{}, owned, configuration.ErrInvalid
		}
		typed := configuration.MCPSecretMutation{Slot: mutation.SlotId}
		switch action := mutation.Action.(type) {
		case *mcpv1.SecretMutation_Keep:
			if action.Keep == nil {
				return configuration.MCPDraft{}, owned, configuration.ErrInvalid
			}
			typed.Action = configuration.Keep
		case *mcpv1.SecretMutation_Replace:
			total += len(action.Replace)
			if len(action.Replace) > 8192 || total > 128<<10 {
				return configuration.MCPDraft{}, owned, configuration.ErrLimit
			}
			typed.Action = configuration.Replace
			typed.Replacement = configuration.NewMCPSecret(action.Replace)
		case *mcpv1.SecretMutation_Clear:
			if action.Clear == nil || action.Clear.Confirmed == nil || !*action.Clear.Confirmed {
				return configuration.MCPDraft{}, owned, configuration.ErrInvalid
			}
			typed.Action, typed.Confirmed = configuration.Clear, true
		default:
			return configuration.MCPDraft{}, owned, configuration.ErrInvalid
		}
		owned = append(owned, typed)
	}
	return configuration.MCPDraft{AgentID: draft.AgentId, Name: draft.Name, Document: cloned}, owned, nil
}

func mcpConfigurationShapeError(err error) error {
	if err == agentconfig.ErrMCPLimit {
		return configuration.ErrLimit
	}
	return configuration.ErrInvalid
}

func mcpConfigurationClear(mutations []configuration.MCPSecretMutation) {
	for index := range mutations {
		mutations[index].Replacement.Clear()
	}
}

func mcpConfigurationNamedInput(input *mcpv1.NamedSlots) (*agentconfig.MCPNamedSlots, error) {
	if input == nil {
		return nil, nil
	}
	result := &agentconfig.MCPNamedSlots{Values: []agentconfig.MCPNamedSlot{}}
	for _, value := range input.Values {
		if value == nil {
			return nil, configuration.ErrInvalid
		}
		result.Values = append(result.Values, agentconfig.MCPNamedSlot{Name: value.Name, ValueRef: value.SlotRef})
	}
	return result, nil
}

func mcpConfigurationRemoteInput(input *mcpv1.Remote) (*agentconfig.MCPRemote, error) {
	if input == nil {
		return nil, configuration.ErrInvalid
	}
	headers, err := mcpConfigurationNamedInput(input.Headers)
	return &agentconfig.MCPRemote{URL: input.Url, Headers: headers}, err
}

func mcpConfigurationNamed(input *agentconfig.MCPNamedSlots) *mcpv1.NamedSlots {
	if input == nil {
		return nil
	}
	result := &mcpv1.NamedSlots{Values: []*mcpv1.NamedSlot{}}
	for _, value := range input.Values {
		result.Values = append(result.Values, &mcpv1.NamedSlot{Name: value.Name, SlotRef: value.ValueRef})
	}
	return result
}

func mcpConfigurationServer(input agentconfig.MCPServer) *mcpv1.Server {
	result := &mcpv1.Server{Id: input.ID, Name: input.Name, Enabled: new(*input.Enabled)}
	if value := input.Stdio; value != nil {
		stdio := &mcpv1.Stdio{Command: value.Command, Env: mcpConfigurationNamed(value.Env)}
		if value.Args != nil {
			stdio.Args = &mcpv1.Args{Values: []*mcpv1.Arg{}}
			for _, arg := range value.Args.Values {
				stdio.Args.Values = append(stdio.Args.Values, &mcpv1.Arg{Id: arg.ID, SlotRef: arg.ValueRef})
			}
		}
		result.Connection = &mcpv1.Server_Stdio{Stdio: stdio}
	} else if value := input.HTTP; value != nil {
		result.Connection = &mcpv1.Server_Http{Http: &mcpv1.Remote{Url: value.URL, Headers: mcpConfigurationNamed(value.Headers)}}
	} else if value := input.SSE; value != nil {
		result.Connection = &mcpv1.Server_Sse{Sse: &mcpv1.Remote{Url: value.URL, Headers: mcpConfigurationNamed(value.Headers)}}
	}
	return result
}

func mcpConfigurationReadiness(input configuration.Readiness) (mcpv1.Readiness, error) {
	switch input {
	case configuration.CredentialComplete:
		return mcpv1.Readiness_READINESS_COMPLETE, nil
	case configuration.NeedsCredential:
		return mcpv1.Readiness_READINESS_NEEDS_CREDENTIAL, nil
	default:
		return 0, configuration.ErrUnavailable
	}
}

func mcpConfigurationView(input configuration.MCPView) (*mcpv1.View, error) {
	asset, err := configurationAsset(input.Metadata)
	if err != nil || input.Metadata.Kind != state.ConfigurationKindMCP {
		return nil, configuration.ErrUnavailable
	}
	slots, err := agentconfig.ValidateMCPDocument(input.Draft.Document)
	if err != nil || len(slots) != len(input.Secrets) {
		return nil, configuration.ErrUnavailable
	}
	readiness, err := mcpConfigurationReadiness(input.Readiness)
	if err != nil {
		return nil, err
	}
	result := &mcpv1.View{Asset: asset, Draft: &mcpv1.Draft{Name: input.Draft.Name, AgentId: input.Draft.AgentID, Document: &mcpv1.Document{Servers: []*mcpv1.Server{}}}, Readiness: readiness, HasPreservedExtras: input.HasPreservedExtras}
	for _, server := range input.Draft.Document.Servers {
		result.Draft.Document.Servers = append(result.Draft.Document.Servers, mcpConfigurationServer(server))
	}
	for index, slot := range input.Secrets {
		if slots[index].Ref != slot.Ref {
			return nil, configuration.ErrUnavailable
		}
		result.Secrets = append(result.Secrets, &mcpv1.SlotStatus{SlotId: slot.Ref, Present: new(slot.Present)})
	}
	return result, nil
}

func mcpConfigurationPreview(input configuration.MCPPreview) (*mcpv1.PreviewResponse, error) {
	readiness, err := mcpConfigurationReadiness(input.View.Readiness)
	if err != nil || input.CanApply || input.Applied {
		return nil, configuration.ErrUnavailable
	}
	result := &mcpv1.PreviewResponse{Readiness: readiness, Warnings: []mcpv1.Warning{mcpv1.Warning_WARNING_NATIVE_NOT_QUALIFIED}}
	for _, operation := range input.Declaration.Operations {
		if !operation.ManagedOnly {
			return nil, configuration.ErrUnavailable
		}
		value := &mcpv1.Operation{ServerId: operation.ServerID, NativeName: operation.Name, RequiresManagedProof: true}
		switch operation.Action {
		case "disable-managed-server":
			if operation.Stdio != nil || operation.HTTP != nil || operation.SSE != nil {
				return nil, configuration.ErrUnavailable
			}
			value.Action = mcpv1.Action_ACTION_DISABLE_MANAGED
		case "upsert-managed-server":
			server := agentconfig.MCPServer{ID: operation.ServerID, Name: operation.Name, Enabled: new(true), Stdio: operation.Stdio, HTTP: operation.HTTP, SSE: operation.SSE}
			if _, err := agentconfig.ValidateMCPDocument(agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{server}}); err != nil {
				return nil, configuration.ErrUnavailable
			}
			value.Action, value.Desired = mcpv1.Action_ACTION_UPSERT_MANAGED, mcpConfigurationServer(server)
		default:
			return nil, configuration.ErrUnavailable
		}
		result.Operations = append(result.Operations, value)
	}
	for _, warning := range input.Warnings {
		if warning != "insecure-http" {
			return nil, configuration.ErrUnavailable
		}
		result.Warnings = append(result.Warnings, mcpv1.Warning_WARNING_INSECURE_HTTP)
	}
	return result, nil
}
