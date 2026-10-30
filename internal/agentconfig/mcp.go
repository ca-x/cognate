package agentconfig

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	ClaudeMCP           Target     = "claude.mcp"
	ClaudeMCPPlanV1     Capability = "claude.mcp-plan.v1"
	MCPPluginID                    = "builtin.mcp.claude"
	MCPPluginVersion               = "1.0.0"
	MaxMCPDocumentBytes            = 64 << 10
	MaxMCPPlanBytes                = 128 << 10
	MaxMCPSlots                    = 512
)

var ErrMCPInvalid = errors.New("invalid MCP declaration")
var ErrMCPUnsupported = errors.New("unsupported MCP declaration")
var ErrMCPLimit = errors.New("MCP declaration capacity exhausted")

type MCPDocumentV1 struct {
	Servers []MCPServer `json:"servers"`
}
type MCPServer struct {
	ID      string     `json:"id"`
	Name    string     `json:"name"`
	Enabled *bool      `json:"enabled"`
	Stdio   *MCPStdio  `json:"stdio,omitzero"`
	HTTP    *MCPRemote `json:"http,omitzero"`
	SSE     *MCPRemote `json:"sse,omitzero"`
}
type MCPStdio struct {
	Command string         `json:"command"`
	Args    *MCPArgs       `json:"args,omitzero"`
	Env     *MCPNamedSlots `json:"env,omitzero"`
}
type MCPRemote struct {
	URL     string         `json:"url"`
	Headers *MCPNamedSlots `json:"headers,omitzero"`
}
type MCPArgs struct {
	Values []MCPArg `json:"values"`
}
type MCPArg struct {
	ID       string `json:"id"`
	ValueRef string `json:"valueRef"`
}
type MCPNamedSlots struct {
	Values []MCPNamedSlot `json:"values"`
}
type MCPNamedSlot struct {
	Name     string `json:"name"`
	ValueRef string `json:"valueRef"`
}
type MCPSlot struct {
	Ref     string `json:"ref"`
	Purpose string `json:"purpose"`
}
type MCPSlotStatus struct {
	Ref     string `json:"ref"`
	Present bool   `json:"present"`
}
type MCPPlanRequest struct {
	APIVersion       int             `json:"apiVersion"`
	PluginID         string          `json:"pluginID"`
	PluginVersion    string          `json:"pluginVersion"`
	DefinitionDigest string          `json:"definitionDigest"`
	Target           Target          `json:"target"`
	Capabilities     []Capability    `json:"capabilities"`
	Document         MCPDocumentV1   `json:"document"`
	Secrets          []MCPSlotStatus `json:"secrets"`
}
type MCPOperation struct {
	Action      string     `json:"action"`
	ServerID    string     `json:"serverID"`
	Name        string     `json:"name"`
	ManagedOnly bool       `json:"managedOnly"`
	Stdio       *MCPStdio  `json:"stdio,omitzero"`
	HTTP        *MCPRemote `json:"http,omitzero"`
	SSE         *MCPRemote `json:"sse,omitzero"`
}
type MCPDeclarationV1 struct {
	Version    int            `json:"version"`
	Target     Target         `json:"target"`
	Operations []MCPOperation `json:"operations"`
}

var mcpID = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
var mcpEnv = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var mcpHeader = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")

