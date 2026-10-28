package packs

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"log/slog"
	"strings"
	"time"

	"github.com/ca-x/cognate/internal/server/state"
)

type ShareToken struct{ value string }

func (ShareToken) String() string       { return "[redacted pack share]" }
func (ShareToken) GoString() string     { return "[redacted pack share]" }
func (ShareToken) LogValue() slog.Value { return slog.StringValue("[redacted pack share]") }
func (token ShareToken) Reveal() string { return token.value }

type IssuedShare struct {
	Info  state.PackShare
	Token ShareToken
}

type capability struct {
	id     string
	digest [32]byte
}

func (service *Service) CreateShare(ctx context.Context, user, packID string, expiry *time.Time) (IssuedShare, error) {
	ctx, done, err := service.BeginRequest(ctx)
	if err != nil {
		return IssuedShare{}, err
	}
	defer done()
	now := service.now().Truncate(time.Second)
	var expires int64
	if expiry != nil {
		normalized := expiry.Truncate(time.Second)
		if !normalized.After(now) || normalized.After(now.Add(366*24*time.Hour)) {
			return IssuedShare{}, state.ErrPackInvalid
		}
		expiry, expires = &normalized, normalized.Unix()
	}
	id := rand.Text()
	payload := make([]byte, 75, 107)
	payload[0] = 1
	copy(payload[1:27], id)
	if _, err := rand.Read(payload[27:59]); err != nil {
		return IssuedShare{}, err
	}
	binary.BigEndian.PutUint64(payload[59:67], uint64(now.Unix()))
	binary.BigEndian.PutUint64(payload[67:75], uint64(expires))
	mac := hmac.New(sha256.New, service.signing)
	_, _ = mac.Write(payload)
	raw := "shp1." + base64.RawURLEncoding.EncodeToString(mac.Sum(payload))
	digest := sha256.Sum256([]byte(raw))
	info, err := service.store.CreatePackShare(ctx, user, packID, id, digest[:], expiry)
	if err != nil {
		return IssuedShare{}, err
	}
	return IssuedShare{Info: info, Token: ShareToken{value: raw}}, nil
}

func (service *Service) parse(raw string) (capability, error) {
	encoded, valid := strings.CutPrefix(raw, "shp1.")
	if !valid || len(encoded) != 143 {
		return capability{}, state.ErrPackShareUnavailable
	}
	payload, err := base64.RawURLEncoding.Strict().DecodeString(encoded)
	if err != nil || len(payload) != 107 || payload[0] != 1 {
		return capability{}, state.ErrPackShareUnavailable
	}
	mac := hmac.New(sha256.New, service.signing)
	_, _ = mac.Write(payload[:75])
	if !hmac.Equal(mac.Sum(nil), payload[75:]) {
		return capability{}, state.ErrPackShareUnavailable
	}
	issued, expires := int64(binary.BigEndian.Uint64(payload[59:67])), int64(binary.BigEndian.Uint64(payload[67:75]))
	now := service.now().Unix()
	if issued <= 0 || issued > now+30 || expires < 0 || expires != 0 && (expires <= now || expires <= issued || expires-issued > int64(366*24*time.Hour/time.Second)) {
		return capability{}, state.ErrPackShareUnavailable
	}
	return capability{id: string(payload[1:27]), digest: sha256.Sum256([]byte(raw))}, nil
}
