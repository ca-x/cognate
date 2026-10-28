package httpapi

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

type configurationPrivacyBuffer struct {
	mutex sync.Mutex
	data  bytes.Buffer
}

func (buffer *configurationPrivacyBuffer) Write(data []byte) (int, error) {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return buffer.data.Write(data)
}

func (buffer *configurationPrivacyBuffer) contains(value string) bool {
	buffer.mutex.Lock()
	defer buffer.mutex.Unlock()
	return bytes.Contains(buffer.data.Bytes(), []byte(value))
}

type configurationPrivacyTransport struct {
	base      http.RoundTripper
	responses io.Writer
}

func (transport configurationPrivacyTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := transport.base.RoundTrip(request)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	decoded := data
	if response.Header.Get("Content-Encoding") == "gzip" {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, errors.New("invalid privacy fixture compressed response")
		}
		decoded, err = io.ReadAll(io.LimitReader(reader, (2<<20)+1))
		_ = reader.Close()
		if err != nil {
			return nil, errors.New("invalid privacy fixture compressed response")
		}
	}
	_, _ = transport.responses.Write(decoded)
	_ = response.Header.Write(transport.responses)
	response.Body = io.NopCloser(bytes.NewReader(data))
	return response, nil
}

func configurationPrivacyServer(test *testing.T, handler http.Handler, logs, responses *configurationPrivacyBuffer) *httptest.Server {
	test.Helper()
	endpoint := httptest.NewUnstartedServer(handler)
	endpoint.Config.ErrorLog = log.New(logs, "http-error ", 0)
	endpoint.Config.ErrorLog.Print("privacy-http-capture-control")
	endpoint.Start()
	test.Cleanup(endpoint.Close)
	client := endpoint.Client()
	client.Transport = configurationPrivacyTransport{base: client.Transport, responses: responses}
	return endpoint
}

func configurationCaptureLogs(test *testing.T, format string) *configurationPrivacyBuffer {
	test.Helper()
	previousLogger, previousWriter := slog.Default(), log.Writer()
	previousFlags, previousPrefix := log.Flags(), log.Prefix()
	test.Cleanup(func() {
		slog.SetDefault(previousLogger)
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
		log.SetPrefix(previousPrefix)
	})
	buffer := &configurationPrivacyBuffer{}
	options := &slog.HandlerOptions{Level: slog.LevelDebug}
	var handler slog.Handler = slog.NewTextHandler(buffer, options)
	if format == "json" {
		handler = slog.NewJSONHandler(buffer, options)
	}
	slog.SetDefault(slog.New(handler))
	log.SetOutput(buffer)
	log.SetFlags(0)
	log.SetPrefix("")
	log.Print("privacy-stdlog-capture-control")
	slog.Debug("privacy-slog-capture-control")
	return buffer
}

func configurationAssertStoredSecret(test *testing.T, store *state.Store, owner, id string, revision int64, wanted string) {
	test.Helper()
	metadata, payload, err := store.ReadConfigurationAssetVersion(test.Context(), owner, id)
	if err != nil || metadata.Revision != revision {
		test.Fatal("configuration privacy fixture lost its stored revision")
	}
	data := payload.Reveal()
	defer clear(data)
	var stored struct {
		Secrets struct {
			APIKey *string `json:"api_key"`
		} `json:"secrets"`
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		test.Fatal("cannot decode the synthetic fixture stored secret")
	}
	if (wanted == "" && stored.Secrets.APIKey != nil) || (wanted != "" && (stored.Secrets.APIKey == nil || *stored.Secrets.APIKey != wanted)) {
		test.Fatal("wire mutation did not preserve the expected stored secret")
	}
}

type configurationPrivacyOversize struct {
	configurationv1connect.UnimplementedConfigurationServiceHandler
	private string
}

func (fixture configurationPrivacyOversize) ListAssets(context.Context, *connect.Request[configurationv1.ListAssetsRequest]) (*connect.Response[configurationv1.ListAssetsResponse], error) {
	return connect.NewResponse(&configurationv1.ListAssetsResponse{Assets: []*configurationv1.Asset{{Name: fixture.private + strings.Repeat("x", 1<<20)}}}), nil
}

