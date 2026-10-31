package state

import "context"

func (store *Store) MaintenanceManaged() bool {
	return store != nil && store.state != nil && store.state.maintenance != nil
}

func (store *Store) WithMaintenanceWriter[Value any](ctx context.Context, operation func(context.Context) (Value, error)) (Value, error) {
	var zero Value
	ctx, release, err := store.enterWriter(ctx)
	if err != nil {
		return zero, err
	}
	defer release()
	return operation(ctx)
}
