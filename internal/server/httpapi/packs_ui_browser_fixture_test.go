package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/subtle"
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

	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server/auth"
	"github.com/ca-x/cognate/internal/server/library"
	"github.com/ca-x/cognate/internal/server/packs"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/web"
)

func TestPacksUIBrowserFixture(test *testing.T) {
	directory := os.Getenv("COGNATE_PACKS_UI_BROWSER_DIR")
	if directory == "" {
		test.Skip("isolated Pack UI browser fixture")
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
	store, err := state.Open(test.Context(), filepath.Join(storage, "packs.db"), key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = store.Close() })
	authentication, err := auth.NewService(store, key)
	if err != nil {
		test.Fatal(err)
	}
	password := rand.Text()
	owner, err := authentication.Register(test.Context(), "packs-ui-fixture", []byte(password))
	if err != nil {
		test.Fatal("synthetic account unavailable")
	}
	archive := libraryArchive(test, "---\nname: Contract review\ndescription: Review interface changes and keyboard accessibility.\n---\nSynthetic instructions only.\n")
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
	var selections []state.PackSelection
	for _, name := range []string{"synthetic/interface-review", "synthetic/overlapping-review"} {
		entry, err := collection.Collect(test.Context(), owner, name, "main")
		if err != nil {
			test.Fatal(err)
		}
		entry, err = collection.Get(test.Context(), owner, entry.ID)
		if err != nil || len(entry.Skills) != 1 {
			test.Fatal("synthetic collection unavailable")
		}
		selections = append(selections, state.PackSelection{SkillID: entry.Skills[0].ID})
	}
	baseline, err := store.CreatePack(test.Context(), owner, state.PackDraft{Name: "Engineering review · 工程审阅", Description: "Review contracts across repositories. Shared content follows the current Pack revision; device configuration is not applied.", Items: selections})
	if err != nil {
		test.Fatal(err)
	}
	distribution, err := packs.NewService(store, collection, key)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(distribution.Close)
	ctx, cancel := context.WithTimeout(test.Context(), 15*time.Minute)
	defer cancel()
	capability := rand.Text()
	var mutex sync.Mutex
	mode, target := "normal", ""
	counts := make(map[string]int)
	public := httptest.NewUnstartedServer(nil)
	origin := "https://" + public.Listener.Addr().String()
	handler := NewWithOptions(authentication, Options{Store: store, PublicOrigin: origin, Library: collection, Packs: distribution, WebUI: web.Handler()})
	public.Config.Handler = http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, procedure, _ := strings.CutLast(request.URL.Path, "/")
		mutex.Lock()
		if strings.HasPrefix(request.URL.Path, "/skillhub.pack.v1.PackService/") {
			counts[procedure]++
		}
		current := "normal"
		if procedure == target {
			current = mode
			if mode == "lost" {
				mode = "normal"
			}
		}
		mutex.Unlock()
		if current == "slow" {
			select {
			case <-request.Context().Done():
				return
			case <-time.After(1200 * time.Millisecond):
			}
		}
		if current == "error" {
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"code":"unavailable","message":"synthetic read failure"}`))
			return
		}
		if current == "lost" {
			recorded := httptest.NewRecorder()
			handler.ServeHTTP(recorded, request)
			writer.Header().Set("Content-Type", "application/json")
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"code":"unavailable","message":"synthetic receipt loss"}`))
			return
		}
		handler.ServeHTTP(writer, request)
	})
	public.Config.ReadHeaderTimeout = 5 * time.Second
	public.StartTLS()
	test.Cleanup(public.Close)
	control := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if subtle.ConstantTimeCompare([]byte(request.Header.Get("X-Fixture-Capability")), []byte(capability)) != 1 || request.Header.Get("Origin") != "" || request.URL.RawQuery != "" {
			http.Error(writer, "fixture control rejected", http.StatusForbidden)
			return
		}
		switch request.Method + " " + request.URL.Path {
		case "POST /mode":
			var input struct{ Mode, Target string }
			if json.UnmarshalRead(http.MaxBytesReader(writer, request.Body, 256), &input) != nil || !strings.Contains("|normal|slow|error|lost|", "|"+input.Mode+"|") {
				http.Error(writer, "invalid mode", http.StatusBadRequest)
				return
			}
			mutex.Lock()
			mode, target = input.Mode, input.Target
			mutex.Unlock()
		case "GET /state":
			entries, err := store.ListPacks(request.Context(), owner)
			if err != nil {
				http.Error(writer, "state unavailable", http.StatusInternalServerError)
				return
			}
			mutex.Lock()
			defer mutex.Unlock()
			_ = json.MarshalWrite(writer, map[string]any{"counts": counts, "packs": entries})
		case "POST /conflict":
			entry, err := store.Pack(request.Context(), owner, baseline.ID)
			if err == nil {
				_, err = store.UpdatePack(request.Context(), owner, entry.ID, entry.Revision, state.PackDraft{Name: entry.Name, Description: "Changed in another synthetic session.", Items: selections})
			}
			if err != nil {
				http.Error(writer, "conflict setup failed", http.StatusInternalServerError)
			}
		case "POST /expire":
			_, err := distribution.CreateShare(request.Context(), owner, baseline.ID, new(time.Now().Add(time.Second)))
			if err != nil {
				http.Error(writer, "expiry setup failed", http.StatusInternalServerError)
			}
		case "POST /stop":
			cancel()
		default:
			http.NotFound(writer, request)
		}
	}))
	test.Cleanup(control.Close)
	data, err := json.Marshal(map[string]any{"url": public.URL, "controlURL": control.URL, "capability": capability, "username": "packs-ui-fixture", "password": password})
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
