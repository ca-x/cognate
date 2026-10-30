package configuration

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/ca-x/cognate/internal/agentconfig"
	"testing"
)

func TestMCPEnvelopeOwnedAndClosed(test *testing.T) {
	source := []byte("MCP-PRIVATE-CANARY-638124")
	secret := NewMCPSecret(source)
	source[0] = 'X'
	if secret.bytes()[0] != 'M' {
		test.Fatal("secret borrows caller input")
	}
	clone := secret.clone()
	alias := secret
	secret.Clear()
	if alias.bytes() != nil || !bytes.Equal(clone.bytes(), []byte("MCP-PRIVATE-CANARY-638124")) {
		test.Fatal("alias/clone lifetime incorrect")
	}
	clone.Close()
	clone.Clear()
	if clone.bytes() != nil {
		test.Fatal("secret remained consumable")
	}
	envelope, err := mergeMCPEnvelope(mcpEnvelope{}, mcpDraft().Document, mcpReplacements())
	if err != nil {
		test.Fatal(err)
	}
	values := envelope.data().slots
	copied, err := newMCPEnvelope(envelope.data().document, values)
	if err != nil {
		test.Fatal(err)
	}
	encoded, err := envelope.encode()
	if err != nil {
		test.Fatal(err)
	}
	decoded, err := decodeMCPEnvelope(encoded)
	if err != nil {
		test.Fatal(err)
	}
	defer decoded.Close()
	if value, exists := decoded.data().slots["env/local/TOKEN"]; !exists || value.bytes() == nil || len(value.bytes()) != 0 {
		test.Fatal("present empty collapsed")
	}
	envelope.Close()
	envelope.Close()
	if envelope.data() != nil || len(values) != 0 || copied.data().slots["arg/local/first"].bytes() == nil {
		test.Fatal("envelope clone/close failed")
	}
	copied.Close()
	if _, err := copied.encode(); !errors.Is(err, ErrInvalid) {
		test.Fatal("closed encode accepted")
	}
}

func TestMCPEnvelopeAggregateAndUnionLimits(test *testing.T) {
	document := agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{}}
	for index := range 8 {
		id := fmt.Sprintf("server%d", index)
		server := agentconfig.MCPServer{ID: id, Name: id, Enabled: new(false), Stdio: &agentconfig.MCPStdio{Command: "node", Args: &agentconfig.MCPArgs{Values: []agentconfig.MCPArg{}}}}
		for argument := range 64 {
			name := fmt.Sprintf("old%d", argument)
			server.Stdio.Args.Values = append(server.Stdio.Args.Values, agentconfig.MCPArg{ID: name, ValueRef: "arg/" + id + "/" + name})
		}
		document.Servers = append(document.Servers, server)
	}
	current, err := mergeMCPEnvelope(mcpEnvelope{}, document, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer current.Close()
	next, _ := agentconfig.CloneMCPDocument(document)
	mutations := []MCPSecretMutation{}
	for index := range next.Servers {
		for argument := range next.Servers[index].Stdio.Args.Values {
			previous := next.Servers[index].Stdio.Args.Values[argument].ValueRef
			name := fmt.Sprintf("new%d", argument)
			ref := "arg/" + next.Servers[index].ID + "/" + name
			next.Servers[index].Stdio.Args.Values[argument] = agentconfig.MCPArg{ID: name, ValueRef: ref}
			mutations = append(mutations, MCPSecretMutation{Slot: previous, Action: Clear, Confirmed: true}, MCPSecretMutation{Slot: ref, Action: Replace, Replacement: NewMCPSecret([]byte{})})
		}
	}
	defer clearMCPMutations(mutations)
	merged, err := mergeMCPEnvelope(current, next, mutations)
	if err != nil || len(merged.data().slots) != 512 {
		test.Fatal("legal 1024 union mutations rejected")
	}
	defer merged.Close()
	oversized := append(append([]MCPSecretMutation{}, mutations...), MCPSecretMutation{Slot: "alien", Action: Clear, Confirmed: true})
	if result, err := mergeMCPEnvelope(current, next, oversized); !errors.Is(err, ErrLimit) {
		result.Close()
		test.Fatal("union budget not enforced")
	}
	big := []MCPSecretMutation{}
	for index, slot := range mutations {
		if slot.Action != Replace {
			continue
		}
		big = append(big, MCPSecretMutation{Slot: slot.Slot, Action: Replace, Replacement: NewMCPSecret(bytes.Repeat([]byte{'k'}, 8192))})
		if index >= 31 {
			break
		}
	}
	defer clearMCPMutations(big)
	exact, err := mergeMCPEnvelope(mcpEnvelope{}, next, big)
	if err != nil {
		test.Fatal("128KiB aggregate rejected")
	}
	exact.Close()
	extra := MCPSecretMutation{Slot: mutations[33].Slot, Action: Replace, Replacement: NewMCPSecret([]byte{'k'})}
	defer extra.Replacement.Close()
	big = append(big, extra)
	if result, err := mergeMCPEnvelope(mcpEnvelope{}, next, big); !errors.Is(err, ErrLimit) {
		result.Close()
		test.Fatal("aggregate secret limit not enforced")
	}
}