func TestConfigurationHTTPLoggingPrivacy(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			logs := configurationCaptureLogs(test, format)
			responses := &configurationPrivacyBuffer{}
			sensitive := []string{"privacy-replacement-first", "privacy-replacement-second", "privacy-invalid-session", "privacy-internal-error", "privacy-unknown-slot", "privacy-unknown-field"}
			test.Cleanup(func() {
				for _, value := range sensitive {
					if logs.contains(value) {
						test.Error("configuration HTTP path logged sensitive data")
					}
					if responses.contains(value) {
						test.Error("configuration HTTP response echoed sensitive data")
					}
				}
				for _, control := range []string{"privacy-stdlog-capture-control", "privacy-slog-capture-control", "privacy-http-capture-control"} {
					if !logs.contains(control) {
						test.Errorf("configuration privacy capture channel was not exercised: %s", control)
					}
				}
				if !responses.contains("Privacy fixture") {
					test.Error("configuration privacy response capture was not exercised")
				}
			})
			for _, encoding := range []string{"proto", "json"} {
				test.Run(encoding, func(test *testing.T) {
					service, store := testServiceAndStore(test)
					owner, session := mcpSession(test, service, "configuration-privacy")
					sensitive = append(sensitive, session)
					endpoint := configurationPrivacyServer(test, NewWithOptions(service, Options{Configuration: configuration.New(store)}), logs, responses)
					var options []connect.ClientOption
					if encoding == "json" {
						options = append(options, connect.WithProtoJSON())
					}
					client := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL, options...)
					created, err := client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Privacy fixture"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[0]}}}}, session))
					if err != nil {
						test.Fatalf("privacy fixture create: %s", connect.CodeOf(err))
					}
					id := created.Msg.View.Asset.Id
					configurationAssertStoredSecret(test, store, owner, id, 1, sensitive[0])
					mutations := []*configurationv1.SecretMutation{
						{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[1]}},
						{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Keep{Keep: &configurationv1.KeepSecret{}}},
						{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Clear{Clear: &configurationv1.ClearSecret{}}},
					}
					for index, mutation := range mutations {
						updated, err := client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: int64(index + 1), Draft: configurationDraft("Privacy fixture"), Secrets: []*configurationv1.SecretMutation{mutation}}, session))
						if err != nil || updated.Msg.View.Secrets[0].Present != (index < 2) {
							test.Fatalf("privacy fixture mutation: %s", connect.CodeOf(err))
						}
						wanted := sensitive[1]
						if index == 2 {
							wanted = ""
						}
						configurationAssertStoredSecret(test, store, owner, id, int64(index+2), wanted)
					}
					_, err = client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Invalid"), Secrets: []*configurationv1.SecretMutation{{SlotId: sensitive[4], Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[0]}}}}, session))
					if connect.CodeOf(err) != connect.CodeInvalidArgument {
						test.Fatal("invalid mutation accepted")
					}
					_, err = client.UpdateAsset(test.Context(), configurationRequest(&configurationv1.UpdateAssetRequest{Id: id, ExpectedRevision: 4, Draft: configurationDraft("Invalid"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[1] + "\r\n"}}}}, session))
					if connect.CodeOf(err) != connect.CodeInvalidArgument {
						test.Fatal("invalid update mutation accepted")
					}
					_, err = client.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, sensitive[2]))
					if connect.CodeOf(err) != connect.CodeUnauthenticated {
						test.Fatal("invalid session accepted")
					}
					_, err = client.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("Oversize"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[0] + strings.Repeat("x", 128<<10)}}}}, session))
					if err == nil {
						test.Fatal("oversize request accepted")
					}
					for _, invalid := range []string{
						`{"draft":{"name":"Private","claudeProvider":{"authMode":2,"privacy-unknown-field":"privacy-replacement-first"}}}`,
						`{"draft":{"name":"Private","claudeProvider":{"authMode":2}},"secrets":[{"slotId":"claude.api-key","replace":"privacy-replacement-second`,
					} {
						status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/json", []byte(invalid))
						if status != http.StatusBadRequest {
							test.Fatal("strict JSON privacy fixture accepted")
						}
					}
					message := &configurationv1.CreateAssetRequest{Draft: configurationDraft("Unknown binary"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: sensitive[0]}}}}
					unknown := protowire.AppendTag(nil, 99, protowire.BytesType)
					message.Draft.ProtoReflect().SetUnknown(protowire.AppendString(unknown, sensitive[5]))
					data, err := proto.Marshal(message)
					if err != nil {
						test.Fatal("cannot encode strict binary privacy fixture")
					}
					status, _ := configurationRaw(test, endpoint, session, configurationv1connect.ConfigurationServiceCreateAssetProcedure, "application/proto", data)
					if status != http.StatusBadRequest {
						test.Fatal("strict binary privacy fixture accepted")
					}
					forbidden, err := http.NewRequestWithContext(test.Context(), http.MethodPost, endpoint.URL+configurationv1connect.ConfigurationServiceCreateAssetProcedure, strings.NewReader(`{"secrets":[{"replace":"privacy-replacement-first"}]}`))
					if err != nil {
						test.Fatal("cannot create HTTP guard privacy fixture")
					}
					forbidden.Header.Set("Authorization", "Bearer "+session)
					forbidden.Header.Set("Content-Type", "application/json")
					forbidden.Header.Set("Origin", "https://privacy-unknown-field.invalid")
					rejected, err := endpoint.Client().Do(forbidden)
					if err != nil {
						test.Fatal("HTTP guard privacy fixture failed")
					}
					_ = rejected.Body.Close()
					if rejected.StatusCode != http.StatusForbidden {
						test.Fatal("HTTP guard privacy fixture accepted")
					}
					fault := configurationPrivacyServer(test, NewWithOptions(service, Options{Configuration: configuration.New(&configurationFaultStore{Store: store, err: errors.New(sensitive[3])})}), logs, responses)
					faultClient := configurationv1connect.NewConfigurationServiceClient(fault.Client(), fault.URL, options...)
					_, err = faultClient.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
					if connect.CodeOf(err) != connect.CodeInternal {
						test.Fatal("fault privacy fixture did not fail")
					}
					_, oversized := configurationTransport(configurationPrivacyOversize{private: sensitive[0]})
					oversize := configurationPrivacyServer(test, oversized, logs, responses)
					oversizeClient := configurationv1connect.NewConfigurationServiceClient(oversize.Client(), oversize.URL, options...)
					_, err = oversizeClient.ListAssets(test.Context(), configurationRequest(&configurationv1.ListAssetsRequest{}, session))
					if connect.CodeOf(err) != connect.CodeInternal {
						test.Fatal("oversize response fixture did not fail")
					}
					if _, err := client.ReadDraft(test.Context(), configurationRequest(&configurationv1.ReadDraftRequest{Id: id}, session)); err != nil {
						test.Fatal("privacy error fixtures prevented later reads")
					}
					configurationAssertStoredSecret(test, store, owner, id, 4, "")
				})
			}
		})
	}
}

