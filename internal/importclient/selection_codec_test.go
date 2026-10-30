package importclient

import (
	"testing"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

func TestSelectionCodecRejectsAmbiguousScalars(test *testing.T) {
	for name, entry := range map[string]struct {
		wire   []byte
		target proto.Message
	}{
		"bool-two":        {protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 2), &importv1.ImportCredentialSlot{}},
		"enum-overflow":   {protowire.AppendVarint(protowire.AppendTag(nil, 2, protowire.VarintType), 1<<32+1), &importv1.ImportNotice{}},
		"uint32-overflow": {protowire.AppendVarint(protowire.AppendTag(nil, 5, protowire.VarintType), 1<<32+1), &configurationv1.Asset{}},
		"duplicate":       {[]byte{0x10, 1, 0x10, 1}, &importv1.ImportCredentialSlot{}},
		"nonminimal":      {[]byte{0x10, 0x81, 0}, &importv1.ImportCredentialSlot{}},
	} {
		test.Run(name, func(test *testing.T) {
			if err := (selectionCodec{}).Unmarshal(entry.wire, entry.target); err == nil {
				test.Fatal("ambiguous scalar wire accepted")
			}
		})
	}
	for _, message := range []proto.Message{&importv1.ImportCredentialSlot{SlotId: "slot", Present: false}, &importv1.ImportCredentialSlot{SlotId: "slot", Present: true}, &importv1.ImportNotice{Code: 1, Count: 1}, &configurationv1.Asset{SchemaVersion: 1}} {
		wire, err := proto.Marshal(message)
		if err != nil {
			test.Fatal(err)
		}
		if err := (selectionCodec{}).Unmarshal(wire, message.ProtoReflect().New().Interface()); err != nil {
			test.Fatal("canonical scalar rejected")
		}
	}
}
