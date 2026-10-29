package codexconfig

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json/v2"
	"slices"
	"strings"

	"github.com/ca-x/cognate/internal/agentconfig"
	"github.com/pelletier/go-toml/v2"
	"github.com/pelletier/go-toml/v2/unstable"
)

type rawEdit struct {
	start int
	end   int
	value []byte
}
type tomlLeaf struct {
	start int
	end   int
	kind  unstable.Kind
}
type tomlSection struct {
	path   []string
	anchor int
	end    int
}
type tomlDocument struct {
	raw        []byte
	semantic   map[string]any
	leaves     map[string]tomlLeaf
	sections   []tomlSection
	rootAnchor int
	endAnchor  int
	closed     bool
	dotted     bool
}

func pathID(parts []string) string { encoded, _ := json.Marshal(parts); return string(encoded) }
func providerPath(parts []string) bool {
	return len(parts) >= 2 && parts[0] == "model_providers" && parts[1] == "cognate"
}
func lineStart(raw []byte, offset int) int {
	if prefix, _, found := bytes.CutLast(raw[:offset], []byte("\n")); found {
		return len(prefix) + 1
	}
	return 0
}
func nodeKeys(node *unstable.Node) []string {
	var keys []string
	iterator := node.Key()
	for iterator.Next() {
		keys = append(keys, string(bytes.Clone(iterator.Node().Data)))
	}
	return keys
}

func countTOML(ctx context.Context, node *unstable.Node, depth int, total *int) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	*total++
	if *total > 32768 {
		return ErrCodecLimit
	}
	if node.Kind == unstable.Array || node.Kind == unstable.InlineTable {
		depth++
	}
	if depth > 32 {
		return ErrCodecLimit
	}
	if node.Kind == unstable.KeyValue || node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
		if len(nodeKeys(node)) > 32 {
			return ErrCodecLimit
		}
	}
	iterator := node.Children()
	for iterator.Next() {
		if err := countTOML(ctx, iterator.Node(), depth, total); err != nil {
			return err
		}
	}
	return nil
}

