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

func TestSelectionCodecOpenCodeMaps(test *testing.T) {
	for _, message := range []proto.Message{
		&configurationv1.OpenCodeHeaders{Entries: map[string]string{"Authorization": "opencode.value.01", "X-Tenant": "opencode.value.02"}},
		&configurationv1.OpenCodeV1Models{Entries: map[string]*configurationv1.OpenCodeV1Model{"a": {Name: new("Model A")}, "b": {Name: new("Model B")}}},
	} {
		wire, err := proto.Marshal(message)
		if err != nil {
			test.Fatal(err)
		}
		target := message.ProtoReflect().New().Interface()
		if err := (selectionCodec{}).Unmarshal(wire, target); err != nil || !proto.Equal(message, target) {
			test.Fatal("distinct canonical map entries rejected")
		}
	}
	entry, _ := proto.Marshal(&configurationv1.OpenCodeHeaders{Entries: map[string]string{"Authorization": "opencode.value.01"}})
	if err := (selectionCodec{}).Unmarshal(append(entry, entry...), &configurationv1.OpenCodeHeaders{}); err == nil {
		test.Fatal("duplicate map key silently overwrote prior value")
	}
}
