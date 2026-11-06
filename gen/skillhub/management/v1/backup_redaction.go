package managementv1

import (
	"fmt"
	"io"
	"log/slog"
)

func (*UpdateBackupSettingsRequest) Format(output fmt.State, _ rune) {
	_, _ = io.WriteString(output, "[private backup settings update]")
}

func (*UpdateBackupSettingsRequest) LogValue() slog.Value {
	return slog.StringValue("[private backup settings update]")
}

func (*UpdateBackupSettingsRequest) MarshalJSON() ([]byte, error) {
	return []byte(`{}`), nil
}
