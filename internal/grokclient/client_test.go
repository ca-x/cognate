package grokclient

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json/v2"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/repository"
	"github.com/ca-x/cognate/internal/server"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"github.com/ca-x/cognate/internal/skill"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/descriptorpb"
)

type appFixture struct {
	origin, owner, session string
	settings               *http.Transport
	http                   *http.Client
	peer                   *state.Store
	stop                   func()
	path                   string
	master                 []byte
}

func newAppFixture(test *testing.T) *appFixture {
	test.Helper()
	ctx, cancel := context.WithTimeout(test.Context(), 90*time.Second)
	test.Cleanup(cancel)
	directory := test.TempDir()
	if os.Chmod(directory, 0700) != nil {
		test.Fatal("private fixture directory")
	}
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	certificate := seed.Certificate()
	privateKey, err := x509.MarshalPKCS8PrivateKey(seed.TLS.Certificates[0].PrivateKey)
	seed.Close()
	if err != nil {
		test.Fatal("TLS fixture")
	}
	defer clear(privateKey)
	master := bytes.Repeat([]byte{97}, 32)
	defer clear(master)
	for name, value := range map[string][]byte{"master.key": master, "tls.pem": pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), "tls.key": pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateKey})} {
		if os.WriteFile(filepath.Join(directory, name), value, 0600) != nil {
			test.Fatal("fixture file")
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		test.Fatal("loopback fixture")
	}
	test.Cleanup(func() { _ = listener.Close() })
	fixture := &appFixture{origin: "https://" + listener.Addr().String()}
	fixture.path = filepath.Join(directory, "data", "server.db")
	fixture.master = bytes.Clone(master)
	test.Cleanup(func() { clear(fixture.master) })
	application, err := server.Open(ctx, server.Config{DataDirectory: filepath.Join(directory, "data"), MasterKeyFile: filepath.Join(directory, "master.key"), TLSCertificate: filepath.Join(directory, "tls.pem"), TLSKey: filepath.Join(directory, "tls.key"), PublicOrigin: fixture.origin, SourceRefreshInterval: new(time.Duration(0))})
	if err != nil {
		test.Fatal("actual App open", err)
	}
	done := make(chan error, 1)
	var group sync.WaitGroup
	group.Go(func() { done <- application.Serve(ctx, listener) })
	var once sync.Once
	fixture.stop = func() {
		once.Do(func() {
			cancel()
			if <-done != nil {
				test.Error("App Serve join")
			}
			group.Wait()
			if application.Close() != nil {
				test.Error("App close")
			}
		})
	}
	test.Cleanup(fixture.stop)
	pool := x509.NewCertPool()
	pool.AddCert(certificate)
	fixture.settings = &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool}, Proxy: nil, ResponseHeaderTimeout: 5 * time.Second}
	fixture.http = &http.Client{Transport: fixture.settings, Timeout: 8 * time.Second}
	test.Cleanup(fixture.settings.CloseIdleConnections)
	auth := authv1connect.NewAuthServiceClient(fixture.http, fixture.origin)
	registered, err := auth.Register(ctx, connect.NewRequest(&authv1.RegisterRequest{Username: "grok-g1-owner", Password: "synthetic-owner-password-917"}))
	if err != nil || registered == nil || registered.Msg == nil {
		test.Fatal("actual registration")
	}
	login, err := auth.Login(ctx, connect.NewRequest(&authv1.LoginRequest{Username: "grok-g1-owner", Password: "synthetic-owner-password-917"}))
	if err != nil || login == nil || login.Msg == nil {
		test.Fatal("actual login")
	}
	fixture.owner, fixture.session = registered.Msg.UserId, login.Msg.SessionToken
	fixture.peer, err = state.Open(ctx, filepath.Join(directory, "data", "server.db"), master)
	if err != nil {
		test.Fatal("second SQLite Store", err)
	}
	test.Cleanup(func() {
		if fixture.peer.Close() != nil {
			test.Error("peer close")
		}
	})
	return fixture
}

func fixtureRequest[Message any](fixture *appFixture, message *Message) *connect.Request[Message] {
	request := connect.NewRequest(message)
	request.Header().Set("Authorization", "Bearer "+fixture.session)
	return request
}

func wireDraft() *configurationv1.Draft {
	return &configurationv1.Draft{Name: "Owner Grok", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_CUSTOM_MODEL, Model: &configurationv1.GrokBuildModel{Profile: "local-profile", UpstreamModel: "x-ai/upstream", Endpoint: "https://synthetic.invalid/v1", Name: "Provider", ApiBackend: "responses", ContextWindow: 500000, EnvKey: new("GROK_REF")}}}}
}

