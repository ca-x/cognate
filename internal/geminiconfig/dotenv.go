package geminiconfig

import (
	"bytes"
	"context"
	"slices"
	"strings"
	"unicode/utf8"
)

type envEntry struct {
	name                                    string
	value                                   []byte
	start, end, lineStart, lineEnd, comment int
}
type envDocument struct {
	raw     []byte
	entries []envEntry
}
type envUpdate struct {
	name   string
	value  []byte
	remove bool
}

func readEnv(ctx context.Context, raw []byte) (_ *envDocument, returnedError error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if len(raw) > MaxImageBytes {
		return nil, ErrCodecLimit
	}
	if !utf8.Valid(raw) || bytes.ContainsRune(raw, 0) {
		return nil, ErrInvalidLocalImage
	}
	if bytes.Contains(raw, []byte("\u2028")) || bytes.Contains(raw, []byte("\u2029")) {
		return nil, ErrUnsupportedRepresentation
	}
	document := &envDocument{raw: raw}
	defer func() {
		if returnedError != nil {
			document.destroy()
		}
	}()
	seen := make(map[string]bool)
	lines := 0
	for offset := 0; offset < len(raw); {
		if err := checkContext(ctx); err != nil {
			return nil, err
		}
		lines++
		if lines > 4096 {
			return nil, ErrCodecLimit
		}
		end := len(raw)
		if newline := bytes.IndexByte(raw[offset:], '\n'); newline >= 0 {
			end = offset + newline
		}
		next := end
		if next < len(raw) {
			next++
		}
		line := raw[offset:end]
		if len(line) > 0 && line[len(line)-1] == '\r' && end < len(raw) {
			line = line[:len(line)-1]
		}
		if bytes.ContainsRune(line, '\r') {
			return nil, ErrUnsupportedRepresentation
		}
		leading := len(line) - len(bytes.TrimLeft(line, " \t"))
		content := line[leading:]
		if len(content) != 0 && content[0] != '#' {
			entry, err := parseEnvLine(content, offset+leading)
			if err != nil {
				return nil, err
			}
			if entry.end-entry.start >= 2 {
				quote := raw[entry.start]
				if (quote == '\'' || quote == '`' || quote == '"') && raw[entry.end-2] == '\\' && bytes.IndexByte(raw[entry.end:], quote) >= 0 {
					clear(entry.value)
					return nil, ErrUnsupportedRepresentation
				}
			}
			if seen[entry.name] {
				clear(entry.value)
				return nil, ErrUnsupportedRepresentation
			}
			seen[entry.name] = true
			entry.lineStart, entry.lineEnd = offset, next
			document.entries = append(document.entries, entry)
		}
		offset = next
	}
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	return document, nil
}

