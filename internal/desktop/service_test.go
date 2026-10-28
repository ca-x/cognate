package desktop

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"
)

func TestLogoutResponseIsBounded(test *testing.T) {
	body := protowire.AppendTag(nil, 1, protowire.BytesType)
	body = protowire.AppendBytes(body, bytes.Repeat([]byte{0}, 1<<20))
	server := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Type", "application/proto")
		_, _ = response.Write(body)
	}))
	defer server.Close()
	service := &Service{HTTP: server.Client()}
	err := service.Logout(test.Context(), server.URL, strings.Repeat("A", 43)+"."+strings.Repeat("B", 43))
	if err == nil || !strings.Contains(err.Error(), "resource_exhausted") {
		test.Fatalf("oversized logout response was not rejected: %v", err)
	}
}
