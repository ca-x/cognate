package httpapi

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	githubv1 "github.com/ca-x/cognate/gen/skillhub/github/v1"
	"github.com/ca-x/cognate/gen/skillhub/github/v1/githubv1connect"
	"github.com/ca-x/cognate/internal/github"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/githubauth"
	"github.com/ca-x/cognate/internal/server/state"
)

type reauthorizeFixture struct {
	store      *state.Store
	database   *sql.DB
	client     githubv1connect.GitHubServiceClient
	users      [2]string
	sessions   [2]string
	generation string
	calls      atomic.Int64
}

func newReauthorizeFixture(test *testing.T) *reauthorizeFixture {
	test.Helper()
	fixture := &reauthorizeFixture{}
	directory := test.TempDir()
	if err := os.Chmod(directory, 0o700); err != nil {
		test.Fatal(err)
	}
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	path := filepath.Join(directory, "server.db")
	store, err := state.Open(test.Context(), path, key)
	if err != nil {
		test.Fatal(err)
	}
	fixture.store = store
	test.Cleanup(func() { _ = store.Close() })
	service, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	for index, name := range []string{"alice", "bob"} {
		password := []byte(rand.Text())
		fixture.users[index], err = service.Register(test.Context(), name, password)
		if err != nil {
			test.Fatal(err)
		}
		session, err := service.Login(test.Context(), name, password)
		clear(password)
		if err != nil {
			test.Fatal(err)
		}
		fixture.sessions[index] = session.Reveal()
	}
	oldToken, err := github.NewToken("synthetic-restored-secret", nil)
	if err != nil {
		test.Fatal(err)
	}
	fixture.generation, err = store.PutCredential(test.Context(), fixture.users[0], "old-viewer", "", oldToken)
	if err != nil {
		test.Fatal(err)
	}
	fixture.database, err = sql.Open("sqlite3", path)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = fixture.database.Close() })
	fixture.status(test, "needs_reauthorization")
	now := time.Now().UTC()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{"INSERT INTO git_hub_lists (user_id,viewer_id,list_key,credential_generation,name,slug,private,updated_at,last_added_at,verified_at,subscribed,visible) SELECT user_id,viewer_id,'old-list',generation,'old-list','old-list',1,?,?,?,1,1 FROM git_hub_credentials WHERE generation = ?", []any{now, now, now, fixture.generation}},
		{"INSERT INTO git_hub_repositories (user_id,viewer_id,repository_key,name_with_owner,private) SELECT user_id,viewer_id,'old-repo','org/old-private',1 FROM git_hub_credentials WHERE generation = ?", []any{fixture.generation}},
		{"INSERT INTO git_hub_memberships (list_id,repository_id,visible) SELECT lists.id,repositories.id,1 FROM git_hub_lists AS lists JOIN git_hub_repositories AS repositories ON lists.user_id = repositories.user_id", nil},
	} {
		if _, err := fixture.database.ExecContext(test.Context(), statement.query, statement.args...); err != nil {
			test.Fatal(err)
		}
	}
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fixture.calls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/graphql":
			if request.Header.Get("Authorization") != "Bearer synthetic-new-pat" && request.Header.Get("Authorization") != "Bearer synthetic-device-access" {
				test.Error("upstream received an unapproved credential")
				writer.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = io.WriteString(writer, `{"data":{"viewer":{"id":"new-viewer","lists":{"pageInfo":{"hasNextPage":false,"endCursor":null},"nodes":[]}}}}`)
		case "/login/device/code":
			if err := request.ParseForm(); err != nil || request.Form.Get("client_id") != "synthetic-client" {
				test.Error("invalid device request")
			}
			_, _ = io.WriteString(writer, `{"device_code":"synthetic-device-secret","user_code":"ABCD-EFGH","verification_uri":"https://github.com/login/device","expires_in":900,"interval":1}`)
		case "/login/oauth/access_token":
			if err := request.ParseForm(); err != nil || request.Form.Get("device_code") != "synthetic-device-secret" || request.Form.Get("client_id") != "synthetic-client" {
				test.Error("invalid device poll")
			}
			_, _ = io.WriteString(writer, `{"access_token":"synthetic-device-access","token_type":"bearer","scope":"user"}`)
		default:
			test.Error("unexpected synthetic upstream route")
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	test.Cleanup(upstream.Close)
	target, err := url.Parse(upstream.URL)
	if err != nil {
		test.Fatal(err)
	}
	transport := upstream.Client().Transport
	client := &http.Client{Transport: graphTransport(func(request *http.Request) (*http.Response, error) {
		if request.URL.Scheme != "https" || (request.URL.Host != "api.github.com" && request.URL.Host != "github.com") {
			return nil, errors.New("unexpected upstream origin")
		}
		cloned := request.Clone(request.Context())
		cloned.URL = request.URL.Clone()
		cloned.URL.Scheme, cloned.URL.Host = target.Scheme, target.Host
		cloned.Host = target.Host
		return transport.RoundTrip(cloned)
	})}
	device, err := githubauth.NewService(store, "synthetic-client", client)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(device.Close)
	server := httptest.NewServer(NewWithOptions(service, Options{Store: store, GitHubClient: client, GitHubDevice: device}))
	test.Cleanup(server.Close)
	fixture.client = githubv1connect.NewGitHubServiceClient(server.Client(), server.URL)
	return fixture
}