func validEnvName(name string) bool {
	if len(name) < 1 {
		return false
	}
	for index, character := range []byte(name) {
		letter := character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character == '_'
		if !letter && (index == 0 || character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func parseEnvLine(line []byte, offset int) (envEntry, error) {
	original := line
	if bytes.HasPrefix(line, []byte("export ")) || bytes.HasPrefix(line, []byte("export\t")) {
		line = bytes.TrimLeft(line[6:], " \t")
	}
	name, rest, found := bytes.Cut(line, []byte("="))
	name = bytes.TrimRight(name, " \t")
	if !found || !validEnvName(string(name)) {
		return envEntry{}, ErrUnsupportedRepresentation
	}
	rest = bytes.TrimLeft(rest, " \t")
	trimmed := bytes.Trim(rest, " \t")
	if !bytes.Equal(bytes.TrimSpace(rest), trimmed) || bytes.HasPrefix(trimmed, []byte("\xef\xbb\xbf")) || bytes.HasSuffix(trimmed, []byte("\xef\xbb\xbf")) {
		return envEntry{}, ErrUnsupportedRepresentation
	}
	start := len(original) - len(rest)
	end, comment := len(rest), -1
	var value []byte
	if len(rest) > 0 && bytes.ContainsRune([]byte("'\"`"), rune(rest[0])) {
		quote := rest[0]
		closing := bytes.IndexByte(rest[1:], quote)
		if closing < 0 {
			return envEntry{}, ErrUnsupportedRepresentation
		}
		end = closing + 2
		suffix := bytes.TrimLeft(rest[end:], " \t")
		if len(suffix) > 0 && suffix[0] != '#' {
			return envEntry{}, ErrUnsupportedRepresentation
		}
		if len(suffix) > 0 {
			comment = offset + start + len(rest) - len(suffix)
		}
		value = bytes.Clone(rest[1 : end-1])
		if quote == '"' {
			next := bytes.ReplaceAll(value, []byte(`\n`), []byte{'\n'})
			clear(value)
			value = next
			next = bytes.ReplaceAll(value, []byte(`\r`), []byte{'\r'})
			clear(value)
			value = next
		}
	} else {
		if index := bytes.IndexByte(rest, '#'); index >= 0 {
			end = index
			comment = offset + start + index
		}
		end = len(bytes.TrimRight(rest[:end], " \t"))
		value = bytes.Clone(rest[:end])
	}
	return envEntry{name: strings.Clone(string(name)), value: value, start: offset + start, end: offset + start + end, comment: comment}, nil
}
func (document *envDocument) destroy() {
	for _, entry := range document.entries {
		clear(entry.value)
	}
	document.entries = nil
}
func (document *envDocument) find(name string) *envEntry {
	for index := range document.entries {
		if document.entries[index].name == name {
			return &document.entries[index]
		}
	}
	return nil
}
func encodeEnv(value []byte) ([]byte, error) {
	if bytes.Contains(value, []byte("\u2028")) || bytes.Contains(value, []byte("\u2029")) {
		return nil, ErrUnsupportedRepresentation
	}
	if !utf8.Valid(value) || bytes.ContainsAny(value, "\x00\r\n") {
		return nil, ErrUnsupportedRepresentation
	}
	trailingBackslash := bytes.HasSuffix(value, []byte{'\\'})
	for _, quote := range []byte{'\'', '`', '"'} {
		if trailingBackslash || bytes.ContainsRune(value, rune(quote)) || quote == '"' && (bytes.Contains(value, []byte(`\n`)) || bytes.Contains(value, []byte(`\r`))) {
			continue
		}
		encoded := append([]byte{quote}, value...)
		return append(encoded, quote), nil
	}
	if len(value) > 0 && !bytes.ContainsRune(value, '#') && bytes.Equal(bytes.TrimSpace(value), value) && !bytes.ContainsRune([]byte("'\"`"), rune(value[0])) {
		return bytes.Clone(value), nil
	}
	return nil, ErrUnsupportedRepresentation
}

type rawEdit struct {
	start, end int
	value      []byte
}

func applyEdits(ctx context.Context, raw []byte, edits []rawEdit) ([]byte, error) {
	if err := checkContext(ctx); err != nil {
		return nil, err
	}
	if len(raw) > MaxImageBytes || len(edits) > 16 {
		return nil, ErrCodecLimit
	}
	slices.SortFunc(edits, func(first, second rawEdit) int { return first.start - second.start })
	total, previous := len(raw), 0
	for _, edit := range edits {
		if edit.start < previous || edit.end < edit.start || edit.end > len(raw) {
			return nil, ErrInvalidLocalImage
		}
		total += len(edit.value) - (edit.end - edit.start)
		if total > MaxImageBytes {
			return nil, ErrCodecLimit
		}
		previous = edit.end
	}
	result := make([]byte, 0, total)
	previous = 0
	for _, edit := range edits {
		if err := checkContext(ctx); err != nil {
			clear(result)
			return nil, err
		}
		result = append(result, raw[previous:edit.start]...)
		result = append(result, edit.value...)
		previous = edit.end
	}
	result = append(result, raw[previous:]...)
	if err := checkContext(ctx); err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}

func rewriteEnv(ctx context.Context, document *envDocument, updates []envUpdate) ([]byte, error) {
	if document == nil {
		return nil, ErrInvalidLocalImage
	}
	var edits []rawEdit
	var added []byte
	defer func() {
		clear(added)
		for _, edit := range edits {
			clear(edit.value)
		}
	}()
	seen := make(map[string]bool)
	newline := []byte{'\n'}
	if bytes.Contains(document.raw, []byte("\r\n")) {
		newline = []byte{'\r', '\n'}
	}
	for _, update := range updates {
		if !validEnvName(update.name) || seen[update.name] {
			return nil, ErrInvalidLocalImage
		}
		seen[update.name] = true
		entry := document.find(update.name)
		if update.remove {
			if entry != nil {
				end := entry.lineEnd
				if entry.comment >= 0 {
					end = entry.comment
				}
				edits = append(edits, rawEdit{start: entry.lineStart, end: end})
			}
			continue
		}
		encoded, err := encodeEnv(update.value)
		if err != nil {
			return nil, err
		}
		if entry != nil {
			edits = append(edits, rawEdit{start: entry.start, end: entry.end, value: encoded})
		} else {
			if len(added) > 0 || len(document.raw) > 0 && document.raw[len(document.raw)-1] != '\n' {
				added = append(added, newline...)
			}
			added = append(added, update.name...)
			added = append(added, '=')
			added = append(added, encoded...)
			clear(encoded)
		}
	}
	if len(added) > 0 {
		if len(document.raw) == 0 || document.raw[len(document.raw)-1] == '\n' {
			added = append(added, newline...)
		}
		edits = append(edits, rawEdit{start: len(document.raw), end: len(document.raw), value: bytes.Clone(added)})
	}
	result, err := applyEdits(ctx, document.raw, edits)
	if err != nil {
		return nil, err
	}
	parsed, err := readEnv(ctx, result)
	if err != nil {
		clear(result)
		return nil, err
	}
	defer parsed.destroy()
	for _, update := range updates {
		entry := parsed.find(update.name)
		if update.remove && entry != nil || !update.remove && (entry == nil || !bytes.Equal(entry.value, update.value)) {
			clear(result)
			return nil, ErrUnsupportedRepresentation
		}
	}
	if err := checkContext(ctx); err != nil {
		clear(result)
		return nil, err
	}
	return result, nil
}
