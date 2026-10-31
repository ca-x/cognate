package cli

import (
	"slices"
	"strings"
	"testing"
)

func TestAuthRunGrokRoutes(test *testing.T) {
	for _, route := range [][]string{
		{"grok-assets", "list"}, {"grok-assets", "get"}, {"grok-assets", "read"},
		{"grok-assets", "create"}, {"grok-assets", "update"}, {"grok-assets", "copy"},
		{"grok-assets", "delete"}, {"grok-assets", "order", "get"}, {"grok-assets", "order", "replace"},
	} {
		test.Run(strings.Join(route, "/"), func(test *testing.T) {
			arguments := slices.Concat([]string{"run", "--server", "https://example.test", "--"}, route)
			origin, child, native, err := authArguments(arguments)
			if err != nil || origin != "https://example.test" || !slices.Equal(child, route) || native {
				test.Fatal("owner Grok asset route did not reach the isolated account child")
			}
		})
	}
	for _, route := range [][]string{
		{"grok-assets", "preview"}, {"grok-assets", "apply"}, {"grok-assets", "order"},
		{"grok-assets", "order", "set"}, {"grok-assets", "order", "--help"},
		{"grok-assets", "list", "--server=https://other.test"},
		{"grok-assets", "list", "--token-stdin=false"}, {"grok-assets", "list", "--owner", "other"},
	} {
		arguments := slices.Concat([]string{"run", "--server", "https://example.test", "--"}, route)
		if _, _, _, err := authArguments(arguments); err == nil {
			test.Fatal("unsafe Grok account route admitted")
		}
	}
}
