package objectid_test

import (
	"testing"

	"github.com/ca-x/skillhub/internal/objectid"
)

func TestValidate(t *testing.T) {
	for _, identity := range []string{"linux-dev-ino-v1:0:1", "linux-dev-ino-v1:18446744073709551615:18446744073709551615"} {
		if err := objectid.Validate(identity); err != nil {
			t.Errorf("rejected %q: %v", identity, err)
		}
	}
	for _, identity := range []string{"", "linux-dev-ino-v1:0:0", "linux-dev-ino-v1:01:1", "linux-dev-ino-v1:-1:1", "linux-dev-ino-v1:18446744073709551616:1", "linux-dev-ino-v1:1:1:1", "other:1:1"} {
		if err := objectid.Validate(identity); err == nil {
			t.Errorf("accepted %q", identity)
		}
	}
}
