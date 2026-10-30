package backup

import (
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

const MaxCiphertextBytes int64 = 65 << 20

var (
	ErrInvalid     = errors.New("backup target invalid input")
	ErrBusy        = errors.New("backup target busy")
	ErrClosed      = errors.New("backup target closed")
	ErrDenied      = errors.New("backup target denied")
	ErrUnavailable = errors.New("backup target unavailable")
	ErrUnsupported = errors.New("backup target unsupported")
)

type ObjectKind uint8

const (
	DataExtent ObjectKind = iota + 1
	ManifestPage
	Bootstrap
	Complete
)

type PutResult uint8

const (
	PutUnknown PutResult = iota
	VerifiedNew
	VerifiedExisting
	PutConflict
)

type VerifyResult uint8

const (
	VerifyUnknown VerifyResult = iota
	Verified
	Missing
	VerifyConflict
)

type DeleteResult uint8

const (
	DeleteUnknown DeleteResult = iota
	AbsentVerified
	DeleteConflict
)

type Credentials struct{ read func() credentialData }

func (Credentials) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type credentialData struct{ access, secret, token []byte }
type S3Options struct{ read func() optionData }

func (S3Options) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type optionData struct {
	endpoint, region, bucket, root string
	instance                       [16]byte
	credentials                    Credentials
	prune                          bool
}
type Attempt struct{ read func() attemptData }

func (Attempt) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type attemptData struct{ instance, backup, attempt [16]byte }
type ObjectRef struct{ read func() refData }

func (ObjectRef) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type refData struct {
	attempt attemptData
	kind    ObjectKind
	ordinal uint32
	size    int64
	digest  [32]byte
}
type Cursor struct{ read func() cursorData }

func (Cursor) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type cursorData struct {
	target  *S3Target
	attempt attemptData
	token   string
	page    uint32
}
type DeletePermit struct{ read func() permitData }

func (DeletePermit) MarshalJSON() ([]byte, error) { return []byte("{}"), nil }

type permitData struct {
	target *S3Target
	ref    ObjectRef
	etag   string
}
type ListedObject struct {
	ref  string
	size int64
}

func (object ListedObject) Key() string { return object.ref }
func (object ListedObject) Size() int64 { return object.size }

type Page struct {
	entries []ListedObject
	next    Cursor
}

func (page Page) Objects() []ListedObject { return append([]ListedObject(nil), page.entries...) }
func (page Page) Next() Cursor            { return page.next }
func (cursor Cursor) IsZero() bool        { return cursor.read == nil }

var rootSegment = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
var bucketPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var regionPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)

func NewCredentials(accessID, secret, sessionToken []byte) (Credentials, error) {
	for index, value := range [][]byte{accessID, secret, sessionToken} {
		if len(value) > []int{256, 1024, 4096}[index] || index < 2 && len(value) == 0 {
			return Credentials{}, ErrInvalid
		}
		for _, character := range value {
			if character < 32 || character == 127 {
				return Credentials{}, ErrInvalid
			}
		}
	}
	data := credentialData{bytes.Clone(accessID), bytes.Clone(secret), bytes.Clone(sessionToken)}
	return Credentials{read: func() credentialData { return data }}, nil
}

func NewS3Options(endpoint, region, bucket, root string, instanceID [16]byte, credentials Credentials, prune bool) (S3Options, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || len(endpoint) > 2048 || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Path != "" || parsed.Opaque != "" || strings.ContainsAny(endpoint, "\r\n\\") || parsed.ForceQuery || !bucketPattern.MatchString(bucket) || strings.Contains(bucket, "..") || !regionPattern.MatchString(region) || len(root) == 0 || len(root) > 128 || instanceID == [16]byte{} || credentials.read == nil {
		return S3Options{}, ErrInvalid
	}
	for segment := range strings.SplitSeq(root, "/") {
		if !rootSegment.MatchString(segment) {
			return S3Options{}, ErrInvalid
		}
	}
	data := optionData{endpoint, region, bucket, root, instanceID, credentials, prune}
	return S3Options{read: func() optionData { return data }}, nil
}

func NewAttempt(instanceID, backupID, attemptID [16]byte) (Attempt, error) {
	if instanceID == [16]byte{} || backupID == [16]byte{} || attemptID == [16]byte{} {
		return Attempt{}, ErrInvalid
	}
	data := attemptData{instanceID, backupID, attemptID}
	return Attempt{read: func() attemptData { return data }}, nil
}
func NewObjectRef(attempt Attempt, kind ObjectKind, ordinal uint32, ciphertextBytes int64, ciphertextSHA256 [32]byte) (ObjectRef, error) {
	if attempt.read == nil || kind < DataExtent || kind > Complete || ordinal >= 100000 || kind >= Bootstrap && ordinal != 0 || ciphertextBytes <= 0 || ciphertextBytes > maxKindSize(kind) {
		return ObjectRef{}, ErrInvalid
	}
	data := refData{attempt.read(), kind, ordinal, ciphertextBytes, ciphertextSHA256}
	return ObjectRef{read: func() refData { return data }}, nil
}
func maxKindSize(kind ObjectKind) int64 {
	switch kind {
	case DataExtent:
		return MaxCiphertextBytes
	case ManifestPage:
		return 1 << 20
	default:
		return 16 << 10
	}
}
func attemptPrefix(options optionData, attempt attemptData) string {
	return fmt.Sprintf("%s/%x/backups/%x/%x/", options.root, attempt.instance, attempt.backup, attempt.attempt)
}
func objectKey(options optionData, ref refData) string {
	return fmt.Sprintf("%s%s/%08x", attemptPrefix(options, ref.attempt), []string{"", "data", "page", "bootstrap", "complete"}[ref.kind], ref.ordinal)
}
