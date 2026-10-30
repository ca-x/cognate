package geminiconfig

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
	name     string
	keyStart int
	value    *jsonNode
}
type jsonNode struct {
	kind       byte
	start, end int
	text       string
	members    []jsonField
	elements   []*jsonNode
}
type jsonDocument struct {
	raw, clean []byte
	root       *jsonNode
}
type jsonChange struct {
	path   []string
	value  []byte
	remove bool
}

func (document *jsonDocument) destroy() {
	clear(document.clean)
	document.clean = nil
	document.root = nil
}
func (node *jsonNode) member(name string) *jsonNode {
	for _, member := range node.members {
		if member.name == name {
			return member.value
		}
	}
	return nil
}
func stripComments(ctx context.Context, raw []byte) ([]byte, error) {
	clean := bytes.Clone(raw)
	quoted := false
	for offset := 0; offset < len(clean); offset++ {
		if err := checkContext(ctx); err != nil {
			clear(clean)
			return nil, err
		}
		character := clean[offset]
		if quoted {
			if character == '\\' {
				offset++
			} else if character == '"' {
				quoted = false
			}
			continue
		}
		if character == '"' {
			quoted = true
			continue
		}
		if character != '/' || offset+1 >= len(clean) {
			continue
		}
		if clean[offset+1] == '/' {
			for offset < len(clean) && clean[offset] != '\n' && clean[offset] != '\r' {
				clean[offset] = ' '
				offset++
			}
			offset--
		} else if clean[offset+1] == '*' {
			end := bytes.Index(clean[offset+2:], []byte("*/"))
			if end < 0 {
				clear(clean)
				return nil, ErrInvalidLocalImage
			}
			end += offset + 4
			for offset < end {
				if clean[offset] != '\n' && clean[offset] != '\r' {
					clean[offset] = ' '
				}
				offset++
			}
			offset--
		}
	}
	return clean, nil
}

func jsonStart(raw []byte, offset int) int {
	for offset < len(raw) && bytes.ContainsRune([]byte(" \t\r\n,:"), rune(raw[offset])) {
		offset++
	}
	return offset
}
func jsonToken(ctx context.Context, decoder *jsontext.Decoder, count *int) (jsontext.Token, error) {
	if err := checkContext(ctx); err != nil {
		return jsontext.Token{}, err
	}
	*count++
	if *count > 32768 {
		return jsontext.Token{}, ErrCodecLimit
	}
	token, err := decoder.ReadToken()
	if err != nil {
		return jsontext.Token{}, ErrInvalidLocalImage
	}
	return token, nil
}
func readJSONNode(ctx context.Context, decoder *jsontext.Decoder, clean []byte, depth int, count *int) (*jsonNode, error) {
	start := jsonStart(clean, int(decoder.InputOffset()))
	token, err := jsonToken(ctx, decoder, count)
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
			keyStart := jsonStart(clean, int(decoder.InputOffset()))
			key, err := jsonToken(ctx, decoder, count)
			if err != nil {
				return nil, err
			}
			if key.Kind() != '"' || seen[key.String()] {
				return nil, ErrInvalidLocalImage
			}
			name := strings.Clone(key.String())
			seen[name] = true
			value, err := readJSONNode(ctx, decoder, clean, depth, count)
			if err != nil {
				return nil, err
			}
			node.members = append(node.members, jsonField{name: name, keyStart: keyStart, value: value})
		}
		if _, err := jsonToken(ctx, decoder, count); err != nil {
			return nil, err
		}
	case '[':
		for decoder.PeekKind() != ']' {
			if len(node.elements) >= 4096 {
				return nil, ErrCodecLimit
			}
			value, err := readJSONNode(ctx, decoder, clean, depth, count)
			if err != nil {
				return nil, err
			}
			node.elements = append(node.elements, value)
		}
		if _, err := jsonToken(ctx, decoder, count); err != nil {
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
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if len(raw) > MaxImageBytes {
		return nil, ErrCodecLimit
	}
	if !utf8.Valid(raw) {
		return nil, ErrInvalidLocalImage
	}
	clean, err := stripComments(ctx, raw)
	if err != nil {
		return nil, err
	}
	decoder := jsontext.NewDecoder(bytes.NewReader(clean))
	count := 0
	root, err := readJSONNode(ctx, decoder, clean, 0, &count)
	if err == nil && root.kind != '{' {
		err = ErrInvalidLocalImage
	}
	if err == nil {
		if _, trailing := decoder.ReadToken(); !errors.Is(trailing, io.EOF) {
			err = ErrInvalidLocalImage
		}
	}
	if err == nil {
		err = checkContext(ctx)
	}
	if err != nil {
		clear(clean)
		return nil, err
	}
	return &jsonDocument{raw: raw, clean: clean, root: root}, nil
}

func jsonEdits(document *jsonDocument, object *jsonNode, path []string, value []byte, remove bool) ([]rawEdit, error) {
	if len(path) < 1 || len(path) > 3 || object.kind != '{' {
		return nil, ErrInvalidLocalImage
	}
	index := -1
	for position, member := range object.members {
		if member.name == path[0] {
			index = position
			break
		}
	}
	if index >= 0 {
		member := object.members[index]
		if len(path) > 1 {
			return jsonEdits(document, member.value, path[1:], value, remove)
		}
		if !remove {
			return []rawEdit{{start: member.value.start, end: member.value.end, value: bytes.Clone(value)}}, nil
		}
		edits := []rawEdit{{start: member.keyStart, end: member.value.end}}
		if len(object.members) > 1 {
			start, end := member.value.end, object.members[len(object.members)-1].value.end
			if index+1 < len(object.members) {
				end = object.members[index+1].keyStart
			} else {
				start, end = object.members[index-1].value.end, member.keyStart
			}
			comma := bytes.IndexByte(document.clean[start:end], ',')
			if comma < 0 {
				return nil, ErrInvalidLocalImage
			}
			edits = append(edits, rawEdit{start: start + comma, end: start + comma + 1})
		}
		return edits, nil
	}
	if remove {
		return nil, nil
	}
	encoded := bytes.Clone(value)
	for offset := len(path) - 1; offset >= 0; offset-- {
		next := []byte{'{'}
		next, _ = jsontext.AppendQuote(next, path[offset])
		next = append(next, ':')
		next = append(next, encoded...)
		next = append(next, '}')
		clear(encoded)
		encoded = next
	}
	inside := bytes.Clone(encoded[1 : len(encoded)-1])
	clear(encoded)
	if len(object.members) != 0 {
		inside = append([]byte{','}, inside...)
	}
	return []rawEdit{{start: object.end - 1, end: object.end - 1, value: inside}}, nil
}

func rewriteSettings(ctx context.Context, document *jsonDocument, updates []jsonChange) ([]byte, error) {
	if document == nil || len(updates) > 6 {
		return nil, ErrInvalidLocalImage
	}
	result := bytes.Clone(document.raw)
	for _, update := range updates {
		parsed, err := readJSON(ctx, result)
		if err != nil {
			clear(result)
			return nil, err
		}
		edits, err := jsonEdits(parsed, parsed.root, update.path, update.value, update.remove)
		if err == nil {
			var next []byte
			next, err = applyEdits(ctx, result, edits)
			clear(result)
			result = next
		}
		for _, edit := range edits {
			clear(edit.value)
		}
		parsed.destroy()
		if err != nil {
			clear(result)
			return nil, err
		}
	}
	parsed, err := readJSON(ctx, result)
	if err != nil {
		clear(result)
		return nil, err
	}
	parsed.destroy()
	if err := checkContext(ctx); err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}
