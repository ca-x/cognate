package desktop

import (
	"context"
	"net/http"

	"github.com/ca-x/cognate/internal/grokclient"
)

func (service *Service) openGrokAssets(ctx context.Context, identity configurationIdentity) (*grokAssetsModel, error) {
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
	var settings *http.Transport
	if service.HTTP != nil && service.HTTP.Transport != nil {
		var ok bool
		settings, ok = service.HTTP.Transport.(*http.Transport)
		if !ok {
			return nil, grokclient.ErrUnsupported
		}
	}
	client, err := grokclient.New(identity.Origin, session, settings)
	if err != nil {
		return nil, err
	}
	verified, err := service.ConfigurationIdentity(ctx, identity.Origin, session)
	if err != nil {
		client.Close()
		return nil, err
	}
	defer verified.clear()
	if verified.OwnerID != identity.OwnerID || verified.Origin != identity.Origin {
		client.Close()
		return nil, errConfigurationIdentity
	}
	life, cancel := context.WithCancel(context.Background())
	state := &grokAssetsState{client: client, life: life, cancel: cancel}
	return &grokAssetsModel{private: func() *grokAssetsState { return state }}, nil
}
