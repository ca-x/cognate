package server

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	configurationv1 "github.com/ca-x/cognate/gen/skillhub/configuration/v1"
	importv1 "github.com/ca-x/cognate/gen/skillhub/import/v1"
	"github.com/ca-x/cognate/gen/skillhub/import/v1/importv1connect"
	"google.golang.org/protobuf/proto"
)

func TestImportSelectionAppActivation(test *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "without-origin"
		if enabled {
			name = "canonical-origin"
		}
		test.Run(name, func(test *testing.T) {
			config, roots := nativeAppConfig(test)
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				test.Fatal("loopback listener unavailable")
			}
			test.Cleanup(func() { _ = listener.Close() })
			origin := "https://" + listener.Addr().String()
			if enabled {
				config.PublicOrigin = origin
			}
			app, err := Open(test.Context(), config)
			if err != nil {
				test.Fatal("application could not open")
			}
			test.Cleanup(func() {
				if err := app.Close(); err != nil {
					test.Error("application close failed")
				}
			})
			ctx, cancel := context.WithCancel(test.Context())
			finished := make(chan error, 1)
			go func() { finished <- app.Serve(ctx, listener) }()
			test.Cleanup(func() {
				cancel()
				select {
				case err := <-finished:
					if err != nil {
						test.Error("application serve failed")
					}
				case <-time.After(5 * time.Second):
					test.Error("application did not stop and join")
				}
			})
			transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
			test.Cleanup(transport.CloseIdleConnections)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			accounts := authv1connect.NewAuthServiceClient(client, origin)
			registered, err := accounts.Register(test.Context(), connect.NewRequest(&authv1.RegisterRequest{Username: "selection-app", Password: "synthetic selection app password"}))
			if err != nil {
				test.Fatal("TLS account registration failed")
			}
			login, err := accounts.Login(test.Context(), connect.NewRequest(&authv1.LoginRequest{Username: "selection-app", Password: "synthetic selection app password"}))
			if err != nil {
				test.Fatal("TLS account login failed")
			}
			for _, encoding := range []string{"proto", "json"} {
				var options []connect.ClientOption
				if encoding == "json" {
					options = append(options, connect.WithProtoJSON())
				}
				imports := importv1connect.NewImportServiceClient(client, origin, options...)
				if !enabled {
					for _, procedure := range []string{importv1connect.ImportServicePreviewImportSelectionProcedure, importv1connect.ImportServiceCommitImportSelectionProcedure, importv1connect.ImportServiceGetImportCommitStatusProcedure} {
						test.Run(encoding+procedure+"-before-body", func(test *testing.T) {
							selectionAppUnconfiguredBeforeBody(test, listener.Addr().String(), transport.TLSClientConfig, encoding, procedure, login.Msg.SessionToken)
						})
					}
				}
				if enabled {
					test.Run(encoding+"-configuration-roundtrip", func(test *testing.T) {
						selectionAppConfigurationRoundtrip(test, imports, login.Msg.SessionToken)
					})
				}
				preview := connect.NewRequest(&importv1.PreviewImportSelectionRequest{})
				commit := connect.NewRequest(&importv1.CommitImportSelectionRequest{})
				status := connect.NewRequest(&importv1.GetImportCommitStatusRequest{})
				for _, header := range []http.Header{preview.Header(), commit.Header(), status.Header()} {
					header.Set("Authorization", "Bearer "+login.Msg.SessionToken)
				}
				_, previewErr := imports.PreviewImportSelection(test.Context(), preview)
				_, commitErr := imports.CommitImportSelection(test.Context(), commit)
				_, statusErr := imports.GetImportCommitStatus(test.Context(), status)
				want := connect.CodeFailedPrecondition
				if enabled {
					want = connect.CodeInvalidArgument
				}
				for _, err := range []error{previewErr, commitErr, statusErr} {
					if connect.CodeOf(err) != want {
						test.Fatalf("application selection activation: expected=%s actual=%s", want, connect.CodeOf(err))
					}
				}
				legacySnapshot := connect.NewRequest(&importv1.PreviewCCSwitchSnapshotRequest{})
				legacySnapshot.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
				_, legacySnapshotErr := imports.PreviewCCSwitchSnapshot(test.Context(), legacySnapshot)
				legacyExport := connect.NewRequest(&importv1.PreviewLibraryExportRequest{})
				legacyExport.Header().Set("Authorization", "Bearer "+login.Msg.SessionToken)
				_, legacyExportErr := imports.PreviewLibraryExport(test.Context(), legacyExport)
				if connect.CodeOf(legacySnapshotErr) != connect.CodeInvalidArgument || connect.CodeOf(legacyExportErr) != connect.CodeInvalidArgument {
					test.Fatal("selection origin precondition changed legacy preview behavior")
				}
			}
			assets, err := app.store.ListConfigurationAssets(test.Context(), registered.Msg.UserId)
			wantAssets := 0
			if enabled {
				wantAssets = 6
			}
			if err != nil || len(assets) != wantAssets {
				test.Fatal("application asset count differs from explicitly selected imports")
			}
			repositories, err := app.store.ListCollectedRepositories(test.Context(), registered.Msg.UserId)
			if err != nil || len(repositories) != 0 {
				test.Fatal("configuration-only import collected an unselected repository")
			}
		})
	}
}

