package configuration

import (
	"context"
	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/ca-x/cognate/internal/server/agentadapter"
	"github.com/ca-x/cognate/internal/server/state"
)

type MCPDraft struct {
	AgentID  string                    `json:"agentID"`
	Name     string                    `json:"name"`
	Document agentconfig.MCPDocumentV1 `json:"document"`
}

type MCPSecretMutation struct {
	Slot        string       `json:"slot"`
	Action      SecretAction `json:"action"`
	Replacement MCPSecret    `json:"replacement"`
	Confirmed   bool         `json:"confirmed"`
}

type MCPView struct {
	Metadata           state.ConfigurationAsset    `json:"metadata"`
	Draft              MCPDraft                    `json:"draft"`
	Secrets            []agentconfig.MCPSlotStatus `json:"secrets"`
	Readiness          Readiness                   `json:"readiness"`
	HasPreservedExtras bool                        `json:"hasPreservedExtras"`
}

type MCPPreview struct {
	View        MCPView                      `json:"view"`
	Declaration agentconfig.MCPDeclarationV1 `json:"declaration"`
	Warnings    []string                     `json:"warnings"`
	CanApply    bool                         `json:"canApply"`
	Applied     bool                         `json:"applied"`
}

type MCPService struct {
	store    Store
	registry *agentadapter.Registry
}

func NewMCP(store Store) *MCPService {
	registry, _ := agentadapter.Builtins()
	return &MCPService{store: store, registry: registry}
}

func (service *MCPService) ready(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if service == nil || service.store == nil || service.registry == nil {
		return ErrUnavailable
	}
	return nil
}
