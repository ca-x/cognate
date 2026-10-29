package configuration

import (
	"bytes"
	"fmt"
	"io"
	"log/slog"
)

type Secret struct{ private func() []byte }

func NewSecret(value []byte) Secret {
	owned := bytes.Clone(value)
	return Secret{private: func() []byte { return owned }}
}

func (secret *Secret) Clear() {
	if secret == nil {
		return
	}
	clear(secretBytes(*secret))
	secret.private = nil
}

func secretBytes(secret Secret) []byte {
	if secret.private == nil {
		return nil
	}
	return secret.private()
}

func cloneSecret(secret Secret) Secret { return NewSecret(secretBytes(secret)) }

func (Secret) String() string               { return "[redacted configuration secret]" }
func (Secret) GoString() string             { return "[redacted configuration secret]" }
func (Secret) MarshalJSON() ([]byte, error) { return []byte(`"[redacted configuration secret]"`), nil }
func (Secret) MarshalText() ([]byte, error) { return []byte("[redacted configuration secret]"), nil }
func (Secret) LogValue() slog.Value         { return slog.StringValue("[redacted configuration secret]") }
func (Secret) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[redacted configuration secret]")
}

func (secrets *credentials) clear() {
	secrets.apiKey.Clear()
	secrets.authToken.Clear()
}