func (fixture *reauthorizeFixture) status(test *testing.T, status string) {
	test.Helper()
	if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET status = ? WHERE generation = ?", status, fixture.generation); err != nil {
		test.Fatal(err)
	}
}

func TestReauthorizeCatalogNeedsMetadata(test *testing.T) {
	fixture := newReauthorizeFixture(test)
	request := connect.NewRequest(&githubv1.GetCatalogRequest{})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	response, err := fixture.client.GetCatalog(test.Context(), request)
	if err != nil {
		test.Fatalf("restored catalog cannot supply expected generation: %v", err)
	}
	catalog := response.Msg.Catalog
	if !catalog.AuthorizationRequired || catalog.Connected || !catalog.DeviceFlowEnabled || catalog.CredentialGeneration != fixture.generation || catalog.ViewerId != "old-viewer" || len(catalog.Lists) != 0 || len(catalog.Repositories) != 0 || fixture.calls.Load() != 0 {
		test.Fatal("restored catalog leaked content or failed to provide passive identity")
	}
}

func TestReauthorizePATThroughConnect(test *testing.T) {
	fixture := newReauthorizeFixture(test)
	request := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: "synthetic-new-pat", ExpectedGeneration: fixture.generation})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	response, err := fixture.client.ConnectToken(test.Context(), request)
	if err != nil {
		test.Fatalf("verified PAT could not replace restored credential: %v", err)
	}
	credential, err := fixture.store.Credential(test.Context(), fixture.users[0])
	if err != nil || credential.Generation == fixture.generation || credential.Generation != response.Msg.CredentialGeneration || credential.ViewerID != "new-viewer" || credential.Token.Reveal() != "synthetic-new-pat" || fixture.calls.Load() != 2 {
		test.Fatal("verified PAT was not committed as a fresh ready generation")
	}
}

