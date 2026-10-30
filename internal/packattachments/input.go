package packattachments

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

var ErrInvalid = errors.New("pack attachment input or response invalid")
var ErrUnsupported = errors.New("pack attachment file input unsupported on this platform")

type Reference struct {
	AssetID               string `json:"assetId"`
	ExpectedAssetRevision int64  `json:"expectedAssetRevision"`
	TargetID              string `json:"targetId"`
}

func ReadReferences(ctx context.Context, path string) ([]Reference, error) {
	data, err := readReferencesFile(ctx, path)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	if len(data) > 64<<10 || !utf8.Valid(data) || len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '[' {
		return nil, ErrInvalid
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(data))
	depth := 0
	for {
		token, err := decoder.ReadToken()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, ErrInvalid
		}
		switch token.Kind() {
		case '[', '{':
			depth++
			if depth > 2 {
				return nil, ErrInvalid
			}
		case ']', '}':
			depth--
		case '"':
		case '0':
			number := token.String()
			if number == "" || number[0] < '1' || number[0] > '9' || strings.ContainsAny(number, ".-+eE") {
				return nil, ErrInvalid
			}
		default:
			return nil, ErrInvalid
		}
	}
	var refs []Reference
	if json.Unmarshal(data, &refs, json.RejectUnknownMembers(true)) != nil || !validReferences(refs) {
		return nil, ErrInvalid
	}
	return refs, nil
}

func validID(value string) bool {
	if len(value) != 26 {
		return false
	}
	for _, character := range value {
		if !(character >= 'A' && character <= 'Z' || character >= '2' && character <= '7') {
			return false
		}
	}
	return true
}

func validTarget(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if !(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '.' || character == '_') {
			return false
		}
	}
	return true
}

func validReferences(refs []Reference) bool {
	if refs == nil || len(refs) > 16 {
		return false
	}
	seen := make(map[string]bool, len(refs))
	for _, ref := range refs {
		key := ref.AssetID + "/" + ref.TargetID
		if !validID(ref.AssetID) || ref.ExpectedAssetRevision < 1 || !validTarget(ref.TargetID) || seen[key] {
			return false
		}
		seen[key] = true
	}
	return true
}
