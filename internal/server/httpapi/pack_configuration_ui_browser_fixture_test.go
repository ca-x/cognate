package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/json/v2"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
	"google.golang.org/protobuf/encoding/protojson"
)

type packConfigurationReceiptWriter struct {
	*httptest.ResponseRecorder
	target http.ResponseWriter
}

func (writer packConfigurationReceiptWriter) Unwrap() http.ResponseWriter { return writer.target }

func TestPackConfigurationUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_PACK_CONFIGURATION_UI_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated owner attachment browser fixture")
	}
	root, err := geminiBrowserOpenRoot(directory)
	if err != nil {
		test.Fatal(err)
	}
	defer root.Close()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		test.Fatal(err)
	}
	defer clear(key)
	storage := test.TempDir()
	if err := os.Chmod(storage, 0o700); err != nil {
		test.Fatal(err)
	}
	store, err := state.Open(test.Context(), filepath.Join(storage, "attachments.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	password := rand.Text()
	owner, err := authentication.Register(test.Context(), "attachment-owner", []byte(password))
	if err != nil {
		test.Fatal("synthetic owner unavailable")
	}
	other, err := authentication.Register(test.Context(), "attachment-other", []byte(password))
	if err != nil {
		test.Fatal("synthetic other owner unavailable")
	}
	archive := libraryArchive(test, "---\nname: Attachment review\ndescription: Synthetic skill, unchanged by configuration metadata.\n---\nSynthetic instructions.\n")
	upstream := &http.Client{Transport: graphTransport(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(archive))}, nil
	})}
	cache, err := library.OpenArchiveCache(filepath.Join(test.TempDir(), "archives"))
	if err != nil {
		test.Fatal(err)
	}
	collection, err := library.NewService(store, cache, repository.NewArchiveFetcher(upstream))
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = collection.Close() })
	repo, err := collection.Collect(test.Context(), owner, "synthetic/attachments", "main")
	if err != nil {
		test.Fatal(err)
	}
	repo, err = collection.Get(test.Context(), owner, repo.ID)
	if err != nil || len(repo.Skills) != 1 {
		test.Fatal("synthetic skill unavailable")
	}
	draft := state.PackDraft{Name: "Attachment review · 配置审阅", Items: []state.PackSelection{{SkillID: repo.Skills[0].ID}}}
	baseline, err := store.CreatePack(test.Context(), owner, draft)
	if err != nil {
		test.Fatal(err)
	}
	draft.Name = "Alternate pack · 另一组合"
	alternate, err := store.CreatePack(test.Context(), owner, draft)
	if err != nil {
		test.Fatal(err)
	}
	domain := configuration.New(store)
	assets := make([]state.ConfigurationAsset, 0, 18)
	for index, agent := range []string{"claude", "codex", "gemini", "grokbuild", "opencode", "claude-desktop", "openclaw", "hermes", "pi", "mcode", "kimi", "dsh", "agy", "omp", "claude", "codex", "gemini", "grokbuild"} {
		asset, err := store.CreateConfigurationAsset(test.Context(), owner, state.ConfigurationAssetDraft{AgentID: agent, Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: "Synthetic " + agent + " " + string(rune('A'+index)), Payload: state.NewConfigurationPayload([]byte("PRIVATE_ATTACHMENT_BODY_CANARY"))})
		if err != nil {
			test.Fatal("synthetic metadata unavailable")
		}
		assets = append(assets, asset)
	}
	foreignAsset, err := store.CreateConfigurationAsset(test.Context(), other, state.ConfigurationAssetDraft{AgentID: "codex", Kind: state.ConfigurationKindProvider, SchemaVersion: 1, Name: "Other owner's private asset", Payload: state.NewConfigurationPayload([]byte("PRIVATE_OTHER_BODY_CANARY"))})
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(store, collection, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(distribution.Close)
	ctx, cancel := context.WithTimeout(test.Context(), 12*time.Minute)
	defer cancel()
	capability := rand.Text()
	var mutex sync.Mutex
	mode, target := "normal", ""
	counts := make(map[string]int)
	public := httptest.NewUnstartedServer(nil)
	origin := "https://" + public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Store: store, PublicOrigin: origin, Library: collection, Packs: distribution, Configuration: domain, WebUI: web.Handler()})
	public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, procedure, _ := strings.CutLast(request.URL.Path, "/")
		mutex.Lock()
		if strings.HasPrefix(request.URL.Path, "/skillhub.") {
			counts[procedure]++
		}
		current := "normal"
		if procedure == target {
			current = mode
			if current != "error" {
				mode = "normal"
			}
		}
		mutex.Unlock()
		if current == "error" || current == "denied" {
			writer.Header().Set("Content-Type", "application/json")
			if current == "denied" {
				writer.WriteHeader(http.StatusForbidden)
				_, _ = writer.Write([]byte(`{"code":"permission_denied","message":"pack configuration request refused"}`))
			} else {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"code":"unavailable","message":"synthetic read failure"}`))
			}
			return
		}
		if current == "slow" {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(3 * time.Second):
			}
		}
		if current == "asset-race" {
			asset, err := store.ConfigurationAsset(request.Context(), owner, assets[0].ID)
			if err == nil {
				_, err = store.UpdateConfigurationAsset(request.Context(), owner, asset.ID, asset.Revision, state.ConfigurationAssetDraft{AgentID: asset.AgentID, Kind: asset.Kind, SchemaVersion: asset.SchemaVersion, Name: asset.Name, Payload: state.NewConfigurationPayload([]byte("PRIVATE_ATTACHMENT_BODY_CANARY"))})
			}
			if err != nil {
				test.Error("asset race setup failed")
			}
		}
		if current == "pack-race" || current == "configuration-race" {
			snapshot, err := store.PackConfigurationAttachments(request.Context(), owner, baseline.ID)
			if err == nil && current == "configuration-race" {
				_, err = store.ReplacePackConfigurationAttachments(request.Context(), owner, baseline.ID, snapshot.PackRevision, snapshot.ConfigurationRevision, nil)
			} else if err == nil {
				entry, readErr := store.Pack(request.Context(), owner, baseline.ID)
				err = readErr
				if err == nil {
					_, err = store.UpdatePack(request.Context(), owner, entry.ID, entry.Revision, state.PackDraft{Name: entry.Name, Items: []state.PackSelection{{SkillID: repo.Skills[0].ID}}})
				}
			}
			if err != nil {
				test.Error("revision race setup failed")
			}
		}
		if current == "lost" || current == "malformed" || current == "malformed-error" || current == "malformed-details" || current == "duplicate-error" || current == "committed-slow" {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(packConfigurationReceiptWriter{ResponseRecorder: recorded, target: writer}, request)
			if recorded.Code != http.StatusOK {
				test.Errorf("fault did not follow a real commit: status %d", recorded.Code)
			}
			if current == "committed-slow" {
				select {
				case <-request.Context().Done():
					return
				case <-time.After(3 * time.Second):
				}
			}
			writer.Header().Set("Content-Type", "application/json")
			if current == "malformed-details" {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(`{"code":"aborted","message":"synthetic malformed receipt","details":[{}]}`))
			} else if current == "duplicate-error" {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(`{"code":"invalid_argument","code":"aborted","message":"synthetic malformed receipt"}`))
			} else if current == "malformed-error" {
				writer.WriteHeader(http.StatusBadRequest)
				_, _ = writer.Write([]byte(`{"code":"invalid_argument","message":17}`))
			} else if current == "malformed" {
				_, _ = writer.Write([]byte(`{"snapshot":{}}`))
			} else {
				writer.WriteHeader(http.StatusServiceUnavailable)
				_, _ = writer.Write([]byte(`{"code":"unavailable","message":"synthetic receipt loss"}`))
			}
			return
		}
		if current == "missing-metadata" {
			request = request.Clone(request.Context())
			request.Header.Del("Accept-Encoding")
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(packConfigurationReceiptWriter{ResponseRecorder: recorded, target: writer}, request)
			metadata := &configurationv1.ListAssetsResponse{}
			if err := protojson.Unmarshal(recorded.Body.Bytes(), metadata); err != nil {
				test.Errorf("metadata omission setup failed: status %d, encoding %q", recorded.Code, recorded.Header().Get("Content-Encoding"))
			}
			for index, asset := range metadata.Assets {
				if asset.Id == assets[0].ID {
					metadata.Assets = append(metadata.Assets[:index], metadata.Assets[index+1:]...)
					break
				}
			}
			writer.Header().Set("Content-Type", "application/json")
			encoded, err := protojson.Marshal(metadata)
			if err != nil {
				test.Error("metadata omission encode failed")
			}
			_, _ = writer.Write(encoded)
			return
		}
		handler.ServeHTTP(writer, request)
	})
	public.Config.ReadHeaderTimeout = 5 * time.Second
	public.StartTLS()
	test.Cleanup(public.Close)
	control := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "refused", http.StatusForbidden)
			return
		}
		fail := func(err error) {
			if err != nil {
				http.Error(writer, "fixture action failed", http.StatusInternalServerError)
			}
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /mode":
			var input struct{ Mode, Target string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 256), &input) != nil || !strings.Contains("|normal|error|denied|slow|lost|malformed|malformed-error|malformed-details|duplicate-error|committed-slow|asset-race|pack-race|configuration-race|missing-metadata|", "|"+input.Mode+"|") {
				http.Error(writer, "invalid mode", http.StatusBadRequest)
				return
			}
			mutex.Lock()
			mode, target = input.Mode, input.Target
			mutex.Unlock()
		case "GET /state":
			snapshot, err := store.PackConfigurationAttachments(request.Context(), owner, baseline.ID)
			if err != nil {
				fail(err)
				return
			}
			entry, err := store.Pack(request.Context(), owner, baseline.ID)
			if err != nil {
				fail(err)
				return
			}
			mutex.Lock()
			defer mutex.Unlock()
			_ = json.MarshalWrite(writer, map[string]any{"counts": counts, "snapshot": snapshot, "skillCount": len(entry.Items)})
		case "POST /asset-conflict", "POST /unsupported":
			asset, err := store.ConfigurationAsset(request.Context(), owner, assets[0].ID)
			if err != nil {
				fail(err)
				return
			}
			version := asset.SchemaVersion
			if request.URL.Path == "/unsupported" {
				version = 65535
			}
			_, err = store.UpdateConfigurationAsset(request.Context(), owner, asset.ID, asset.Revision, state.ConfigurationAssetDraft{AgentID: asset.AgentID, Kind: asset.Kind, SchemaVersion: version, Name: asset.Name, Payload: state.NewConfigurationPayload([]byte("PRIVATE_ATTACHMENT_BODY_CANARY"))})
			if err == nil && request.URL.Path == "/unsupported" {
				database, openErr := sql.Open("sqlite3", filepath.Join(storage, "attachments.db"))
				err = openErr
				if err == nil {
					_, err = database.ExecContext(request.Context(), "UPDATE pack_configuration_attachments SET asset_revision = ? WHERE configuration_asset_id IN (SELECT id FROM configuration_assets WHERE public_id = ?)", asset.Revision+1, asset.ID)
					if closeErr := database.Close(); err == nil {
						err = closeErr
					}
				}
			}
			fail(err)
		case "POST /pack-conflict":
			entry, err := store.Pack(request.Context(), owner, baseline.ID)
			if err != nil {
				fail(err)
				return
			}
			draft.Name = entry.Name
			_, err = store.UpdatePack(request.Context(), owner, entry.ID, entry.Revision, draft)
			fail(err)
		case "POST /configuration-conflict":
			snapshot, err := store.PackConfigurationAttachments(request.Context(), owner, baseline.ID)
			if err != nil {
				fail(err)
				return
			}
			_, err = store.ReplacePackConfigurationAttachments(request.Context(), owner, baseline.ID, snapshot.PackRevision, snapshot.ConfigurationRevision, nil)
			fail(err)
		case "POST /delete":
			entry, err := store.Pack(request.Context(), owner, baseline.ID)
			if err == nil {
				err = store.DeletePack(request.Context(), owner, baseline.ID, entry.Revision)
			}
			fail(err)
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(control.Close)
	data, err := json.Marshal(map[string]any{"url": public.URL, "controlURL": control.URL, "capability": capability, "username": "attachment-owner", "otherUsername": "attachment-other", "password": password, "packId": baseline.ID, "alternateId": alternate.ID, "foreignAssetId": foreignAsset.ID, "assetIds": assets})
	if err != nil {
		test.Fatal(err)
	}
	file, err := root.OpenFile("ready.json", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := file.Write(data); err != nil {
		test.Fatal(err)
	}
	if err := file.Close(); err != nil {
		test.Fatal(err)
	}
	<-ctx.Done()
}