func selectionAppUnconfiguredBeforeBody(test *testing.T, address string, configuration *tls.Config, encoding, procedure, bearer string) {
	test.Helper()
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 2 * time.Second}, Config: configuration.Clone()}
	connection, err := dialer.DialContext(test.Context(), "tcp", address)
	if err != nil {
		test.Fatal("unconfigured application TLS connection failed")
	}
	defer connection.Close()
	if err := connection.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		test.Fatal("unconfigured application deadline failed")
	}
	contentType := "application/proto"
	if encoding == "json" {
		contentType = "application/json"
	}
	if _, err := fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: %s\r\nContent-Type: %s\r\nConnect-Protocol-Version: 1\r\nAuthorization: Bearer %s\r\nContent-Length: 4096\r\nConnection: close\r\n\r\n", procedure, address, contentType, bearer); err != nil {
		test.Fatal("unconfigured application header write failed")
	}
	response, err := http.ReadResponse(bufio.NewReader(connection), &http.Request{Method: http.MethodPost})
	if err != nil {
		test.Fatal("unconfigured application waited for a missing body")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusBadRequest {
		test.Fatal("unconfigured application returned an incorrect precondition status")
	}
}

func selectionAppConfigurationRoundtrip(test *testing.T, client importv1connect.ImportServiceClient, bearer string) {
	test.Helper()
	statements, err := os.ReadFile("importer/testdata/selection/ccswitch-schema24.sql")
	if err != nil {
		test.Fatal("synthetic snapshot schema unavailable")
	}
	database, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		test.Fatal("synthetic SQLite unavailable")
	}
	defer database.Close()
	connection, err := database.Conn(test.Context())
	if err != nil {
		test.Fatal("synthetic connection unavailable")
	}
	defer connection.Close()
	if _, err := connection.ExecContext(test.Context(), string(statements)); err != nil {
		test.Fatal("synthetic schema creation failed")
	}
	var source []byte
	err = connection.Raw(func(driver any) error {
		serializer, ok := driver.(interface{ Serialize() ([]byte, error) })
		if !ok {
			return errors.New("SQLite serializer unavailable")
		}
		var err error
		source, err = serializer.Serialize()
		return err
	})
	if err != nil {
		test.Fatal("synthetic snapshot serialization failed")
	}
	defer clear(source)
	identity := sha256.Sum256([]byte(test.Name()))
	previewMessage := &importv1.PreviewImportSelectionRequest{Source: importv1.ImportSelectionSource_IMPORT_SELECTION_SOURCE_CCSWITCH_SCHEMA24, SourceData: source}
	previewRequest := connect.NewRequest(previewMessage)
	previewRequest.Header().Set("Authorization", "Bearer "+bearer)
	preview, err := client.PreviewImportSelection(test.Context(), previewRequest)
	if err != nil {
		test.Fatalf("initial preview failed: %s", connect.CodeOf(err))
	}
	for _, candidate := range preview.Msg.Configurations {
		if candidate.KnownAgentId != "claude" && candidate.KnownAgentId != "codex" && candidate.KnownAgentId != "gemini" {
			continue
		}
		option := &importv1.ImportCandidateOptions{CandidateId: candidate.Id, Name: new(hex.EncodeToString(identity[:4]) + candidate.KnownAgentId)}
		switch candidate.KnownAgentId {
		case "claude":
			option.Auth = &importv1.ImportCandidateOptions_ClaudeAuthMode{ClaudeAuthMode: configurationv1.ClaudeAuthMode_CLAUDE_AUTH_MODE_API_KEY}
		case "gemini":
			option.GeminiKeyTransport = new(configurationv1.GeminiKeyTransport_GEMINI_KEY_TRANSPORT_BEARER)
		}
		previewMessage.Options = append(previewMessage.Options, option)
	}
	preview, err = client.PreviewImportSelection(test.Context(), previewRequest)
	if err != nil {
		test.Fatalf("explicit-choice preview failed: %s", connect.CodeOf(err))
	}
	selectionAppPublicResponse(test, preview.Msg)
	operation := identity[:16]
	commitMessage := &importv1.CommitImportSelectionRequest{Ticket: preview.Msg.Ticket, Source: previewMessage.Source, SourceData: source, Options: previewMessage.Options, OperationId: operation}
	for _, notice := range preview.Msg.Notices {
		commitMessage.AckNoticeIds = append(commitMessage.AckNoticeIds, notice.Id)
	}
	for _, candidate := range preview.Msg.Skills {
		for _, notice := range candidate.Notices {
			commitMessage.AckNoticeIds = append(commitMessage.AckNoticeIds, notice.Id)
		}
	}
	for _, candidate := range preview.Msg.Configurations {
		for _, notice := range candidate.Notices {
			commitMessage.AckNoticeIds = append(commitMessage.AckNoticeIds, notice.Id)
		}
		if candidate.KnownAgentId != "claude" && candidate.KnownAgentId != "codex" && candidate.KnownAgentId != "gemini" {
			continue
		}
		if candidate.Status != importv1.ImportCandidateStatus_IMPORT_CANDIDATE_STATUS_READY {
			test.Fatal("explicit provider choice did not become ready")
		}
		commitMessage.ConfigurationIds = append(commitMessage.ConfigurationIds, candidate.Id)
	}
	if len(commitMessage.ConfigurationIds) != 3 {
		test.Fatal("preview did not expose all three typed providers")
	}
	commitRequest := connect.NewRequest(commitMessage)
	commitRequest.Header().Set("Authorization", "Bearer "+bearer)
	committed, err := client.CommitImportSelection(test.Context(), commitRequest)
	if err != nil {
		test.Fatalf("configuration commit failed: %s", connect.CodeOf(err))
	}
	receipt := committed.Msg.Receipt
	selectionAppPublicResponse(test, committed.Msg)
	if receipt == nil || receipt.Phase != importv1.ImportReceiptPhase_IMPORT_RECEIPT_PHASE_COMPLETED || len(receipt.CreatedAssets) != 3 || len(receipt.RepositoryOutcomes) != 0 || len(receipt.SelectedSkillBindings) != 0 {
		test.Fatal("configuration-only commit returned an incorrect receipt")
	}
	statusRequest := connect.NewRequest(&importv1.GetImportCommitStatusRequest{OperationId: operation})
	statusRequest.Header().Set("Authorization", "Bearer "+bearer)
	status, err := client.GetImportCommitStatus(test.Context(), statusRequest)
	if err != nil || status.Msg.Observation != importv1.ImportCommitObservation_IMPORT_COMMIT_OBSERVATION_TERMINAL || !proto.Equal(status.Msg.Receipt, receipt) {
		test.Fatal("status did not return the committed durable receipt")
	}
	selectionAppPublicResponse(test, status.Msg)
	replayed, err := client.CommitImportSelection(test.Context(), commitRequest)
	if err != nil || !proto.Equal(replayed.Msg.Receipt, receipt) {
		test.Fatal("operation replay did not return the same durable receipt")
	}
}

func selectionAppPublicResponse(test *testing.T, message proto.Message) {
	test.Helper()
	encoded, err := proto.Marshal(message)
	defer clear(encoded)
	if err != nil || bytes.Contains(encoded, []byte("SYNTHETIC-PRIVATE-MARKER")) {
		test.Fatal("public import response exposed excluded source data")
	}
}