func indexTOML(ctx context.Context, raw []byte) (*tomlDocument, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(raw) > MaxImageBytes {
		return nil, ErrCodecLimit
	}
	document := &tomlDocument{raw: raw, leaves: make(map[string]tomlLeaf), rootAnchor: len(raw), endAnchor: len(raw)}
	if toml.Unmarshal(raw, &document.semantic) != nil {
		return nil, ErrInvalidLocalImage
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	parser := unstable.Parser{KeepComments: true}
	parser.Reset(raw)
	var section []string
	expressions, nodes, commentAnchor := 0, 0, -1
	for parser.NextExpression() {
		expressions++
		if expressions > 4096 {
			return nil, ErrCodecLimit
		}
		node := parser.Expression()
		if err := countTOML(ctx, node, 0, &nodes); err != nil {
			return nil, err
		}
		if node.Kind == unstable.Comment {
			start := lineStart(raw, int(node.Raw.Offset))
			if len(bytes.TrimSpace(raw[start:int(node.Raw.Offset)])) == 0 && commentAnchor < 0 {
				commentAnchor = start
			}
			continue
		}
		if node.Kind == unstable.Table || node.Kind == unstable.ArrayTable {
			anchor := lineStart(raw, int(node.Raw.Offset))
			if commentAnchor >= 0 {
				anchor = commentAnchor
			}
			if len(document.sections) == 0 {
				document.rootAnchor = anchor
			} else {
				document.sections[len(document.sections)-1].end = anchor
			}
			section = nodeKeys(node)
			if len(section) > 32 {
				return nil, ErrCodecLimit
			}
			if node.Kind == unstable.ArrayTable && (providerPath(section) || len(section) == 1 && section[0] == "model_providers") {
				document.closed = true
			}
			document.sections = append(document.sections, tomlSection{path: slices.Clone(section), anchor: anchor, end: len(raw)})
		} else if node.Kind == unstable.KeyValue {
			keys := append(slices.Clone(section), nodeKeys(node)...)
			if len(keys) > 32 {
				return nil, ErrCodecLimit
			}
			value := node.Value()
			document.leaves[pathID(keys)] = tomlLeaf{start: int(value.Raw.Offset), end: int(value.Raw.Offset + value.Raw.Length), kind: value.Kind}
			if len(keys) == 1 && keys[0] == "model_providers" || providerPath(keys) && len(keys) == 2 {
				document.closed = true
			}
			if len(section) == 0 && providerPath(keys) && len(keys) > 2 {
				document.dotted = true
			}
		}
		commentAnchor = -1
	}
	if parser.Error() != nil {
		return nil, ErrInvalidLocalImage
	}
	if commentAnchor >= 0 {
		document.endAnchor = commentAnchor
	}
	if len(document.sections) == 0 {
		document.rootAnchor = document.endAnchor
	} else {
		document.sections[len(document.sections)-1].end = document.endAnchor
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return document, nil
}

func (document *tomlDocument) lookup(parts ...string) (any, bool) {
	var current any = document.semantic
	for _, part := range parts {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		var exists bool
		current, exists = object[part]
		if !exists {
			return nil, false
		}
	}
	return current, true
}

func scalarTOML(value any) ([]byte, error) {
	raw, err := toml.Marshal(map[string]any{"value": value})
	if err != nil {
		return nil, ErrUnsupportedRepresentation
	}
	parser := unstable.Parser{}
	parser.Reset(raw)
	if !parser.NextExpression() || parser.Expression().Kind != unstable.KeyValue {
		return nil, ErrUnsupportedRepresentation
	}
	return bytes.Clone(parser.Raw(parser.Expression().Value().Raw)), nil
}

func applyEdits(ctx context.Context, raw []byte, edits []rawEdit) ([]byte, error) {
	if len(edits) > maxEdits {
		return nil, ErrCodecLimit
	}
	slices.SortStableFunc(edits, func(left, right rawEdit) int { return cmp.Compare(left.start, right.start) })
	result := make([]byte, 0, len(raw))
	offset := 0
	for _, edit := range edits {
		if err := ctx.Err(); err != nil {
			clear(result)
			return nil, err
		}
		if edit.start < offset || edit.end < edit.start || edit.end > len(raw) {
			clear(result)
			return nil, ErrUnsupportedRepresentation
		}
		if len(result)+edit.start-offset+len(edit.value) > MaxImageBytes {
			clear(result)
			return nil, ErrCodecLimit
		}
		result = append(result, raw[offset:edit.start]...)
		result = append(result, edit.value...)
		offset = edit.end
	}
	if len(result)+len(raw)-offset > MaxImageBytes {
		clear(result)
		return nil, ErrCodecLimit
	}
	result = append(result, raw[offset:]...)
	if err := ctx.Err(); err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}

func insertion(raw []byte, offset int, lines []string, eol string) []byte {
	if len(lines) == 0 {
		return nil
	}
	prefix := ""
	if offset > 0 && raw[offset-1] != '\n' {
		prefix = eol
	}
	return []byte(prefix + strings.Join(lines, eol) + eol)
}

func rewriteTOML(ctx context.Context, document *tomlDocument, declaration agentconfig.CodexProviderDeclarationV1, requires bool, secret []byte) ([]byte, int, error) {
	if document.closed {
		return nil, 0, ErrUnsupportedRepresentation
	}
	fields := []struct {
		path  []string
		value any
	}{
		{[]string{"model_provider"}, "cognate"}, {[]string{"model"}, declaration.Model},
	}
	if declaration.ReasoningEffort != nil {
		fields = append(fields, struct {
			path  []string
			value any
		}{[]string{"model_reasoning_effort"}, string(*declaration.ReasoningEffort)})
	}
	if declaration.Catalog != nil {
		fields = append(fields, struct {
			path  []string
			value any
		}{[]string{"model_catalog_json"}, "cognate-model-catalog.json"})
	}
	for _, field := range []struct {
		name  string
		value any
	}{{"name", "Cognate"}, {"base_url", declaration.Endpoint}, {"wire_api", "responses"}, {"requires_openai_auth", requires}, {"experimental_bearer_token", string(secret)}} {
		fields = append(fields, struct {
			path  []string
			value any
		}{[]string{"model_providers", "cognate", field.name}, field.value})
	}
	eol := "\n"
	if index := bytes.IndexByte(document.raw, '\n'); index > 0 && document.raw[index-1] == '\r' {
		eol = "\r\n"
	}
	providerAnchor, providerSection := document.endAnchor, false
	for _, section := range document.sections {
		if slices.Equal(section.path, []string{"model_providers", "cognate"}) {
			providerAnchor, providerSection = section.end, true
		}
	}
	var edits []rawEdit
	var rootLines, providerLines []string
	for _, field := range fields {
		token, err := scalarTOML(field.value)
		if err != nil {
			return nil, 0, err
		}
		if leaf, exists := document.leaves[pathID(field.path)]; exists {
			if leaf.kind != unstable.String && leaf.kind != unstable.Bool {
				return nil, 0, ErrUnsupportedRepresentation
			}
			edits = append(edits, rawEdit{start: leaf.start, end: leaf.end, value: token})
		} else if len(field.path) == 1 {
			rootLines = append(rootLines, field.path[0]+" = "+string(token))
		} else if document.dotted {
			rootLines = append(rootLines, strings.Join(field.path, ".")+" = "+string(token))
		} else {
			providerLines = append(providerLines, field.path[2]+" = "+string(token))
		}
	}
	if len(rootLines) > 0 {
		edits = append(edits, rawEdit{start: document.rootAnchor, end: document.rootAnchor, value: insertion(document.raw, document.rootAnchor, rootLines, eol)})
	}
	if len(providerLines) > 0 {
		if !providerSection {
			providerLines = append([]string{"[model_providers.cognate]"}, providerLines...)
		}
		edits = append(edits, rawEdit{start: providerAnchor, end: providerAnchor, value: insertion(document.raw, providerAnchor, providerLines, eol)})
	}
	result, err := applyEdits(ctx, document.raw, edits)
	if err != nil {
		return nil, 0, err
	}
	checked, err := indexTOML(ctx, result)
	if err != nil {
		clear(result)
		return nil, 0, err
	}
	for _, field := range fields {
		value, exists := checked.lookup(field.path...)
		if !exists || value != field.value {
			clear(result)
			return nil, 0, ErrUnsupportedRepresentation
		}
	}
	return result, len(edits), nil
}
