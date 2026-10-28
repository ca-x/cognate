package httpapi

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"
	mcpv1 "github.com/ca-x/cognate/gen/skillhub/mcp/v1"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/state"
)

type mcpManagementHandler struct {
	auth   *authHandler
	access *mcpaccess.Service
	once   sync.Once
	slots  chan struct{}
}

func (handler *mcpManagementHandler) CreateCredential(ctx context.Context, request *connect.Request[mcpv1.CreateCredentialRequest]) (*connect.Response[mcpv1.CreateCredentialResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.CreateCredentialResponse, error) {
		entry, secret, err := handler.access.Create(ctx, owner, mcpSpec(request.Msg.Spec))
		if err != nil {
			return nil, err
		}
		return &mcpv1.CreateCredentialResponse{Credential: mcpCredential(entry), Token: secret.Reveal()}, nil
	})
}

func (handler *mcpManagementHandler) ListCredentials(ctx context.Context, request *connect.Request[mcpv1.ListCredentialsRequest]) (*connect.Response[mcpv1.ListCredentialsResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.ListCredentialsResponse, error) {
		entries, err := handler.access.List(ctx, owner)
		if err != nil {
			return nil, err
		}
		response := &mcpv1.ListCredentialsResponse{Credentials: make([]*mcpv1.Credential, 0, len(entries))}
		for _, entry := range entries {
			response.Credentials = append(response.Credentials, mcpCredential(entry))
		}
		return response, nil
	})
}

func (handler *mcpManagementHandler) GetCredential(ctx context.Context, request *connect.Request[mcpv1.GetCredentialRequest]) (*connect.Response[mcpv1.GetCredentialResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.GetCredentialResponse, error) {
		entry, err := handler.access.Get(ctx, owner, request.Msg.Id)
		if err != nil {
			return nil, err
		}
		return &mcpv1.GetCredentialResponse{Credential: mcpCredential(entry)}, nil
	})
}

func (handler *mcpManagementHandler) UpdateCredential(ctx context.Context, request *connect.Request[mcpv1.UpdateCredentialRequest]) (*connect.Response[mcpv1.UpdateCredentialResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.UpdateCredentialResponse, error) {
		entry, err := handler.access.Update(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision, mcpSpec(request.Msg.Spec))
		if err != nil {
			return nil, err
		}
		return &mcpv1.UpdateCredentialResponse{Credential: mcpCredential(entry)}, nil
	})
}

func (handler *mcpManagementHandler) RotateCredential(ctx context.Context, request *connect.Request[mcpv1.RotateCredentialRequest]) (*connect.Response[mcpv1.RotateCredentialResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.RotateCredentialResponse, error) {
		entry, secret, err := handler.access.Rotate(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return &mcpv1.RotateCredentialResponse{Credential: mcpCredential(entry), Token: secret.Reveal()}, nil
	})
}

func (handler *mcpManagementHandler) RevokeCredential(ctx context.Context, request *connect.Request[mcpv1.RevokeCredentialRequest]) (*connect.Response[mcpv1.RevokeCredentialResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.RevokeCredentialResponse, error) {
		entry, err := handler.access.Revoke(ctx, owner, request.Msg.Id, request.Msg.ExpectedRevision)
		if err != nil {
			return nil, err
		}
		return &mcpv1.RevokeCredentialResponse{Credential: mcpCredential(entry)}, nil
	})
}

func (handler *mcpManagementHandler) ListAudit(ctx context.Context, request *connect.Request[mcpv1.ListAuditRequest]) (*connect.Response[mcpv1.ListAuditResponse], error) {
	return handler.localCall(ctx, request.Header(), func(ctx context.Context, owner string) (*mcpv1.ListAuditResponse, error) {
		entries, err := handler.access.ListAudit(ctx, owner, request.Msg.CredentialId)
		if err != nil {
			return nil, err
		}
		response := &mcpv1.ListAuditResponse{Entries: make([]*mcpv1.Audit, 0, len(entries))}
		for _, entry := range entries {
			response.Entries = append(response.Entries, &mcpv1.Audit{CredentialId: entry.CredentialID, Action: entry.Action, Resource: entry.Resource, Outcome: entry.Outcome, CreatedAtUnix: entry.CreatedAt.Unix()})
		}
		return response, nil
	})
}

func (handler *mcpManagementHandler) localCall[Response any](ctx context.Context, header http.Header, operation func(context.Context, string) (*Response, error)) (*connect.Response[Response], error) {
	handler.once.Do(func() { handler.slots = make(chan struct{}, 8) })
	select {
	case handler.slots <- struct{}{}:
		defer func() { <-handler.slots }()
	default:
		return nil, connect.NewError(connect.CodeResourceExhausted, errors.New("MCP management capacity exhausted"))
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	owner, err := handler.auth.authenticate(ctx, header)
	if err != nil {
		return nil, err
	}
	response, err := operation(ctx, owner)
	if err != nil {
		return nil, mcpError(ctx, err)
	}
	return connect.NewResponse(response), nil
}

func mcpError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	for _, mapping := range []struct {
		err  error
		code connect.Code
	}{
		{state.ErrMCPInvalid, connect.CodeInvalidArgument},
		{state.ErrMCPNotFound, connect.CodeNotFound},
		{state.ErrMCPConflict, connect.CodeAborted},
		{state.ErrMCPLimit, connect.CodeResourceExhausted},
	} {
		if errors.Is(err, mapping.err) {
			return connect.NewError(mapping.code, mapping.err)
		}
	}
	return connect.NewError(connect.CodeInternal, state.ErrMCPUnavailable)
}

func mcpSpec(spec *mcpv1.CredentialSpec) state.MCPMetadata {
	return state.MCPMetadata{Name: spec.GetName(), Scopes: spec.GetScopes(), ExpiresAt: time.Unix(spec.GetExpiresAtUnix(), 0)}
}

func mcpCredential(entry state.MCPCredential) *mcpv1.Credential {
	response := &mcpv1.Credential{Id: entry.ID, Name: entry.Name, Scopes: entry.Scopes, CreatedAtUnix: entry.CreatedAt.Unix(), ExpiresAtUnix: entry.ExpiresAt.Unix(), Revision: entry.Revision}
	if entry.RevokedAt != nil {
		response.RevokedAtUnix = entry.RevokedAt.Unix()
	}
	return response
}