func TestReauthorizeDeviceThroughConnect(test *testing.T) {
	fixture := newReauthorizeFixture(test)
	catalogRequest := connect.NewRequest(&githubv1.GetCatalogRequest{})
	catalogRequest.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	catalog, err := fixture.client.GetCatalog(test.Context(), catalogRequest)
	if err != nil || !catalog.Msg.GetCatalog().GetAuthorizationRequired() || catalog.Msg.Catalog.CredentialGeneration != fixture.generation || fixture.calls.Load() != 0 {
		test.Fatal("device flow could not obtain restored passive identity")
	}
	request := connect.NewRequest(&githubv1.StartDeviceAuthorizationRequest{ExpectedGeneration: catalog.Msg.Catalog.CredentialGeneration})
	request.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	started, err := fixture.client.StartDeviceAuthorization(test.Context(), request)
	if err != nil {
		test.Fatalf("restored device authorization rejected before upstream: %v", err)
	}
	lookup := connect.NewRequest(&githubv1.GetDeviceAuthorizationRequest{AuthorizationId: started.Msg.Authorization.Id})
	lookup.Header().Set("Authorization", "Bearer "+fixture.sessions[1])
	if _, err := fixture.client.GetDeviceAuthorization(test.Context(), lookup); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("other owner could observe device authorization")
	}
	cancel := connect.NewRequest(&githubv1.CancelDeviceAuthorizationRequest{AuthorizationId: started.Msg.Authorization.Id})
	cancel.Header().Set("Authorization", "Bearer "+fixture.sessions[1])
	if _, err := fixture.client.CancelDeviceAuthorization(test.Context(), cancel); connect.CodeOf(err) != connect.CodeNotFound {
		test.Fatal("other owner could cancel device authorization")
	}
	lookup.Header().Set("Authorization", "Bearer "+fixture.sessions[0])
	deadline := time.Now().Add(6 * time.Second)
	for {
		result, err := fixture.client.GetDeviceAuthorization(test.Context(), lookup)
		if err != nil {
			test.Fatal(err)
		}
		if result.Msg.Authorization.State != githubv1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_PENDING {
			if result.Msg.Authorization.State != githubv1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_COMPLETE || result.Msg.Authorization.UserCode != "" || result.Msg.Authorization.CredentialGeneration == fixture.generation {
				test.Fatal("device authorization did not complete a fresh generation")
			}
			credential, err := fixture.store.Credential(test.Context(), fixture.users[0])
			if err != nil || credential.Generation != result.Msg.Authorization.CredentialGeneration || credential.ViewerID != "new-viewer" || credential.Token.ClientID() != "synthetic-client" || credential.Token.Reveal() != "synthetic-device-access" || fixture.calls.Load() != 4 {
				test.Fatal("device authorization skipped verified token persistence")
			}
			break
		}
		if time.Now().After(deadline) {
			test.Fatal("device authorization completion timed out")
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := fixture.client.GetCatalog(test.Context(), catalogRequest)
	if err != nil || !response.Msg.GetCatalog().GetConnected() || response.Msg.Catalog.AuthorizationRequired || response.Msg.Catalog.ViewerId != "new-viewer" || len(response.Msg.Catalog.Lists) != 0 || len(response.Msg.Catalog.Repositories) != 0 {
		test.Fatal("new ready catalog was not readable")
	}
}

func (fixture *reauthorizeFixture) credentialDigest(test *testing.T) [32]byte {
	test.Helper()
	rows, err := fixture.database.QueryContext(test.Context(), "SELECT id,user_id,viewer_id,client_id,key_id,generation,ciphertext,status,refresh_attempt,sync_sequence,sync_applied_sequence,sync_fetch_id,collection_cursor FROM git_hub_credentials ORDER BY id")
	if err != nil {
		test.Fatal(err)
	}
	defer rows.Close()
	var records [][]any
	for rows.Next() {
		values := make([]any, 13)
		pointers := make([]any, len(values))
		for index := range values {
			pointers[index] = &values[index]
		}
		if err := rows.Scan(pointers...); err != nil {
			test.Fatal(err)
		}
		records = append(records, values)
	}
	if err := rows.Err(); err != nil {
		test.Fatal(err)
	}
	data, err := json.Marshal(records)
	if err != nil {
		test.Fatal(err)
	}
	defer clear(data)
	return sha256.Sum256(data)
}

func TestReauthorizeDeviceRejectsWrongAuthority(test *testing.T) {
	for _, scenario := range []string{"missing-generation", "stale-generation", "refreshing", "unknown", "other-owner"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newReauthorizeFixture(test)
			expected, session := fixture.generation, fixture.sessions[0]
			want := connect.CodeAborted
			switch scenario {
			case "missing-generation":
				expected = ""
			case "stale-generation":
				expected = "stale-generation"
			case "refreshing", "unknown":
				fixture.status(test, scenario)
				want = connect.CodeFailedPrecondition
			case "other-owner":
				session = fixture.sessions[1]
			}
			before := fixture.credentialDigest(test)
			request := connect.NewRequest(&githubv1.StartDeviceAuthorizationRequest{ExpectedGeneration: expected})
			request.Header().Set("Authorization", "Bearer "+session)
			if _, err := fixture.client.StartDeviceAuthorization(test.Context(), request); connect.CodeOf(err) != want {
				test.Fatalf("wrong authority category: %v", err)
			}
			if fixture.calls.Load() != 0 || before != fixture.credentialDigest(test) {
				test.Fatal("wrong device authority contacted upstream or changed committed state")
			}
		})
	}
}