func TestConfigurationProductionDoesNotImportLogging(test *testing.T) {
	paths, err := filepath.Glob("configuration*.go")
	if err != nil {
		test.Fatal(err)
	}
	paths = append(paths, "auth.go", "guard.go")
	for _, path := range paths {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			test.Fatal("cannot parse configuration production source")
		}
		printing := map[string]bool{}
		for _, imported := range file.Imports {
			name, err := strconv.Unquote(imported.Path.Value)
			if err != nil {
				test.Fatal("cannot read configuration production import")
			}
			if name == "log" || name == "log/slog" {
				test.Errorf("configuration production source imports a logger: %s", path)
			}
			if name == "fmt" {
				alias := "fmt"
				if imported.Name != nil {
					alias = imported.Name.Name
				}
				printing[alias] = true
			}
		}
		ast.Inspect(file, func(node ast.Node) bool {
			if selector, ok := node.(*ast.SelectorExpr); ok {
				switch selector.Sel.Name {
				case "Logger", "BodyDump", "BodyDumpWithConfig", "RequestLogger", "RequestLoggerWithConfig", "LoggerWithConfig":
					test.Errorf("configuration HTTP path adds a logging hook: %s", path)
				}
			}
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			if name, ok := call.Fun.(*ast.Ident); ok && (name.Name == "print" || name.Name == "println") {
				test.Errorf("configuration production source prints: %s", path)
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
				if receiver, ok := selector.X.(*ast.Ident); ok && printing[receiver.Name] && (strings.HasPrefix(selector.Sel.Name, "Print") || strings.HasPrefix(selector.Sel.Name, "Fprint")) {
					test.Errorf("configuration production source prints: %s", path)
				}
			}
			return true
		})
	}
}
