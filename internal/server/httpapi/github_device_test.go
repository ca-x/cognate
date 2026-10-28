package httpapi

import (
	"crypto/rand"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	githubv1 "github.com/ca-x/skillhub/gen/skillhub/github/v1"
	"github.com/ca-x/skillhub/gen/skillhub/github/v1/githubv1connect"
	"github.com/ca-x/skillhub/internal/github"
	"github.com/ca-x/skillhub/internal/server/githubauth"
)

func TestDeviceAuthorizationHTTPIsolationAndCancellation(t *testing.T) {
	auth, store := testServiceAndStore(t)
	deviceCode := rand.Text()
	upstream := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.String() != "https://github.com/login/device/code" {
			return nil, errors.New("unexpected request")
		}
		if err := request.ParseForm(); err != nil {
			return nil, err
		}
		if request.Form.Get("scope") != "user" {
			return nil, errors.New("private scope not requested")
		}
		data, err := json.Marshal(map[string]any{"device_code": deviceCode, "user_code": "AAAA-BBBB", "verification_uri": "https://github.com/login/device", "expires_in": 900, "interval": 60})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(data)))}, nil
	})}
	device, err := githubauth.NewService(store, "test-client", upstream)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(device.Close)
	server := httptest.NewServer(NewWithOptions(auth, Options{Store: store, GitHubDevice: device}))
	t.Cleanup(server.Close)
	client := githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	if _, err := client.StartDeviceAuthorization(t.Context(), connect.NewRequest(&githubv1.StartDeviceAuthorizationRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("unauthenticated start: %v", err)
	}
	sessions := make([]string, 0, 2)
	for _, name := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		if _, err := auth.Register(t.Context(), name, password); err != nil {
			t.Fatal(err)
		}
		session, err := auth.Login(t.Context(), name, password)
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session.Reveal())
	}
	request := connect.NewRequest(&githubv1.StartDeviceAuthorizationRequest{RequestUserScope: true})
	request.Header().Set("Authorization", "Bearer "+sessions[0])
	started, err := client.StartDeviceAuthorization(t.Context(), request)
	if err != nil || started.Msg.GetAuthorization().GetState() != githubv1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_PENDING {
		t.Fatalf("start: %v", err)
	}
	encoded, err := json.Marshal(started.Msg)
	if err != nil || strings.Contains(string(encoded), deviceCode) {
		t.Fatal("device_code leaked to browser")
	}
	lookup := connect.NewRequest(&githubv1.GetDeviceAuthorizationRequest{AuthorizationId: started.Msg.Authorization.Id})
	lookup.Header().Set("Authorization", "Bearer "+sessions[1])
	if _, err := client.GetDeviceAuthorization(t.Context(), lookup); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-user lookup: %v", err)
	}
	cancel := connect.NewRequest(&githubv1.CancelDeviceAuthorizationRequest{AuthorizationId: started.Msg.Authorization.Id})
	cancel.Header().Set("Authorization", "Bearer "+sessions[1])
	if _, err := client.CancelDeviceAuthorization(t.Context(), cancel); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("cross-user cancel: %v", err)
	}
	cancel.Header().Set("Authorization", "Bearer "+sessions[0])
	canceled, err := client.CancelDeviceAuthorization(t.Context(), cancel)
	if err != nil || canceled.Msg.GetAuthorization().GetState() != githubv1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_CANCELED || canceled.Msg.Authorization.UserCode != "" {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestDeviceFlowDisabledIsExplicit(t *testing.T) {
	auth, store := testServiceAndStore(t)
	password := []byte(rand.Text())
	if _, err := auth.Register(t.Context(), "alice", password); err != nil {
		t.Fatal(err)
	}
	session, err := auth.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithOptions(auth, Options{Store: store}))
	t.Cleanup(server.Close)
	client := githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&githubv1.StartDeviceAuthorizationRequest{})
	request.Header().Set("Authorization", "Bearer "+session.Reveal())
	if _, err := client.StartDeviceAuthorization(t.Context(), request); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled flow: %v", err)
	}
}

func TestExpiredCredentialCanBeReauthorizedWithoutReturningOldContent(t *testing.T) {
	auth, store := testServiceAndStore(t)
	password := []byte(rand.Text())
	user, err := auth.Register(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	material, err := json.Marshal(map[string]any{"version": 1, "access": rand.Text(), "expires_at": time.Now().Add(-time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	defer clear(material)
	token, err := github.DecodeStoredToken(material)
	if err != nil {
		t.Fatal(err)
	}
	generation, err := store.PutCredential(t.Context(), user, "viewer", "", token)
	if err != nil {
		t.Fatal(err)
	}
	session, err := auth.Login(t.Context(), "alice", password)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(NewWithOptions(auth, Options{Store: store}))
	t.Cleanup(server.Close)
	client := githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	request := connect.NewRequest(&githubv1.GetCatalogRequest{})
	request.Header().Set("Authorization", "Bearer "+session.Reveal())
	response, err := client.GetCatalog(t.Context(), request)
	if err != nil || !response.Msg.GetCatalog().GetAuthorizationRequired() || response.Msg.Catalog.Connected || response.Msg.Catalog.CredentialGeneration != generation || len(response.Msg.Catalog.Lists) != 0 || len(response.Msg.Catalog.Repositories) != 0 {
		t.Fatalf("reauthorization status: %v", err)
	}
}
