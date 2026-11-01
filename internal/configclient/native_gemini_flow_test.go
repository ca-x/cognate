package configclient

import (
	"bytes"
	"encoding/base64"
	"encoding/json/v2"
	"testing"
	"time"

	nativev1 "github.com/ca-x/cognate/gen/skillhub/configuration/native/v1"
	"google.golang.org/protobuf/proto"
)

func TestGeminiRestartTicketStrictStatusOnlyAuthority(test *testing.T) {
	now := time.UnixMilli(1700000000000)
	operation := [16]byte(bytes.Repeat([]byte{1}, 16))
	request := &nativev1.GetNativeMaterializationStatusRequest{PrepareId: "prepare-synthetic", OperationId: operation[:], ClientNonce: bytes.Repeat([]byte{2}, 32), ProofSignature: bytes.Repeat([]byte{3}, 64)}
	wire, err := proto.Marshal(request)
	if err != nil {
		test.Fatal("signed status fixture")
	}
	base := geminiStatusTicketBody{Version: 1, Origin: "https://synthetic.invalid", Owner: "owner-synthetic", Operation: operation, Expires: now.Add(time.Minute).UnixMilli(), Request: wire, Bearer: []byte("cn1." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)))}
	body, _ := json.Marshal(base)
	stored, decoded, err := parseGeminiStatusTicket(body, base.Origin, base.Owner, now)
	if err != nil || decoded == nil || decoded.GrantId != "" || !proto.Equal(decoded, request) {
		test.Fatal("legitimate status-only ticket rejected")
	}
	clear(stored.Bearer)
	clear(stored.Request)
	for name, mutate := range map[string]func(*geminiStatusTicketBody){"owner": func(value *geminiStatusTicketBody) { value.Owner = "other-owner" }, "origin": func(value *geminiStatusTicketBody) { value.Origin = "https://other.invalid" }, "expired": func(value *geminiStatusTicketBody) { value.Expires = now.UnixMilli() }, "operation": func(value *geminiStatusTicketBody) { value.Operation[0] ^= 1 }, "version": func(value *geminiStatusTicketBody) { value.Version = 2 }, "bearer-family": func(value *geminiStatusTicketBody) { value.Bearer = bytes.Repeat([]byte{'x'}, 47) }, "grant-authority": func(value *geminiStatusTicketBody) {
		copyRequest := proto.Clone(request).(*nativev1.GetNativeMaterializationStatusRequest)
		copyRequest.GrantId = "unapproved-grant"
		value.Request, _ = proto.Marshal(copyRequest)
	}, "consume-request": func(value *geminiStatusTicketBody) {
		value.Request, _ = proto.Marshal(&nativev1.ConsumeNativeMaterializationRequest{PrepareId: request.PrepareId, GrantId: "grant", GrantToken: bytes.Repeat([]byte{9}, 32), OperationId: operation[:], IntentCommitment: bytes.Repeat([]byte{8}, 32), ClientNonce: request.ClientNonce, ProofSignature: request.ProofSignature})
	}} {
		test.Run(name, func(test *testing.T) {
			changed := base
			mutate(&changed)
			encoded, _ := json.Marshal(changed)
			result, decoded, err := parseGeminiStatusTicket(encoded, base.Origin, base.Owner, now)
			if err == nil || decoded != nil || len(result.Bearer) != 0 || len(result.Request) != 0 {
				test.Fatal("malformed or broader-authority restart ticket accepted/returned private bytes")
			}
		})
	}
	withSigner := append(bytes.Clone(body[:len(body)-1]), []byte(`,"PrivateKey":"synthetic-private-key"}`)...)
	if _, _, err := parseGeminiStatusTicket(withSigner, base.Origin, base.Owner, now); err == nil {
		test.Fatal("restart ticket accepted unknown signer member")
	}
	if _, _, err := parseGeminiStatusTicket(bytes.Repeat([]byte{'x'}, 8193), base.Origin, base.Owner, now); err == nil {
		test.Fatal("unbounded restart ticket accepted")
	}
}
