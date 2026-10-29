package codexconfig

import (
	"bytes"
	"context"
	"encoding/json/jsontext"
	"errors"
	"io"
	"strings"
	"unicode/utf8"
)

type jsonField struct {
	name  string
	value *jsonNode
}
type jsonNode struct {
	kind     byte
	start    int
	end      int
	text     string
	members  []jsonField
	elements []*jsonNode
}
type jsonDocument struct {
	raw  []byte
	root *jsonNode
}
type jsonUpdate struct {
	name  string
	value []byte
}

func (node *jsonNode) member(name string) *jsonNode {
	for _, member := range node.members {
		if member.name == name {
			return member.value
		}
	}
	return nil
}
func jsonStart(raw []byte, offset int) int {
	for offset < len(raw) && bytes.ContainsRune([]byte(" \t\r\n,:"), rune(raw[offset])) {
		offset++
	}
	return offset
}
func jsonToken(ctx context.Context, decoder *jsontext.Decoder, tokens *int) (jsontext.Token, error) {
	if err := ctx.Err(); err != nil {
		return jsontext.Token{}, err
	}
	*tokens++
	if *tokens > 32768 {
		return jsontext.Token{}, ErrCodecLimit
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return jsontext.Token{}, ErrInvalidLocalImage
	}
	return token, nil
}
func readJSONNode(ctx context.Context, decoder *jsontext.Decoder, raw []byte, depth int, tokens *int) (*jsonNode, error) {
	start := jsonStart(raw, int(decoder.InputOffset()))
	token, err := jsonToken(ctx, decoder, tokens)
	if err != nil {
		return nil, err
	}
	node := &jsonNode{kind: byte(token.Kind()), start: start}
	if token.Kind() == '{' || token.Kind() == '[' {
		depth++
		if depth > 32 {
			return nil, ErrCodecLimit
		}
	}
	switch token.Kind() {
	case '{':
		seen := make(map[string]bool)
		for decoder.PeekKind() != '}' {
			if len(node.members) >= 512 {
				return nil, ErrCodecLimit
			}
			key, err := jsonToken(ctx, decoder, tokens)
			if err != nil {
				return nil, err
			}
			if key.Kind() != '"' || seen[key.String()] {
				return nil, ErrInvalidLocalImage
			}
			name := strings.Clone(key.String())
			seen[name] = true
			value, err := readJSONNode(ctx, decoder, raw, depth, tokens)
			if err != nil {
				return nil, err
			}
			node.members = append(node.members, jsonField{name: name, value: value})
		}
		if _, err := jsonToken(ctx, decoder, tokens); err != nil {
			return nil, err
		}
	case '[':
		for decoder.PeekKind() != ']' {
			if len(node.elements) >= 4096 {
				return nil, ErrCodecLimit
			}
			value, err := readJSONNode(ctx, decoder, raw, depth, tokens)
			if err != nil {
				return nil, err
			}
			node.elements = append(node.elements, value)
		}
		if _, err := jsonToken(ctx, decoder, tokens); err != nil {
			return nil, err
		}
	case '"':
		node.text = strings.Clone(token.String())
	case '0', 't', 'f', 'n':
	default:
		return nil, ErrInvalidLocalImage
	}
	node.end = int(decoder.InputOffset())
	return node, nil
}
func readJSON(ctx context.Context, raw []byte) (*jsonDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxImageBytes {
		return nil, ErrCodecLimit
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidLocalImage
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(raw))
	tokens := 0
	root, err := readJSONNode(ctx, decoder, raw, 0, &tokens)
	if err != nil {
		return nil, err
	}
	if root.kind != '{' {
		return nil, ErrInvalidLocalImage
	}
	if _, err := decoder.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, ErrInvalidLocalImage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return &jsonDocument{raw: raw, root: root}, nil
}

func rewriteJSONMembers(ctx context.Context, raw []byte, object *jsonNode, updates []jsonUpdate) ([]byte, int, error) {
	if object.kind != '{' {
		return nil, 0, ErrInvalidLocalImage
	}
	if len(updates) > maxEdits {
		return nil, 0, ErrCodecLimit
	}
	var edits []rawEdit
	var added []byte
	seen := make(map[string]bool, len(updates))
	for _, update := range updates {
		if seen[update.name] {
			return nil, 0, ErrInvalidLocalImage
		}
		seen[update.name] = true
		if existing := object.member(update.name); existing != nil {
			edits = append(edits, rawEdit{start: existing.start, end: existing.end, value: update.value})
		} else {
			if len(object.members) > 0 || len(added) > 0 {
				added = append(added, ',')
			}
			added, _ = jsontext.AppendQuote(added, update.name)
			added = append(added, ':')
			added = append(added, update.value...)
		}
	}
	if len(added) > 0 {
		offset := object.start + 1
		if len(object.members) > 0 {
			offset = object.members[len(object.members)-1].value.end
		}
		edits = append(edits, rawEdit{start: offset, end: offset, value: added})
	}
	result, err := applyEdits(ctx, raw, edits)
	if err != nil {
		return nil, 0, err
	}
	if _, err := readJSON(ctx, result); err != nil {
		clear(result)
		return nil, 0, err
	}
	return result, len(updates), nil
}
