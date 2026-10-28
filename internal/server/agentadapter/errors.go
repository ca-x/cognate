package agentadapter

import "errors"

var (
	ErrInvalidDefinition = errors.New("invalid agent adapter definition")
	ErrInvalidRequest    = errors.New("invalid agent configuration request")
	ErrIncompatible      = errors.New("incompatible agent adapter version or capabilities")
	ErrUnsupported       = errors.New("agent configuration rendering is unsupported")
	ErrPluginResult      = errors.New("invalid agent adapter result")
	ErrPluginExecution   = errors.New("agent adapter execution failed")
)
