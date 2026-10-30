package agentconfig

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestMCPRemoteURLClassification(test *testing.T) {
	for _, transport := range []string{"http", "sse"} {
		test.Run(transport, func(test *testing.T) {
			for _, entry := range []struct {
				name string
				url  string
				want error
			}{
				{"plain", "https://mcp.example.invalid/rpc", nil},
				{"encoded-path", "https://mcp.example.invalid/rpc%3Ftenant", nil},
				{"query", "https://mcp.example.invalid/rpc?tenant=synthetic", ErrMCPUnsupported},
				{"empty-query", "https://mcp.example.invalid/rpc?", ErrMCPUnsupported},
				{"fragment", "https://mcp.example.invalid/rpc#section", ErrMCPInvalid},
				{"empty-fragment", "https://mcp.example.invalid/rpc#", ErrMCPInvalid},
				{"userinfo-query", "https://synthetic@mcp.example.invalid/rpc?tenant=synthetic", ErrMCPInvalid},
				{"relative-query", "/rpc?tenant=synthetic", ErrMCPInvalid},
				{"invalid-escape", "https://mcp.example.invalid/%zz?tenant=synthetic", ErrMCPInvalid},
			} {
				test.Run(entry.name, func(test *testing.T) {
					remote := &MCPRemote{URL: entry.url}
					server := MCPServer{ID: "remote", Name: "remote", Enabled: new(true)}
					if transport == "http" {
						server.HTTP = remote
					} else {
						server.SSE = remote
					}
					document := MCPDocumentV1{Servers: []MCPServer{server}}
					if _, err := ValidateMCPDocument(document); !errors.Is(err, entry.want) {
						test.Fatal("typed URL classification differs")
					}
					encoded, err := json.Marshal(document)
					if err != nil {
						test.Fatal("encode declaration failed")
					}
					if _, err := DecodeMCPDocument(encoded); !errors.Is(err, entry.want) {
						test.Fatal("decoded URL classification differs")
					}
				})
			}
		})
	}
}

func TestMCPShapePositive(test *testing.T) {
	document := MCPDocumentV1{Servers: []MCPServer{{ID: "local", Name: "local", Enabled: new(false), Stdio: &MCPStdio{Command: "node", Args: &MCPArgs{Values: []MCPArg{{ID: "first", ValueRef: "arg/local/first"}}}, Env: &MCPNamedSlots{Values: []MCPNamedSlot{{Name: "TOKEN", ValueRef: "env/local/TOKEN"}}}}}, {ID: "remote", Name: "remote", Enabled: new(true), HTTP: &MCPRemote{URL: "https://example.invalid/mcp", Headers: &MCPNamedSlots{Values: []MCPNamedSlot{{Name: "Authorization", ValueRef: "header/remote/Authorization"}}}}}, {ID: "legacy", Name: "legacy", Enabled: new(true), SSE: &MCPRemote{URL: "http://localhost:4321/sse"}}}}
	slots, err := ValidateMCPDocument(document)
	if err != nil || len(slots) != 3 {
		test.Fatalf("complete three transport declaration rejected: %v", err)
	}
}

func TestMCPShapeCompleteBudgets(test *testing.T) {
	for _, count := range []int{512, 513} {
		document := MCPDocumentV1{Servers: []MCPServer{}}
		for offset := 0; offset < count; {
			server := MCPServer{ID: fmt.Sprintf("server%d", len(document.Servers)), Name: fmt.Sprintf("server%d", len(document.Servers)), Enabled: new(false), Stdio: &MCPStdio{Command: "node", Args: &MCPArgs{Values: []MCPArg{}}}}
			for len(server.Stdio.Args.Values) < 64 && offset < count {
				id := fmt.Sprintf("arg%d", offset)
				server.Stdio.Args.Values = append(server.Stdio.Args.Values, MCPArg{ID: id, ValueRef: "arg/" + server.ID + "/" + id})
				offset++
			}
			document.Servers = append(document.Servers, server)
		}
		slots, err := ValidateMCPDocument(document)
		if count == 512 && (err != nil || len(slots) != 512) {
			test.Fatal("512 slots rejected")
		}
		if count == 513 && err != ErrMCPLimit {
			test.Fatal("513 slots accepted")
		}
	}
	document := MCPDocumentV1{Servers: []MCPServer{}}
	for index := range 33 {
		document.Servers = append(document.Servers, MCPServer{ID: fmt.Sprintf("s%d", index), Name: fmt.Sprintf("s%d", index), Enabled: new(false), Stdio: &MCPStdio{Command: "node"}})
	}
	if _, err := ValidateMCPDocument(document); err != ErrMCPLimit {
		test.Fatal("33 servers accepted")
	}
	for _, raw := range []string{`{}`, `{"servers":null}`, `{"servers":[{"id":"local","name":"local","enabled":false,"stdio":{"command":"node","args":null}}]}`} {
		if _, err := DecodeMCPDocument([]byte(raw)); err == nil {
			test.Fatal("absence/null silently normalized")
		}
	}
	if _, err := DecodeMCPDocument([]byte(strings.Repeat(" ", MaxMCPDocumentBytes+1))); err != ErrMCPLimit {
		test.Fatal("raw document budget ignored")
	}
}

func TestMCPShapeGolden(test *testing.T) {
	data, err := os.ReadFile("testdata/mcp-v1-shapes.json")
	if err != nil {
		test.Fatal(err)
	}
	var cases []struct {
		Name     string         `json:"name"`
		Document jsontext.Value `json:"document"`
		Valid    bool           `json:"valid"`
	}
	if err := json.Unmarshal(data, &cases); err != nil {
		test.Fatal(err)
	}
	for _, entry := range cases {
		test.Run(entry.Name, func(test *testing.T) {
			_, err := DecodeMCPDocument(entry.Document)
			if (err == nil) != entry.Valid {
				test.Fatalf("shape acceptance differs: %v", err)
			}
		})
	}
}

func TestMCPShapePresenceAndLimits(test *testing.T) {
	original := MCPDocumentV1{Servers: []MCPServer{{ID: "local", Name: "local", Enabled: new(false), Stdio: &MCPStdio{Command: "node", Args: &MCPArgs{Values: []MCPArg{}}}}}}
	cloned, err := CloneMCPDocument(original)
	if err != nil || cloned.Servers[0].Stdio.Args == nil || *cloned.Servers[0].Enabled {
		test.Fatal("lost empty/false presence")
	}
	*original.Servers[0].Enabled = true
	if *cloned.Servers[0].Enabled {
		test.Fatal("clone aliases enabled")
	}
	cloned.Servers[0].Enabled = nil
	if _, err := ValidateMCPDocument(cloned); err == nil {
		test.Fatal("missing enabled accepted")
	}
	original.Servers[0].Stdio.Args = &MCPArgs{Values: make([]MCPArg, 65)}
	if _, err := ValidateMCPDocument(original); err != ErrMCPLimit {
		test.Fatal("args budget bypass")
	}
}
