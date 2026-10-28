package desktop

import (
	"context"
	"errors"

	"github.com/ca-x/cognate/internal/state"
	"github.com/ca-x/cognate/internal/subscription"
)

var errUnsupportedLocal = errors.New("local installation is unsupported on this platform（当前安装内核仅 Linux）")

func (service *Service) WatchCycle(ctx context.Context) (result subscription.SyncResult, records []state.Subscription, resultErr error) {
	manager, err := service.openPackManager(ctx)
	if err != nil {
		return result, nil, err
	}
	defer func() { resultErr = errors.Join(resultErr, manager.Close()) }()
	result, err = manager.Sync(ctx)
	if err != nil {
		return result, nil, err
	}
	records, err = manager.List(ctx)
	return result, records, err
}