func TestReauthorizeCatalogIsolationAndNoSecretRead(test *testing.T) {
	for _, scenario := range []string{"damaged-ciphertext", "refreshing", "unknown", "blank-viewer", "blank-generation", "other-owner"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newReauthorizeFixture(test)
			session := fixture.sessions[0]
			want := connect.CodeFailedPrecondition
			switch scenario {
			case "damaged-ciphertext":
				if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET ciphertext = ?", []byte{1}); err != nil {
					test.Fatal(err)
				}
				want = connect.CodeUnknown
			case "refreshing", "unknown":
				fixture.status(test, scenario)
			case "blank-viewer":
				if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET viewer_id = ''"); err != nil {
					test.Fatal(err)
				}
			case "blank-generation":
				if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET generation = ''"); err != nil {
					test.Fatal(err)
				}
			case "other-owner":
				session = fixture.sessions[1]
				want = connect.CodeUnknown
			}
			before := fixture.credentialDigest(test)
			request := connect.NewRequest(&githubv1.GetCatalogRequest{})
			request.Header().Set("Authorization", "Bearer "+session)
			response, err := fixture.client.GetCatalog(test.Context(), request)
			if want == connect.CodeUnknown {
				if err != nil || response.Msg.Catalog.Connected || len(response.Msg.Catalog.Lists) != 0 || len(response.Msg.Catalog.Repositories) != 0 {
					test.Fatal("passive catalog leaked content or needed old secret")
				}
				if scenario == "other-owner" {
					if response.Msg.Catalog.CredentialGeneration != "" || response.Msg.Catalog.ViewerId != "" || response.Msg.Catalog.AuthorizationRequired {
						test.Fatal("other owner learned restored identity")
					}
				} else if !response.Msg.Catalog.AuthorizationRequired || response.Msg.Catalog.CredentialGeneration != fixture.generation || response.Msg.Catalog.ViewerId != "old-viewer" {
					test.Fatal("metadata path tried to decrypt damaged old ciphertext")
				}
			} else if connect.CodeOf(err) != want || response != nil {
				test.Fatal("unavailable identity did not fail closed")
			}
			if before != fixture.credentialDigest(test) || fixture.calls.Load() != 0 {
				test.Fatal("catalog changed credentials or contacted upstream")
			}
		})
	}
}

func TestReauthorizePATRejectsWithoutCommittedWrites(test *testing.T) {
	for _, scenario := range []string{"missing-generation", "stale-generation", "refreshing", "unknown", "other-owner", "viewer-aad", "damaged-ciphertext"} {
		test.Run(scenario, func(test *testing.T) {
			fixture := newReauthorizeFixture(test)
			expected, session := fixture.generation, fixture.sessions[0]
			want := connect.CodeAborted
			switch scenario {
			case "missing-generation":
				expected = ""
			case "stale-generation":
				expected = "stale-generation"
			case "refreshing", "unknown":
				fixture.status(test, scenario)
			case "other-owner":
				session = fixture.sessions[1]
			case "viewer-aad":
				if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET viewer_id = 'tampered-viewer'"); err != nil {
					test.Fatal(err)
				}
				want = connect.CodeInternal
			case "damaged-ciphertext":
				if _, err := fixture.database.ExecContext(test.Context(), "UPDATE git_hub_credentials SET ciphertext = ?", []byte{1}); err != nil {
					test.Fatal(err)
				}
				want = connect.CodeInternal
			}
			before := fixture.credentialDigest(test)
			request := connect.NewRequest(&githubv1.ConnectTokenRequest{AccessToken: "synthetic-new-pat", ExpectedGeneration: expected})
			request.Header().Set("Authorization", "Bearer "+session)
			response, err := fixture.client.ConnectToken(test.Context(), request)
			if connect.CodeOf(err) != want || response != nil {
				test.Fatalf("wrong replacement failure category: %v", err)
			}
			if strings.Contains(err.Error(), "synthetic-new-pat") || strings.Contains(err.Error(), "synthetic-restored-secret") || strings.Contains(err.Error(), "tampered-viewer") || before != fixture.credentialDigest(test) || fixture.calls.Load() != 2 {
				test.Fatal("failed PAT replacement changed state, disclosed secrets, or skipped new-token verification")
			}
		})
	}
}