func TestMCPEnvelopeMutationGuards(test *testing.T) {
	current, err := mergeMCPEnvelope(mcpEnvelope{}, mcpDraft().Document, mcpReplacements())
	if err != nil {
		test.Fatal(err)
	}
	defer current.Close()
	document := mcpDraft().Document
	document.Servers[0].Stdio.Args = nil
	cases := [][]MCPSecretMutation{
		nil,
		{{Slot: "arg/local/first", Action: Clear}},
		{{Slot: "arg/local/first", Action: Keep}},
		{{Slot: "arg/local/first", Action: Replace, Replacement: NewMCPSecret([]byte("replacement"))}},
		{{Slot: "env/foreign/TOKEN", Action: Clear, Confirmed: true}},
		{{Slot: "arg/local/first", Action: Clear, Confirmed: true}, {Slot: "arg/local/first", Action: Clear, Confirmed: true}},
	}
	for index, mutations := range cases {
		result, err := mergeMCPEnvelope(current, document, mutations)
		clearMCPMutations(mutations)
		if err == nil || result.data() != nil {
			result.Close()
			test.Fatalf("invalid old/new union accepted case %d", index)
		}
	}
	result, err := mergeMCPEnvelope(current, document, []MCPSecretMutation{{Slot: "arg/local/first", Action: Clear, Confirmed: true}})
	if err != nil {
		test.Fatal(err)
	}
	defer result.Close()
	if len(result.data().slots) != 1 || current.data().slots["arg/local/first"].bytes() == nil {
		test.Fatal("clear leaked into original or retained removed slot")
	}
}

func TestMCPEnvelopeValueLimits(test *testing.T) {
	for _, value := range [][]byte{bytes.Repeat([]byte{'a'}, 8193), {0}, {0xff}} {
		mutations := []MCPSecretMutation{{Slot: "arg/local/first", Action: Replace, Replacement: NewMCPSecret(value)}}
		result, err := mergeMCPEnvelope(mcpEnvelope{}, mcpDraft().Document, mutations)
		clearMCPMutations(mutations)
		if err == nil {
			result.Close()
			test.Fatal("invalid slot bytes accepted")
		}
	}
	document := agentconfig.MCPDocumentV1{Servers: []agentconfig.MCPServer{{ID: "remote", Name: "remote", Enabled: new(true), HTTP: &agentconfig.MCPRemote{URL: "https://example.invalid", Headers: &agentconfig.MCPNamedSlots{Values: []agentconfig.MCPNamedSlot{{Name: "X-Token", ValueRef: "header/remote/X-Token"}}}}}}}
	mutations := []MCPSecretMutation{{Slot: "header/remote/X-Token", Action: Replace, Replacement: NewMCPSecret([]byte("a\r\nb"))}}
	if result, err := mergeMCPEnvelope(mcpEnvelope{}, document, mutations); err == nil {
		result.Close()
		test.Fatal("header injection accepted")
	}
	clearMCPMutations(mutations)
}
