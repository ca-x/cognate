package desktop

import (
	"context"

	"github.com/ca-x/cognate/internal/mcpclient"
)

func (service *Service) openMCPAssets(ctx context.Context, identity configurationIdentity) (*mcpAssetsModel, error) {
	if ctx == nil || identity.private == nil {
		return nil, errConfigurationIdentity
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	data := identity.private()
	data.mutex.Lock()
	session := data.session
	data.mutex.Unlock()
	verified, err := service.ConfigurationIdentity(ctx, identity.Origin, session)
	if err != nil {
		return nil, err
	}
	defer verified.clear()
	if verified.OwnerID != identity.OwnerID || verified.Origin != identity.Origin {
		return nil, errConfigurationIdentity
	}
	client, err := mcpclient.New(identity.Origin, session, service.HTTP)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(context.Background())
	state := &mcpAssetsState{client: client, life: life, cancel: cancel}
	return &mcpAssetsModel{private: func() *mcpAssetsState { return state }}, nil
}
