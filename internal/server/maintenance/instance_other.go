//go:build !unix

package maintenance

import "context"

func CheckPlatform(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnsupported
}

func Join(ctx context.Context, _ string) (*Instance, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, ErrUnsupported
}
