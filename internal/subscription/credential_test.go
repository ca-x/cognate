package subscription

import (
	"bytes"
	"context"
	"testing"

	"github.com/ca-x/skillhub/internal/state"
)

func TestSavedCredentialBindsSubscriptionServerAndPack(test *testing.T) {
	key, err := deriveCredentialKey(bytes.Repeat([]byte{44}, 32))
	if err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	manager := &Manager{key: key}
	record := state.Subscription{ID: "First", Server: "https://hub.example", PackID: "ExamplePack"}
	plaintext := []byte("synthetic-credential-material")
	sealed, err := manager.sealCredential(record.ID, record.Server, record.PackID, plaintext)
	if err != nil {
		test.Fatal(err)
	}
	if bytes.Contains(sealed, plaintext) {
		test.Fatal("credential storage did not encrypt")
	}
	opened, err := manager.openCredential(record, sealed)
	if err != nil || !bytes.Equal(opened, plaintext) {
		test.Fatal("credential failed valid round trip")
	}
	clear(opened)
	for _, altered := range []state.Subscription{{ID: "Other", Server: record.Server, PackID: record.PackID}, {ID: record.ID, Server: "https://other.example", PackID: record.PackID}, {ID: record.ID, Server: record.Server, PackID: "OtherPack"}} {
		if _, err := manager.openCredential(altered, sealed); err == nil {
			test.Fatal("copied ciphertext retained authority under a different binding")
		}
	}
	sealed[len(sealed)-1] ^= 1
	if _, err := manager.openCredential(record, sealed); err == nil {
		test.Fatal("modified ciphertext accepted")
	}
	closed, cancel := context.WithCancel(test.Context())
	cancel()
	manager.life = closed
	if _, _, err := manager.requestContext(test.Context()); err == nil {
		test.Fatal("closed manager accepted operation")
	}
}