func TestExistingAPIRealBaseline(test *testing.T) {
	fixture := newAppFixture(test)
	rpc := configurationv1connect.NewConfigurationServiceClient(fixture.http, fixture.origin)
	created, err := rpc.CreateAsset(test.Context(), fixtureRequest(fixture, &configurationv1.CreateAssetRequest{Draft: wireDraft(), Secrets: []*configurationv1.SecretMutation{{SlotId: "grokbuild.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "synthetic-key"}}}}))
	if err != nil || created == nil || created.Msg == nil || created.Msg.View == nil {
		test.Fatal("existing API create positive")
	}
	id := created.Msg.View.Asset.Id
	view, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, id)
	if err != nil || view.Metadata.ID != id || view.Metadata.Revision != 1 || !view.Secrets[0].Present || view.GrokBuildProvider.Model.UpstreamModel != "x-ai/upstream" {
		test.Fatal("actual encrypted snapshot positive")
	}
	copyResponse, err := rpc.CopyAsset(test.Context(), fixtureRequest(fixture, &configurationv1.CopyAssetRequest{SourceId: id, ExpectedSourceRevision: 1, Name: "Copy"}))
	if err != nil || copyResponse.Msg.View.Secrets[0].Present {
		test.Fatal("existing API Copy positive")
	}
	copyView, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, copyResponse.Msg.View.Asset.Id)
	if err != nil || copyView.Secrets[0].Present || copyView.GrokBuildProvider.Model.Profile != "local-profile" {
		test.Fatal("actual copy identity positive")
	}
}

const customJSON = `{"name":"Owner Grok","grokbuildProvider":{"mode":"GROK_BUILD_MODE_CUSTOM_MODEL","model":{"profile":"local-profile","upstreamModel":"x-ai/upstream","endpoint":"https://synthetic.invalid/v1","name":"Provider","apiBackend":"responses","contextWindow":"500000","envKey":"GROK_REF"}}}`
const nativeJSON = `{"name":"Official","grokbuildProvider":{"mode":"GROK_BUILD_MODE_NATIVE_LOGIN"}}`

func privateFile(test *testing.T, data string) string {
	test.Helper()
	path := filepath.Join(test.TempDir(), "input")
	if os.WriteFile(path, []byte(data), 0600) != nil {
		test.Fatal("private input fixture")
	}
	return path
}

func TestInputBasic(test *testing.T) {
	for _, raw := range []string{customJSON, nativeJSON} {
		input, err := LoadInput(test.Context(), privateFile(test, raw), NoKeyMutation, "")
		if err != nil {
			test.Fatal("valid typed input", err)
		}
		input.Close()
	}
	input, err := LoadInput(test.Context(), privateFile(test, customJSON), ReplaceKey, privateFile(test, " synthetic-key\t"))
	if err != nil {
		test.Fatal("explicit owned replacement", err)
	}
	input.Close()
	if _, err := LoadInput(test.Context(), privateFile(test, nativeJSON), ReplaceKey, privateFile(test, "key")); !errors.Is(err, ErrInvalid) {
		test.Fatal("native replacement accepted")
	}
	ids, err := ReadOrderFile(test.Context(), privateFile(test, `[]`))
	if err != nil || ids == nil || len(ids) != 0 {
		test.Fatal("empty order input")
	}
	if _, err := ReadOrderFile(test.Context(), privateFile(test, `["`+strings.Repeat("A", 26)+`","`+strings.Repeat("A", 26)+`"]`)); !errors.Is(err, ErrInvalid) {
		test.Fatal("duplicate order IDs")
	}
}

func TestInputStrictPresence(test *testing.T) {
	cases := map[string]string{
		"env-null":            strings.Replace(customJSON, `"envKey":"GROK_REF"`, `"envKey":null`, 1),
		"native-model-null":   strings.Replace(nativeJSON, `"GROK_BUILD_MODE_NATIVE_LOGIN"`, `"GROK_BUILD_MODE_NATIVE_LOGIN","model":null`, 1),
		"noncanonical-number": strings.Replace(customJSON, `"contextWindow":"500000"`, `"contextWindow":500000`, 1),
		"numeric-enum":        strings.Replace(customJSON, `"GROK_BUILD_MODE_CUSTOM_MODEL"`, `2`, 1),
		"alias":               strings.Replace(customJSON, `"contextWindow"`, `"context_window"`, 1),
		"duplicate":           strings.Replace(customJSON, `"profile":"local-profile"`, `"profile":"local-profile","profile":"other"`, 1),
		"foreign-keyPresent":  strings.Replace(customJSON, `"profile":"local-profile"`, `"profile":"local-profile","keyPresent":true`, 1),
		"unknown":             strings.Replace(nativeJSON, `"name":"Official"`, `"extra":"canary","name":"Official"`, 1),
		"trailing":            customJSON + `{}`,
	}
	for name, raw := range cases {
		test.Run(name, func(test *testing.T) {
			input, err := LoadInput(test.Context(), privateFile(test, raw), NoKeyMutation, "")
			if input != nil {
				input.Close()
			}
			if !errors.Is(err, ErrInvalid) {
				test.Fatal("noncanonical/ambiguous typed input accepted")
			}
		})
	}
}

func fixtureClient(test *testing.T, fixture *appFixture) *Client {
	test.Helper()
	client, err := New(fixture.origin, fixture.session, fixture.settings)
	if err != nil {
		test.Fatal("owner client", err)
	}
	test.Cleanup(client.Close)
	return client
}
func fixtureInput(test *testing.T, raw string, action KeyAction) *Input {
	test.Helper()
	path := ""
	if action == ReplaceKey {
		path = privateFile(test, " synthetic-G1-key\t")
	}
	input, err := LoadInput(test.Context(), privateFile(test, raw), action, path)
	if err != nil {
		test.Fatal("typed fixture input", err)
	}
	test.Cleanup(input.Close)
	return input
}
func submitChange(test *testing.T, client *Client, change Change, input *Input) Result {
	test.Helper()
	operation, err := client.Prepare(test.Context(), change, input)
	if err != nil {
		test.Fatal("prepare", err)
	}
	defer operation.Close()
	var stdout, tty bytes.Buffer
	if operation.WriteSummary(test.Context(), &stdout) != nil || operation.WriteSummary(test.Context(), &tty) != nil || stdout.String() != tty.String() || !strings.Contains(stdout.String(), "NOT been server-validated") || strings.Contains(stdout.String(), "synthetic-G1-key") {
		test.Fatal("safe complete local review")
	}
	if operation.Confirmation() != "grok "+actionLabel(change.Action)+" "+operation.ReviewID() {
		test.Fatal("confirmation binding")
	}
	result, err := operation.Submit(test.Context())
	if err != nil {
		test.Fatal("actual mutation", err)
	}
	if _, err := operation.Submit(test.Context()); !errors.Is(err, ErrClosed) {
		test.Fatal("same operation replayed")
	}
	return result
}
func assertStored(test *testing.T, fixture *appFixture, id string, revision int64, key string, expected *configurationv1.Draft) {
	test.Helper()
	metadata, payload, err := fixture.peer.ReadConfigurationAssetVersion(test.Context(), fixture.owner, id)
	if err != nil || metadata.ID != id || metadata.Revision != revision || metadata.Name != strings.TrimSpace(expected.Name) {
		test.Fatal("receipt does not resolve to approved durable identity")
	}
	data := payload.Reveal()
	defer clear(data)
	var envelope struct {
		Secrets struct {
			APIKey string `json:"api_key"`
		} `json:"secrets"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Secrets.APIKey != key {
		test.Fatal("durable key truth mismatch")
	}
	view, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, id)
	if err != nil {
		test.Fatal("durable domain read")
	}
	provider := expected.GetGrokbuildProvider()
	actual := view.GrokBuildProvider
	if provider.Mode == configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN {
		if actual.Mode != "native-login" || actual.Model != nil {
			test.Fatal("native snapshot mismatch")
		}
	} else {
		model := provider.Model
		stored := actual.Model
		if stored == nil || stored.Profile != model.Profile || stored.UpstreamModel != model.UpstreamModel || stored.Endpoint != model.Endpoint || stored.Name != model.Name || stored.APIBackend != model.ApiBackend || stored.ContextWindow != model.ContextWindow || !reflect.DeepEqual(stored.EnvKey, model.EnvKey) {
			test.Fatal("durable nonsecret snapshot mismatch")
		}
	}
}

func TestClientRealAppNineRPCs(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	empty, err := client.GetOrder(test.Context())
	if err != nil || empty.Revision != 0 || len(empty.Assets) != 0 {
		test.Fatal("empty partition revision0")
	}
	noOp := submitChange(test, client, Change{Action: ReplaceOrder, ExpectedOrderRevision: 0, OrderIDs: []string{}}, nil)
	if noOp.Order == nil || noOp.Order.Revision != 0 {
		test.Fatal("empty exact order")
	}
	input := fixtureInput(test, customJSON, ReplaceKey)
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, input)
	if err != nil {
		test.Fatal(err)
	}
	input.Close()
	if assets, err := fixture.peer.ListConfigurationAssets(test.Context(), fixture.owner); err != nil || len(assets) != 0 {
		test.Fatal("default Prepare mutated server")
	}
	result, err := operation.Submit(test.Context())
	operation.Close()
	if err != nil || result.Metadata == nil {
		test.Fatal("owned input snapshot lost on caller Close", err)
	}
	id := result.Metadata.ID
	assertStored(test, fixture, id, 1, " synthetic-G1-key\t", wireDraft())
	metadata, err := client.Get(test.Context(), id)
	if err != nil || metadata.ID != id || metadata.Revision != 1 {
		test.Fatal("Get mapping")
	}
	view, err := client.Read(test.Context(), id)
	if err != nil {
		test.Fatal("Read mapping")
	}
	var summary bytes.Buffer
	if view.WriteSummary(test.Context(), &summary) != nil || !strings.Contains(summary.String(), "key-present=true") || !strings.Contains(summary.String(), "upstreamModel=\"x-ai/upstream\"") {
		test.Fatal("typed read fields")
	}
	view.Close()
	updated := submitChange(test, client, Change{Action: Update, ID: id, ExpectedRevision: 1}, fixtureInput(test, customJSON, KeepKey))
	if updated.Metadata.Revision != 2 {
		test.Fatal("Update revision")
	}
	assertStored(test, fixture, id, 2, " synthetic-G1-key\t", wireDraft())
	if _, err := client.Prepare(test.Context(), Change{Action: Update, ID: id, ExpectedRevision: 1}, fixtureInput(test, customJSON, NoKeyMutation)); !errors.Is(err, ErrConflict) {
		test.Fatal("stale Prepare accepted")
	}
	copied := submitChange(test, client, Change{Action: Copy, ID: id, ExpectedRevision: 2, Name: "Copy"}, nil)
	copyDraft := wireDraft()
	copyDraft.Name = "Copy"
	assertStored(test, fixture, copied.Metadata.ID, 1, "", copyDraft)
	listed, err := client.List(test.Context())
	if err != nil || len(listed) != 2 {
		test.Fatal("List mapping")
	}
	order, err := client.GetOrder(test.Context())
	if err != nil {
		test.Fatal(err)
	}
	ordered := submitChange(test, client, Change{Action: ReplaceOrder, ExpectedOrderRevision: order.Revision, OrderIDs: []string{copied.Metadata.ID, id}}, nil)
	actual, err := fixture.peer.ConfigurationAssetOrder(test.Context(), fixture.owner, "grokbuild", state.ConfigurationKindProvider)
	if err != nil || ordered.Order == nil || actual.Revision != ordered.Order.Revision || actual.Assets[0].ID != copied.Metadata.ID || actual.Assets[1].ID != id {
		test.Fatal("actual ordered identity mismatch")
	}
	if _, err := client.Prepare(test.Context(), Change{Action: Update, ID: id, ExpectedRevision: 2}, fixtureInput(test, nativeJSON, NoKeyMutation)); !errors.Is(err, ErrInvalid) {
		test.Fatal("native switch silently cleared key")
	}
	native := submitChange(test, client, Change{Action: Update, ID: id, ExpectedRevision: 2}, fixtureInput(test, nativeJSON, ClearKey))
	nativeDraft := &configurationv1.Draft{Name: "Official", Payload: &configurationv1.Draft_GrokbuildProvider{GrokbuildProvider: &configurationv1.GrokBuildProvider{Mode: configurationv1.GrokBuildMode_GROK_BUILD_MODE_NATIVE_LOGIN}}}
	assertStored(test, fixture, id, 3, "", nativeDraft)
	for _, target := range []Metadata{*native.Metadata, *copied.Metadata} {
		result := submitChange(test, client, Change{Action: Delete, ID: target.ID, ExpectedRevision: target.Revision}, nil)
		if !result.Deleted {
			test.Fatal("Delete mapping")
		}
	}
	if assets, err := client.List(test.Context()); err != nil || len(assets) != 0 {
		test.Fatal("durable delete")
	}
}

func TestOpaqueCarrierPrivacy(test *testing.T) {
	input := fixtureInput(test, customJSON, ReplaceKey)
	variants := []string{"synthetic-G1-key", hex.EncodeToString([]byte(" synthetic-G1-key\t")), strings.ToUpper(hex.EncodeToString([]byte(" synthetic-G1-key\t"))), fmt.Sprint([]byte(" synthetic-G1-key\t"))}
	for _, value := range []any{input, struct {
		Label  string
		secret any
	}{"safe", input}, []*Input{input}, map[string]any{"value": input}} {
		for _, format := range []string{"%v", "%+v", "%#v", "%s", "%q", "%d", "%x", "%X"} {
			for _, canary := range variants {
				if strings.Contains(fmt.Sprintf(format, value), canary) {
					test.Fatal("format credential leak")
				}
			}
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			test.Fatal("carrier JSON setup", err)
		}
		var logs bytes.Buffer
		slog.New(slog.NewJSONHandler(&logs, nil)).Info("safe", "value", value)
		if bytes.Contains(encoded, []byte("synthetic-G1-key")) || strings.Contains(logs.String(), "synthetic-G1-key") {
			test.Fatal("JSON/slog credential leak")
		}
	}
}

func TestInputBoundsAndPhysicalOwnership(test *testing.T) {
	if runtime.GOOS != "linux" {
		test.Skip("actual Linux private file boundary")
	}
	for _, key := range []string{"", "bad\rkey", "bad\nkey", "bad\x00key", "bad\xff", strings.Repeat("k", 8193)} {
		if input, err := LoadInput(test.Context(), privateFile(test, customJSON), ReplaceKey, privateFile(test, key)); input != nil || !errors.Is(err, ErrInvalid) {
			if input != nil {
				input.Close()
			}
			test.Fatal("invalid raw key accepted")
		}
	}
	for _, key := range []string{strings.Repeat("k", 8192), " \t "} {
		input, err := LoadInput(test.Context(), privateFile(test, customJSON), ReplaceKey, privateFile(test, key))
		if err != nil {
			test.Fatal("legal exact key boundary", err)
		}
		input.Close()
	}
	for _, mode := range []string{"leaf-symlink", "parent-symlink", "hardlink", "fifo", "directory", "key-world-readable", "draft-world-writable", "overflow"} {
		test.Run(mode, func(test *testing.T) {
			dir := test.TempDir()
			path := filepath.Join(dir, "input")
			source := privateFile(test, customJSON)
			switch mode {
			case "leaf-symlink":
				if os.Symlink(source, path) != nil {
					test.Fatal("symlink fixture")
				}
			case "parent-symlink":
				if os.Symlink(filepath.Dir(source), path) != nil {
					test.Fatal("parent fixture")
				}
				path = filepath.Join(path, "input")
			case "hardlink":
				if os.Link(source, path) != nil {
					test.Fatal("hardlink fixture")
				}
			case "fifo":
				ctx, cancel := context.WithTimeout(test.Context(), 2*time.Second)
				defer cancel()
				if exec.CommandContext(ctx, "mkfifo", path).Run() != nil {
					test.Fatal("FIFO fixture")
				}
			case "directory":
				if os.Mkdir(path, 0700) != nil {
					test.Fatal("directory fixture")
				}
			case "key-world-readable":
				path = privateFile(test, "key")
				if os.Chmod(path, 0644) != nil {
					test.Fatal("mode fixture")
				}
			case "draft-world-writable":
				path = source
				if os.Chmod(path, 0666) != nil {
					test.Fatal("mode fixture")
				}
			case "overflow":
				path = privateFile(test, customJSON+strings.Repeat(" ", 64<<10))
			}
			var input *Input
			var err error
			if mode == "key-world-readable" {
				input, err = LoadInput(test.Context(), source, ReplaceKey, path)
			} else {
				input, err = LoadInput(test.Context(), path, NoKeyMutation, "")
			}
			if input != nil {
				input.Close()
			}
			if !errors.Is(err, ErrInvalid) {
				test.Fatal("unsafe physical input accepted")
			}
		})
	}
	for _, value := range []string{"1", "9007199254740991"} {
		raw := strings.Replace(customJSON, `"500000"`, `"`+value+`"`, 1)
		input, err := LoadInput(test.Context(), privateFile(test, raw), NoKeyMutation, "")
		if err != nil {
			test.Fatal("safe integer boundary")
		}
		input.Close()
	}
	for _, value := range []string{"0", "9007199254740992", "-1", "01", "1e2"} {
		raw := strings.Replace(customJSON, `"500000"`, `"`+value+`"`, 1)
		if input, err := LoadInput(test.Context(), privateFile(test, raw), NoKeyMutation, ""); input != nil || !errors.Is(err, ErrInvalid) {
			test.Fatal("unsafe/noncanonical integer")
		}
	}
}

func TestStrictResponseWireAtomic(test *testing.T) {
	want := &configurationv1.GetAssetResponse{Asset: &configurationv1.Asset{Id: strings.Repeat("A", 26), Revision: 7}}
	encoded, err := proto.Marshal(want)
	if err != nil {
		test.Fatal(err)
	}
	destination := &configurationv1.GetAssetResponse{}
	if (wireCodec{}).Unmarshal(encoded, destination) != nil || !proto.Equal(destination, want) {
		test.Fatal("valid wire refused")
	}
	cases := [][]byte{append(bytes.Clone(encoded), 0x98, 0x06, 0x01), append(bytes.Clone(encoded), encoded...), {0x0a, 0x80, 0x00}, {0x0a, 0x02, 0x08, 0x00}, {0x0a, 0x03, 0x0a, 0x01, 0xff}, bytes.Repeat([]byte{0}, (1<<20)+1)}
	for _, data := range cases {
		destination = proto.Clone(want).(*configurationv1.GetAssetResponse)
		if (wireCodec{}).Unmarshal(data, destination) == nil || !proto.Equal(destination, want) {
			test.Fatal("invalid wire accepted or changed destination")
		}
	}
	descriptor, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{Name: new("synthetic.proto"), Package: new("fixture"), Syntax: new("proto3"), MessageType: []*descriptorpb.DescriptorProto{{Name: new("Scalars"), Field: []*descriptorpb.FieldDescriptorProto{
		{Name: new("sint32"), Number: new(int32(1)), Type: descriptorpb.FieldDescriptorProto_TYPE_SINT32.Enum()},
		{Name: new("fixed32"), Number: new(int32(2)), Type: descriptorpb.FieldDescriptorProto_TYPE_FIXED32.Enum()},
		{Name: new("double"), Number: new(int32(3)), Type: descriptorpb.FieldDescriptorProto_TYPE_DOUBLE.Enum()},
		{Name: new("boolean"), Number: new(int32(4)), Type: descriptorpb.FieldDescriptorProto_TYPE_BOOL.Enum()},
		{Name: new("int32"), Number: new(int32(5)), Type: descriptorpb.FieldDescriptorProto_TYPE_INT32.Enum()},
	}}}}, nil)
	if err != nil {
		test.Fatal("scalar descriptor fixture", err)
	}
	message := descriptor.Messages().Get(0)
	for _, entry := range []struct {
		number int
		wire   protowire.Type
		data   []byte
		valid  bool
	}{
		{1, protowire.VarintType, protowire.AppendVarint(nil, 0xffffffff), true},
		{1, protowire.VarintType, protowire.AppendVarint(nil, 0x100000000), false},
		{2, protowire.Fixed32Type, protowire.AppendFixed32(nil, 7), true},
		{2, protowire.VarintType, []byte{7}, false},
		{3, protowire.Fixed64Type, protowire.AppendFixed64(nil, 0x3ff0000000000000), true},
		{4, protowire.VarintType, []byte{2}, false},
		{5, protowire.VarintType, protowire.AppendVarint(nil, 0xffffffffffffffff), true},
		{5, protowire.VarintType, protowire.AppendVarint(nil, 0xffffffff), false},
	} {
		budget := 100
		data := append(protowire.AppendTag(nil, protowire.Number(entry.number), entry.wire), entry.data...)
		if validWire(data, message, 0, &budget) != entry.valid {
			test.Fatal("scalar kind/wire/range boundary mismatch", entry.number)
		}
	}
}

type transportFunc func(*http.Request) (*http.Response, error)

func (function transportFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type countedBody struct {
	*bytes.Reader
	closed bool
}

func (body *countedBody) Close() error { body.closed = true; return nil }
func TestTransportOwnsRequestBody(test *testing.T) {
	body := &countedBody{Reader: bytes.NewReader([]byte("private-request"))}
	transport := fixedTransport{scheme: "https", host: "synthetic.invalid", base: transportFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(""))}, nil
	})}
	request, err := http.NewRequestWithContext(test.Context(), http.MethodPost, "https://synthetic.invalid"+servicePrefix+"CreateAsset", body)
	if err != nil {
		test.Fatal(err)
	}
	response, err := transport.RoundTrip(request)
	if err != nil {
		test.Fatal(err)
	}
	defer response.Body.Close()
	if !body.closed {
		test.Fatal("early response returned before owned request body was closed")
	}
}

func TestFutureExtrasMetadataQuotaAndInUse(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	raw := `{"version":1,"variant":"grokbuild-provider","provider":{"mode":"custom-model","model":{"profile":"future-profile","upstreamModel":"upstream","endpoint":"https://synthetic.invalid/","name":"Provider","apiBackend":"responses","contextWindow":1,"envKey":"REF"}},"secrets":{"future":{"key":"synthetic-future-canary"}}}`
	future, err := fixture.peer.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, Name: "Unknown extras", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(raw))})
	if err != nil {
		test.Fatal(err)
	}
	view, err := client.Read(test.Context(), future.ID)
	if err != nil {
		test.Fatal("known readonly projection", err)
	}
	var summary bytes.Buffer
	if view.WriteSummary(test.Context(), &summary) != nil || !strings.Contains(summary.String(), "extras=true") || strings.Contains(summary.String(), "synthetic-future-canary") {
		test.Fatal("unknown extras leaked or became editable")
	}
	view.Close()
	for _, change := range []Change{{Action: Update, ID: future.ID, ExpectedRevision: 1}, {Action: Copy, ID: future.ID, ExpectedRevision: 1, Name: "copy"}} {
		var input *Input
		if change.Action == Update {
			input = fixtureInput(test, customJSON, ClearKey)
		}
		if _, err := client.Prepare(test.Context(), change, input); !errors.Is(err, ErrUnsupported) {
			test.Fatal("unknown extras became writable")
		}
	}
	metadata, payload, err := fixture.peer.ReadConfigurationAssetVersion(test.Context(), fixture.owner, future.ID)
	if err != nil || metadata != future || string(payload.Reveal()) != raw {
		test.Fatal("unknown bytes rewritten")
	}
	futureSchema, err := fixture.peer.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: "grokbuild", Kind: state.ConfigurationKindProvider, Name: "Future schema", SchemaVersion: 2, Payload: state.NewConfigurationPayload([]byte(raw))})
	if err != nil {
		test.Fatal(err)
	}
	if metadata, err := client.Get(test.Context(), futureSchema.ID); err != nil || metadata.Schema != 2 {
		test.Fatal("future metadata hidden")
	}
	if _, err := client.Read(test.Context(), futureSchema.ID); !errors.Is(err, ErrUnsupported) {
		test.Fatal("future schema promoted")
	}
	order, err := client.GetOrder(test.Context())
	if err != nil || len(order.Assets) != 2 {
		test.Fatal("readonly metadata omitted from order")
	}
	submitChange(test, client, Change{Action: ReplaceOrder, ExpectedOrderRevision: order.Revision, OrderIDs: []string{futureSchema.ID, future.ID}}, nil)
	submitChange(test, client, Change{Action: Delete, ID: futureSchema.ID, ExpectedRevision: 1}, nil)
	submitChange(test, client, Change{Action: Delete, ID: future.ID, ExpectedRevision: 1}, nil)
	asset := submitChange(test, client, Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
	reference, err := repository.ParseReference("synthetic/grok-g1", "")
	if err != nil {
		test.Fatal(err)
	}
	collected, err := fixture.peer.CollectRepository(test.Context(), fixture.owner, reference, strings.Repeat("a", 64), []repository.ArchivedSkill{{Skill: skill.Skill{Name: "Fixture", Description: "Synthetic", Path: "skills/fixture"}, ContentHash: strings.Repeat("b", 64)}})
	if err != nil {
		test.Fatal(err)
	}
	detail, err := fixture.peer.CollectedRepository(test.Context(), fixture.owner, collected.ID)
	if err != nil {
		test.Fatal(err)
	}
	pack, err := fixture.peer.CreatePack(test.Context(), fixture.owner, state.PackDraft{Name: "Fixture", Items: []state.PackSelection{{SkillID: detail.Skills[0].ID}}})
	if err != nil {
		test.Fatal(err)
	}
	attachments, err := fixture.peer.PackConfigurationAttachments(test.Context(), fixture.owner, pack.ID)
	if err != nil {
		test.Fatal(err)
	}
	if _, err := fixture.peer.ReplacePackConfigurationAttachments(test.Context(), fixture.owner, pack.ID, attachments.PackRevision, attachments.ConfigurationRevision, []state.PackConfigurationReference{{AssetID: asset.Metadata.ID, AssetRevision: 1, TargetID: "grokbuild.provider.user"}}); err != nil {
		test.Fatal(err)
	}
	deletion, err := client.Prepare(test.Context(), Change{Action: Delete, ID: asset.Metadata.ID, ExpectedRevision: 1}, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer deletion.Close()
	if _, err := deletion.Submit(test.Context()); !errors.Is(err, ErrInUse) {
		test.Fatal("typed Pack InUse lost", err)
	}
	for index := 1; index < 250; index++ {
		agent := "claude"
		if index%2 == 0 {
			agent = "gemini"
		}
		if _, err := fixture.peer.CreateConfigurationAsset(test.Context(), fixture.owner, state.ConfigurationAssetDraft{AgentID: agent, Kind: state.ConfigurationKindProvider, Name: "Other metadata", SchemaVersion: 1, Payload: state.NewConfigurationPayload([]byte(`{}`))}); err != nil {
			test.Fatal("total quota fixture", err)
		}
	}
	if items, err := client.List(test.Context()); err != nil || len(items) != 1 {
		test.Fatal("owner-wide metadata quota/filter")
	}
	limited, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, nativeJSON, NoKeyMutation))
	if err != nil {
		test.Fatal(err)
	}
	defer limited.Close()
	if _, err := limited.Submit(test.Context()); !errors.Is(err, ErrLimit) {
		test.Fatal("quota became partition-local")
	}
}

func TestLiteralBoundsAndReadonlyNoHome(test *testing.T) {
	for _, change := range []func(*configurationv1.Draft){
		func(value *configurationv1.Draft) { value.GetGrokbuildProvider().Model.Profile = " padded" },
		func(value *configurationv1.Draft) { value.GetGrokbuildProvider().Model.UpstreamModel = "bad\x00" },
		func(value *configurationv1.Draft) {
			value.GetGrokbuildProvider().Model.Endpoint = "https://synthetic.invalid/%0A"
		},
		func(value *configurationv1.Draft) {
			value.GetGrokbuildProvider().Model.Endpoint = "https://synthetic.invalid/%5C"
		},
		func(value *configurationv1.Draft) {
			value.GetGrokbuildProvider().Model.Endpoint = "https://synthetic.invalid/%FF"
		},
		func(value *configurationv1.Draft) { value.GetGrokbuildProvider().Model.EnvKey = new("") },
		func(value *configurationv1.Draft) { value.GetGrokbuildProvider().Model.EnvKey = new("1BAD") },
		func(value *configurationv1.Draft) { value.GetGrokbuildProvider().Model.ApiBackend = "future" },
	} {
		draft := wireDraft()
		change(draft)
		if validateDraft(draft) == nil {
			test.Fatal("invalid nonsecret field accepted")
		}
	}
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	home := filepath.Join(test.TempDir(), "absent")
	test.Setenv("HOME", home)
	if _, err := client.List(test.Context()); err != nil {
		test.Fatal(err)
	}
	if _, err := os.Lstat(home); !errors.Is(err, os.ErrNotExist) {
		test.Fatal("owner remote read initialized HOME/key/native state")
	}
}

func TestReviewExplicitActionsAndReadiness(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	keyless := strings.Replace(customJSON, `,"envKey":"GROK_REF"`, "", 1)
	operation, err := client.Prepare(test.Context(), Change{Action: Create}, fixtureInput(test, keyless, ClearKey))
	if err != nil {
		test.Fatal(err)
	}
	defer operation.Close()
	var summary bytes.Buffer
	if operation.WriteSummary(test.Context(), &summary) != nil || !strings.Contains(summary.String(), "key-action=clear") || !strings.Contains(summary.String(), "readiness=needs-credential") {
		test.Fatal("review omitted named Clear or predicted readiness")
	}
	result, err := operation.Submit(test.Context())
	if err != nil {
		test.Fatal("incomplete draft must remain saveable", err)
	}
	if view, err := configuration.New(fixture.peer).Edit(test.Context(), fixture.owner, result.Metadata.ID); err != nil || view.Readiness != configuration.NeedsCredential || view.Secrets[0].Present {
		test.Fatal("keyless draft falsely complete")
	}
	deletion, err := client.Prepare(test.Context(), Change{Action: Delete, ID: result.Metadata.ID, ExpectedRevision: 1}, nil)
	if err != nil {
		test.Fatal(err)
	}
	defer deletion.Close()
	summary.Reset()
	if deletion.WriteSummary(test.Context(), &summary) != nil || !strings.Contains(summary.String(), `source-name="Owner Grok"`) {
		test.Fatal("delete omitted source metadata")
	}
}

func TestTransportTLSOriginAndResponsePrivacy(test *testing.T) {
	for _, mode := range []string{"unknown-wire", "duplicate-wire", "unknown-enum", "stream-overflow", "advertised-overflow", "gzip", "redirect", "private-error"} {
		test.Run(mode, func(test *testing.T) {
			var escaped atomic.Int32
			outside := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { escaped.Add(1); writer.WriteHeader(500) }))
			defer outside.Close()
			endpoint := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.Header().Set("Content-Type", "application/proto")
				if request.URL.Path == authProcedure {
					data, _ := proto.Marshal(&authv1.GetSessionResponse{UserId: "fixture-owner"})
					_, _ = writer.Write(data)
					return
				}
				asset := &configurationv1.GetAssetResponse{Asset: &configurationv1.Asset{Id: strings.Repeat("A", 26), AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: "Fixture", SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}}
				data, _ := proto.Marshal(asset)
				switch mode {
				case "unknown-wire":
					data = append(data, 0x98, 0x06, 0x01)
				case "duplicate-wire":
					data = append(data, data...)
				case "unknown-enum":
					asset.Asset.Kind = configurationv1.AssetKind(999)
					data, _ = proto.Marshal(asset)
				case "stream-overflow":
					writer.WriteHeader(200)
					writer.(http.Flusher).Flush()
					_, _ = io.WriteString(writer, strings.Repeat("x", (1<<20)+1))
					return
				case "advertised-overflow":
					writer.Header().Set("Content-Length", "1048577")
					writer.WriteHeader(200)
					return
				case "gzip":
					writer.Header().Set("Content-Encoding", "gzip")
				case "redirect":
					writer.Header().Set("Location", outside.URL)
					writer.WriteHeader(307)
					return
				case "private-error":
					writer.Header().Set("Content-Type", "application/json")
					writer.WriteHeader(500)
					_, _ = io.WriteString(writer, `{"code":"internal","message":"CANARY_PRIVATE_SERVER_BODY"}`)
					return
				}
				_, _ = writer.Write(data)
			}))
			defer endpoint.Close()
			pool := x509.NewCertPool()
			pool.AddCert(endpoint.Certificate())
			client, err := New(endpoint.URL, strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool}})
			if err != nil {
				test.Fatal(err)
			}
			defer client.Close()
			if _, err := client.Get(test.Context(), strings.Repeat("A", 26)); err == nil || strings.Contains(err.Error(), "CANARY_PRIVATE_SERVER_BODY") {
				test.Fatal("untrusted response accepted or printed")
			}
			if escaped.Load() != 0 {
				test.Fatal("redirect escaped pinned origin")
			}
		})
	}
	for _, origin := range []string{"http://example.invalid", "http://localhost", "https://user:pass@example.invalid", "https://example.invalid?token=private", "https://example.invalid/path", "https://example.invalid:", "https://example.invalid:65536", "https://example.invalid/#"} {
		if _, err := New(origin, strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), nil); !errors.Is(err, ErrInvalid) {
			test.Fatal("unsafe origin accepted")
		}
	}
	if client, err := New("http://127.0.0.1:8080", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), nil); err != nil {
		test.Fatal("explicit loopback origin")
	} else {
		client.Close()
	}
	for _, config := range []*tls.Config{{InsecureSkipVerify: true}, {VerifyConnection: func(tls.ConnectionState) error { return nil }}, {GetClientCertificate: func(*tls.CertificateRequestInfo) (*tls.Certificate, error) { return nil, nil }}} {
		if _, err := New("https://example.invalid", strings.Repeat("a", 43)+"."+strings.Repeat("b", 43), &http.Transport{TLSClientConfig: config}); !errors.Is(err, ErrInvalid) {
			test.Fatal("unsafe TLS settings accepted")
		}
	}
}

func TestTransportIgnoresEnvironmentProxy(test *testing.T) {
	var proxies atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { proxies.Add(1); writer.WriteHeader(500) }))
	defer proxy.Close()
	test.Setenv("HTTPS_PROXY", proxy.URL)
	test.Setenv("HTTP_PROXY", proxy.URL)
	test.Setenv("ALL_PROXY", proxy.URL)
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	if _, err := client.List(test.Context()); err != nil || proxies.Load() != 0 {
		test.Fatal("inherited environment proxy")
	}
}

func TestClientExactIntegersAndPresence(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	for _, value := range []string{"1", "9007199254740991"} {
		raw := strings.Replace(customJSON, `"500000"`, `"`+value+`"`, 1)
		raw = strings.Replace(raw, `"profile":"local-profile"`, `"profile":"本地 profile"`, 1)
		action := NoKeyMutation
		if value == "1" {
			raw = strings.Replace(raw, `,"envKey":"GROK_REF"`, "", 1)
		} else {
			action = ReplaceKey
		}
		result := submitChange(test, client, Change{Action: Create}, fixtureInput(test, raw, action))
		expected := wireDraft()
		expected.GetGrokbuildProvider().Model.Profile = "本地 profile"
		key := ""
		if value == "1" {
			expected.GetGrokbuildProvider().Model.ContextWindow = 1
			expected.GetGrokbuildProvider().Model.EnvKey = nil
		} else {
			expected.GetGrokbuildProvider().Model.ContextWindow = maxContext
			key = " synthetic-G1-key\t"
		}
		assertStored(test, fixture, result.Metadata.ID, 1, key, expected)
	}
	padding := customJSON + strings.Repeat(" ", (64<<10)-len(customJSON))
	input, err := LoadInput(test.Context(), privateFile(test, padding), NoKeyMutation, "")
	if err != nil {
		test.Fatal("exact64KiB input boundary")
	}
	input.Close()
}

func TestEncryptedReopen(test *testing.T) {
	fixture := newAppFixture(test)
	client := fixtureClient(test, fixture)
	created := submitChange(test, client, Change{Action: Create}, fixtureInput(test, customJSON, ReplaceKey))
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(fixture.path + suffix)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil || bytes.Contains(data, []byte("synthetic-G1-key")) {
			test.Fatal("plaintext key persisted in SQLite")
		}
	}
	client.Close()
	fixture.stop()
	if fixture.peer.Close() != nil {
		test.Fatal("peer close before reopen")
	}
	reopened, err := state.Open(test.Context(), fixture.path, fixture.master)
	if err != nil {
		test.Fatal("actual SQLite reopen")
	}
	fixture.peer = reopened
	assertStored(test, fixture, created.Metadata.ID, 1, " synthetic-G1-key\t", wireDraft())
}

func TestReadViewClone(test *testing.T) {
	draft := wireDraft()
	wire := &configurationv1.DraftView{Asset: &configurationv1.Asset{Id: strings.Repeat("A", 26), AgentId: "grokbuild", Kind: configurationv1.AssetKind_ASSET_KIND_PROVIDER, Name: draft.Name, SchemaVersion: 1, Revision: 1, CreatedAtUnix: 1, UpdatedAtUnix: 1}, Draft: draft, Secrets: []*configurationv1.SecretStatus{{SlotId: keySlot}}, CredentialReadiness: configurationv1.CredentialReadiness_CREDENTIAL_READINESS_COMPLETE}
	view, err := viewFrom(wire)
	if err != nil {
		test.Fatal(err)
	}
	defer view.Close()
	draft.GetGrokbuildProvider().Model.Profile = "changed"
	if proto.Equal(view.state().draft, draft) {
		test.Fatal("view aliases response")
	}
}
