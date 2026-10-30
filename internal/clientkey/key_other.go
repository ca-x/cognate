//go:build !linux && !darwin

package clientkey

import (
	"context"
	"errors"
)

var (
	ErrInitializationRequired = errors.New("client key initialization required")
	ErrRecoveryRequired       = errors.New("client key recovery required")
	ErrUnavailable            = errors.New("client key unavailable")
	ErrUnsupported            = errors.New("client key unsupported")
)

func LoadExisting(ctx context.Context, home string) ([]byte, error) { return nil, ErrUnsupported }
func Initialize(ctx context.Context, home string) error             { return ErrUnsupported }
