package httpapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	catalogv1 "github.com/ca-x/cognate/gen/skillhub/catalog/v1"
	"github.com/ca-x/cognate/gen/skillhub/catalog/v1/catalogv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/configuration/v1/configurationv1connect"
	packconfigurationv1 "github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/configuration/v1/packconfigurationv1connect"
	packv1 "github.com/ca-x/cognate/gen/skillhub/pack/v1"
	"github.com/ca-x/cognate/gen/skillhub/pack/v1/packv1connect"
	"github.com/ca-x/cognate/internal/server/configuration"
	"github.com/ca-x/cognate/internal/server/mcpaccess"
	"github.com/ca-x/cognate/internal/server/publiccatalog"
	"github.com/ca-x/cognate/internal/server/state"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func TestPackConfigurationPrivacyAndSkillOnlyPublicSurface(test *testing.T) {
	for _, format := range []string{"text", "json"} {
		test.Run(format, func(test *testing.T) {
			fixture := newPackConfigurationFixture(test)
			logs := configurationCaptureLogs(test, format)
			responses := &configurationPrivacyBuffer{}
			manager, logRoot := testLogManager(test)
			catalog, err := publiccatalog.NewService(fixture.store, packConfigurationArchive{})
			if err != nil {
				test.Fatal(err)
			}
			test.Cleanup(func() { _ = catalog.Close() })
			endpoint := httptest.NewUnstartedServer(NewWithOptions(fixture.auth, Options{Store: fixture.store, Packs: fixture.service, Configuration: configuration.New(fixture.store), PublicCatalog: catalog, Logging: manager}))
			endpoint.Config.ErrorLog = log.New(logs, "", 0)
			endpoint.Config.ErrorLog.Print("pc2-http-log-positive-control")
			endpoint.StartTLS()
			test.Cleanup(endpoint.Close)
			endpoint.Client().Transport = configurationPrivacyTransport{base: endpoint.Client().Transport, responses: responses}
			issued, err := fixture.service.CreateShare(test.Context(), fixture.owner, fixture.pack.ID, nil)
			if err != nil {
				test.Fatal(err)
			}
			guest := packv1connect.NewDistributionServiceClient(endpoint.Client(), endpoint.URL)
			manifestRequest := func() *connect.Request[packv1.GetManifestRequest] {
				return configurationRequest(&packv1.GetManifestRequest{}, issued.Token.Reveal())
			}
			before, err := guest.GetManifest(test.Context(), manifestRequest())
			if err != nil {
				test.Fatal(err)
			}
			configurationClient := configurationv1connect.NewConfigurationServiceClient(endpoint.Client(), endpoint.URL)
			created, err := configurationClient.CreateAsset(test.Context(), configurationRequest(&configurationv1.CreateAssetRequest{Draft: configurationDraft("pc2-secret-asset-name-canary"), Secrets: []*configurationv1.SecretMutation{{SlotId: "claude.api-key", Action: &configurationv1.SecretMutation_Replace{Replace: "pc2-real-secret-canary"}}}}, fixture.session))
			if err != nil {
				test.Fatal("create privacy fixture")
			}
			configurationAssertStoredSecret(test, fixture.store, fixture.owner, created.Msg.View.Asset.Id, 1, "pc2-real-secret-canary")
			responses = &configurationPrivacyBuffer{}
			endpoint.Client().Transport = configurationPrivacyTransport{base: fixture.endpoint.Client().Transport, responses: responses}
			client := packconfigurationv1connect.NewPackConfigurationServiceClient(endpoint.Client(), endpoint.URL)
			changed, err := client.ReplaceAttachments(test.Context(), configurationRequest(&packconfigurationv1.ReplaceAttachmentsRequest{PackId: fixture.pack.ID, ExpectedPackRevision: 1, ExpectedConfigurationRevision: 1, References: []*packconfigurationv1.AttachmentReference{{AssetId: created.Msg.View.Asset.Id, ExpectedAssetRevision: 1, TargetId: "claude.provider.user"}}}, fixture.session))
			if err != nil {
				test.Fatal("private replacement")
			}
			for _, encoding := range []string{"json", "proto"} {
				body := []byte(`{"pc2-malformed-private-canary":"pc2-real-secret-canary"}`)
				if encoding == "proto" {
					body = []byte{0xff}
				}
				status, output := configurationRaw(test, endpoint, fixture.session, packconfigurationv1connect.PackConfigurationServiceReplaceAttachmentsProcedure, "application/"+encoding, body)
				if status != http.StatusBadRequest || strings.Contains(output, "canary") {
					test.Fatal("malformed request leaked")
				}
			}
			after, err := guest.GetManifest(test.Context(), manifestRequest())
			if err != nil {
				test.Fatal("share stopped following skill revision")
			}
			expected := proto.Clone(before.Msg).(*packv1.GetManifestResponse)
			expected.Revision++
			var transcript []byte
			appendStrings := func(values ...string) {
				for _, value := range values {
					transcript = binary.BigEndian.AppendUint64(transcript, uint64(len(value)))
					transcript = append(transcript, value...)
				}
			}
			appendStrings("skillhub/pack-manifest/v1", expected.PackId, expected.Name, expected.Description)
			transcript = binary.BigEndian.AppendUint64(transcript, uint64(expected.Revision))
			for _, entry := range expected.Skills {
				appendStrings(entry.SkillId, entry.Slug, entry.Name, entry.Description, entry.ContentHash, entry.RepositoryId, entry.RepositoryName, entry.Path)
			}
			digest := sha256.Sum256(transcript)
			expected.ManifestHash = hex.EncodeToString(digest[:])
			if !proto.Equal(after.Msg, expected) {
				test.Fatal("legacy manifest fields or hash changed beyond permitted Pack revision")
			}
			manifestJSON, err := protojson.Marshal(after.Msg)
			if err != nil {
				test.Fatal(err)
			}
			for _, private := range []string{created.Msg.View.Asset.Id, changed.Msg.Snapshot.Entries[0].AttachmentId, "pc2-real-secret-canary", "pc2-secret-asset-name-canary", "snapshotDigest", "configurationRevision"} {
				if bytes.Contains(manifestJSON, []byte(private)) {
					test.Fatal("share manifest exposed private attachments")
				}
			}
			access, err := mcpaccess.NewService(fixture.store)
			if err != nil {
				test.Fatal(err)
			}
			_, mcpToken, err := access.Create(test.Context(), fixture.owner, state.MCPMetadata{Name: "MCP credential", Scopes: []string{state.MCPCatalogRead}, ExpiresAt: time.Now().Add(time.Hour)})
			if err != nil {
				test.Fatal(err)
			}
			for _, token := range []string{issued.Token.Reveal(), mcpToken.Reveal()} {
				if response, err := client.GetAttachments(test.Context(), configurationRequest(&packconfigurationv1.GetAttachmentsRequest{PackId: fixture.pack.ID}, token)); response != nil || connect.CodeOf(err) != connect.CodeUnauthenticated {
					test.Fatal("real share/MCP capability granted owner access")
				}
			}
			public := catalogv1connect.NewPublicCatalogServiceClient(endpoint.Client(), endpoint.URL)
			publicPage, err := public.ListSkills(test.Context(), connect.NewRequest(&catalogv1.ListSkillsRequest{Limit: 10}))
			if err != nil || len(publicPage.Msg.Skills) != 0 {
				test.Fatal("private Pack/config appeared in public catalog")
			}
			if err := manager.Close(); err != nil {
				test.Fatal(err)
			}
			files, err := os.ReadDir(filepath.Join(logRoot, "logs"))
			if err != nil || len(files) == 0 {
				test.Fatal("managed request log capture not exercised")
			}
			var managed []byte
			for _, file := range files {
				if file.IsDir() {
					continue
				}
				data, err := os.ReadFile(filepath.Join(logRoot, "logs", file.Name()))
				if err != nil {
					test.Fatal(err)
				}
				managed = append(managed, data...)
			}
			if !bytes.Contains(managed, []byte("request")) || !logs.contains("privacy-stdlog-capture-control") || !logs.contains("pc2-http-log-positive-control") {
				test.Fatal("logging positive controls absent")
			}
			for _, private := range []string{"pc2-real-secret-canary", "pc2-malformed-private-canary", fixture.session, issued.Token.Reveal(), mcpToken.Reveal()} {
				if logs.contains(private) || responses.contains(private) || bytes.Contains(managed, []byte(private)) {
					test.Fatal("attachment path disclosed credential/private body")
				}
			}
			if responses.contains("pc2-secret-asset-name-canary") {
				test.Fatal("attachment snapshot disclosed asset name")
			}
		})
	}
}