func ValidateMCPDocument(document MCPDocumentV1) ([]MCPSlot, error) {
	if len(document.Servers) > 32 {
		return nil, ErrMCPLimit
	}
	if document.Servers == nil {
		return nil, ErrMCPInvalid
	}
	slots := make([]MCPSlot, 0)
	ids, names, refs := map[string]bool{}, map[string]bool{}, map[string]bool{}
	add := func(ref, purpose, expected string) error {
		if ref != expected || len(ref) > 128 || refs[ref] {
			return ErrMCPInvalid
		}
		refs[ref] = true
		slots = append(slots, MCPSlot{Ref: ref, Purpose: purpose})
		if len(slots) > MaxMCPSlots {
			return ErrMCPLimit
		}
		return nil
	}
	named := func(server, purpose string, values *MCPNamedSlots, maximum int) error {
		if values == nil {
			return nil
		}
		if values.Values == nil {
			return ErrMCPInvalid
		}
		if len(values.Values) > maximum {
			return ErrMCPLimit
		}
		seen := map[string]bool{}
		for _, entry := range values.Values {
			key := entry.Name
			valid := mcpEnv.MatchString(key)
			if purpose == "header" {
				valid = mcpHeader.MatchString(key)
				key = strings.ToLower(key)
			}
			if len(entry.Name) > 128 || !valid || seen[key] {
				return ErrMCPInvalid
			}
			seen[key] = true
			if err := add(entry.ValueRef, purpose, purpose+"/"+server+"/"+entry.Name); err != nil {
				return err
			}
		}
		return nil
	}
	for _, server := range document.Servers {
		if !mcpID.MatchString(server.ID) || !mcpID.MatchString(server.Name) || ids[server.ID] || names[server.Name] || server.Enabled == nil {
			return nil, ErrMCPInvalid
		}
		ids[server.ID], names[server.Name] = true, true
		transports := 0
		if server.Stdio != nil {
			transports++
		}
		if server.HTTP != nil {
			transports++
		}
		if server.SSE != nil {
			transports++
		}
		if transports != 1 {
			return nil, ErrMCPInvalid
		}
		if server.Stdio != nil {
			stdio := server.Stdio
			if len(stdio.Command) == 0 || len(stdio.Command) > 4096 || !utf8.ValidString(stdio.Command) || strings.ContainsRune(stdio.Command, 0) {
				return nil, ErrMCPInvalid
			}
			if stdio.Args != nil {
				if stdio.Args.Values == nil {
					return nil, ErrMCPInvalid
				}
				if len(stdio.Args.Values) > 64 {
					return nil, ErrMCPLimit
				}
				for _, entry := range stdio.Args.Values {
					if !mcpID.MatchString(entry.ID) {
						return nil, ErrMCPInvalid
					}
					if err := add(entry.ValueRef, "arg", "arg/"+server.ID+"/"+entry.ID); err != nil {
						return nil, err
					}
				}
			}
			if err := named(server.ID, "env", stdio.Env, 64); err != nil {
				return nil, err
			}
		}
		for _, remote := range []*MCPRemote{server.HTTP, server.SSE} {
			if remote == nil {
				continue
			}
			parsed, err := url.Parse(remote.URL)
			if len(remote.URL) > 4096 || !utf8.ValidString(remote.URL) || err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" || strings.ContainsRune(remote.URL, '#') || (parsed.Scheme != "http" && parsed.Scheme != "https") {
				return nil, ErrMCPInvalid
			}
			if parsed.RawQuery != "" || parsed.ForceQuery {
				return nil, ErrMCPUnsupported
			}
			if err := named(server.ID, "header", remote.Headers, 32); err != nil {
				return nil, err
			}
		}
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return nil, ErrMCPInvalid
	}
	if len(encoded) > MaxMCPDocumentBytes {
		return nil, ErrMCPLimit
	}
	return slots, nil
}

func CloneMCPDocument(document MCPDocumentV1) (MCPDocumentV1, error) {
	if _, err := ValidateMCPDocument(document); err != nil {
		return MCPDocumentV1{}, err
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return MCPDocumentV1{}, ErrMCPInvalid
	}
	var cloned MCPDocumentV1
	if json.Unmarshal(encoded, &cloned, json.RejectUnknownMembers(true)) != nil {
		return MCPDocumentV1{}, ErrMCPInvalid
	}
	return cloned, nil
}

func DecodeMCPDocument(encoded []byte) (MCPDocumentV1, error) {
	if len(encoded) > MaxMCPDocumentBytes {
		return MCPDocumentV1{}, ErrMCPLimit
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(encoded))
	for decoder.InputOffset() < int64(len(encoded)) {
		token, err := decoder.ReadToken()
		if err != nil {
			break
		}
		if token.Kind() == 'n' {
			return MCPDocumentV1{}, ErrMCPInvalid
		}
	}
	var document MCPDocumentV1
	if json.Unmarshal(encoded, &document, json.RejectUnknownMembers(true)) != nil {
		return MCPDocumentV1{}, ErrMCPUnsupported
	}
	if _, err := ValidateMCPDocument(document); err != nil {
		return MCPDocumentV1{}, err
	}
	return document, nil
}
