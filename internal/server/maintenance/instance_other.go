//go:build !unix

package maintenance

import "context"

func Join(ctx context.Context, _ string) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}
