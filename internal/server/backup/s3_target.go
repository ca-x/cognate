package backup

import (
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	aferos3 "github.com/fclairamb/afero-s3"
)

type S3Target struct {
	options     S3Options
	roots       *x509.CertPool
	mutex       sync.Mutex
	active      map[*operation]struct{}
	closed      bool
	closeFailed bool
	done        chan struct{}
}

func NewS3Target(options S3Options) (*S3Target, error) {
	if options.read == nil {
		return nil, ErrInvalid
	}
	data := options.read()
	private := data.credentials.read()
	cloned, err := NewCredentials(private.access, private.secret, private.token)
	if err != nil {
		return nil, ErrInvalid
	}
	data.credentials = cloned
	return &S3Target{options: S3Options{read: func() optionData { return data }}, active: make(map[*operation]struct{}), done: make(chan struct{})}, nil
}
func (operation *operation) bind(ref ObjectRef) (refData, error) {
	if ref.read == nil {
		return refData{}, ErrInvalid
	}
	data := ref.read()
	options := operation.target.options.read()
	if data.attempt.instance != options.instance {
		return refData{}, ErrInvalid
	}
	operation.key = objectKey(options, data)
	operation.size = data.size
	return data, nil
}

type contextSection struct {
	*io.SectionReader
	ctx context.Context
}

func (reader contextSection) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.SectionReader.Read(buffer)
}
func hashStream(ctx context.Context, reader io.Reader) ([32]byte, int64, error) {
	hash := sha256.New()
	var buffer [32 << 10]byte
	var total int64
	for {
		if err := ctx.Err(); err != nil {
			return [32]byte{}, total, err
		}
		count, err := reader.Read(buffer[:])
		if count > 0 {
			hash.Write(buffer[:count])
			total += int64(count)
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return [32]byte(hash.Sum(nil)), total, nil
			}
			return [32]byte{}, total, err
		}
		if count == 0 {
			return [32]byte{}, total, io.ErrNoProgress
		}
	}
}
func (target *S3Target) PutSealed(ctx context.Context, ref ObjectRef, source *os.File) (PutResult, error) {
	operation, err := target.begin(ctx, "put", 4)
	if err != nil {
		return PutUnknown, err
	}
	defer operation.end()
	data, err := operation.bind(ref)
	if err != nil {
		return PutUnknown, err
	}
	if source == nil {
		return PutUnknown, ErrInvalid
	}
	info, err := source.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != data.size {
		return PutUnknown, ErrInvalid
	}
	digest, size, err := hashStream(operation.ctx, io.NewSectionReader(source, 0, data.size))
	if err != nil {
		return PutUnknown, publicError(operation.ctx, err)
	}
	if size != data.size || digest != data.digest {
		return PutUnknown, ErrInvalid
	}
	options := target.options.read()
	_, err = operation.client("PutObject").PutObject(operation.ctx, &s3.PutObjectInput{Bucket: new(options.bucket), Key: new(operation.key), ContentLength: new(data.size), ChecksumSHA256: new(base64.StdEncoding.EncodeToString(data.digest[:])), IfNoneMatch: new("*"), Body: contextSection{io.NewSectionReader(source, 0, data.size), operation.ctx}})
	existing := false
	if err != nil {
		if api, ok := errors.AsType[smithy.APIError](err); ok && api.ErrorCode() == "PreconditionFailed" {
			existing = true
		} else {
			return PutUnknown, safeServiceError(operation.ctx, err)
		}
	}
	result, _, err := operation.verify(ref)
	if result == VerifyConflict && existing {
		return PutConflict, err
	}
	if result != Verified || err != nil || !operation.healthy() {
		if err == nil {
			err = ErrUnavailable
		}
		return PutUnknown, publicError(operation.ctx, err)
	}
	if existing {
		return VerifiedExisting, nil
	}
	return VerifiedNew, nil
}
func safeServiceError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if api, ok := errors.AsType[smithy.APIError](err); ok && (api.ErrorCode() == "AccessDenied" || api.ErrorCode() == "InvalidAccessKeyId" || api.ErrorCode() == "SignatureDoesNotMatch") {
		return ErrDenied
	}
	return ErrUnavailable
}
func authoritativeMissing(err error) bool {
	_, typed := errors.AsType[*types.NoSuchKey](err)
	response, ok := errors.AsType[*smithyhttp.ResponseError](err)
	return typed && ok && response.HTTPStatusCode() == 404
}
func (operation *operation) probe() (*s3.GetObjectOutput, VerifyResult, error) {
	options := operation.target.options.read()
	output, err := operation.client("GetObject").GetObject(operation.ctx, &s3.GetObjectInput{Bucket: new(options.bucket), Key: new(operation.key), Range: new("bytes=0-0")})
	if err != nil {
		if authoritativeMissing(err) && operation.healthy() {
			return nil, Missing, nil
		}
		return nil, VerifyUnknown, safeServiceError(operation.ctx, err)
	}
	var buffer [2]byte
	count, readError := io.ReadFull(output.Body, buffer[:])
	closeError := output.Body.Close()
	if count != 1 || !errors.Is(readError, io.ErrUnexpectedEOF) || closeError != nil || output.ContentLength == nil || *output.ContentLength != 1 || output.ETag == nil || len(*output.ETag) == 0 || len(*output.ETag) > 1024 || strings.ContainsAny(*output.ETag, "\r\n") || !operation.healthy() {
		return nil, VerifyUnknown, ErrUnavailable
	}
	return output, Verified, nil
}
func (operation *operation) verify(ref ObjectRef) (VerifyResult, string, error) {
	data := ref.read()
	probe, result, err := operation.probe()
	if result != Verified {
		return result, "", err
	}
	options := operation.target.options.read()
	adapter := aferos3.NewFsFromClient(options.bucket, operation.client("HeadObject", "GetObject"))
	file, err := adapter.Open(operation.key)
	if err != nil {
		return VerifyUnknown, "", safeServiceError(operation.ctx, err)
	}
	digest, size, readError := hashStream(operation.ctx, file)
	closeError := file.Close()
	if readError != nil || closeError != nil || !operation.healthy() {
		return VerifyUnknown, "", publicError(operation.ctx, ErrUnavailable)
	}
	if size != data.size || digest != data.digest {
		return VerifyConflict, "", nil
	}
	return Verified, *probe.ETag, nil
}
func (target *S3Target) VerifyExact(ctx context.Context, ref ObjectRef) (VerifyResult, error) {
	operation, err := target.begin(ctx, "verify", 3)
	if err != nil {
		return VerifyUnknown, err
	}
	defer operation.end()
	if _, err = operation.bind(ref); err != nil {
		return VerifyUnknown, err
	}
	result, _, err := operation.verify(ref)
	return result, err
}
func (target *S3Target) InspectForDelete(ctx context.Context, ref ObjectRef) (DeletePermit, VerifyResult, error) {
	operation, err := target.begin(ctx, "inspect", 3)
	if err != nil {
		return DeletePermit{}, VerifyUnknown, err
	}
	defer operation.end()
	if _, err = operation.bind(ref); err != nil {
		return DeletePermit{}, VerifyUnknown, err
	}
	result, etag, err := operation.verify(ref)
	if result != Verified || err != nil {
		return DeletePermit{}, result, err
	}
	data := permitData{target, ref, etag}
	return DeletePermit{read: func() permitData { return data }}, Verified, nil
}
func (target *S3Target) DeleteExact(ctx context.Context, permit DeletePermit) (DeleteResult, error) {
	operation, err := target.begin(ctx, "delete", 3)
	if err != nil {
		return DeleteUnknown, err
	}
	defer operation.end()
	if permit.read == nil || permit.read().target != target || !target.options.read().prune {
		return DeleteUnknown, ErrInvalid
	}
	data := permit.read()
	if _, err := operation.bind(data.ref); err != nil {
		return DeleteUnknown, err
	}
	options := target.options.read()
	_, err = operation.client("DeleteObject").DeleteObject(operation.ctx, &s3.DeleteObjectInput{Bucket: new(options.bucket), Key: new(operation.key), IfMatch: new(data.etag)})
	if err != nil {
		if api, ok := errors.AsType[smithy.APIError](err); ok && api.ErrorCode() == "PreconditionFailed" {
			return DeleteConflict, nil
		}
		return DeleteUnknown, safeServiceError(operation.ctx, err)
	}
	_, result, err := operation.probe()
	if result == Missing && operation.healthy() {
		return AbsentVerified, nil
	}
	return DeleteUnknown, publicError(operation.ctx, err)
}
func (target *S3Target) ListAttemptPage(ctx context.Context, attempt Attempt, cursor Cursor) (Page, error) {
	operation, err := target.begin(ctx, "list", 1)
	if err != nil {
		return Page{}, err
	}
	defer operation.end()
	options := target.options.read()
	if attempt.read == nil || attempt.read().instance != options.instance {
		return Page{}, ErrInvalid
	}
	data := attempt.read()
	operation.prefix = attemptPrefix(options, data)
	token := ""
	page := uint32(1)
	if cursor.read != nil {
		previous := cursor.read()
		if previous.target != target || previous.attempt != data || previous.page >= 2001 {
			return Page{}, ErrInvalid
		}
		token = previous.token
		page = previous.page + 1
	}
	input := &s3.ListObjectsV2Input{Bucket: new(options.bucket), Prefix: new(operation.prefix), MaxKeys: new(int32(50))}
	if token != "" {
		input.ContinuationToken = new(token)
	}
	output, err := operation.client("ListObjectsV2").ListObjectsV2(operation.ctx, input)
	if err != nil {
		return Page{}, safeServiceError(operation.ctx, err)
	}
	if output.IsTruncated == nil || output.KeyCount == nil || *output.KeyCount != int32(len(output.Contents)) || len(output.Contents) > 50 || !operation.healthy() {
		return Page{}, ErrUnavailable
	}
	result := Page{}
	seen := make(map[string]bool)
	for _, object := range output.Contents {
		if object.Key == nil || object.Size == nil || object.LastModified == nil || object.ETag == nil || len(*object.ETag) == 0 || len(*object.ETag) > 1024 || strings.ContainsAny(*object.ETag, "\r\n") || !validListedKey(operation.prefix, *object.Key, *object.Size) || seen[*object.Key] {
			return Page{}, ErrUnavailable
		}
		seen[*object.Key] = true
		result.entries = append(result.entries, ListedObject{*object.Key, *object.Size})
	}
	if *output.IsTruncated {
		if output.NextContinuationToken == nil || len(*output.NextContinuationToken) == 0 || len(*output.NextContinuationToken) > 4096 || *output.NextContinuationToken == token || page >= 2001 {
			return Page{}, ErrUnavailable
		}
		next := cursorData{target, data, *output.NextContinuationToken, page}
		result.next = Cursor{read: func() cursorData { return next }}
	} else if output.NextContinuationToken != nil && *output.NextContinuationToken != "" {
		return Page{}, ErrUnavailable
	}
	return result, nil
}
func validListedKey(prefix, key string, size int64) bool {
	if len(key) > 384 || !strings.HasPrefix(key, prefix) {
		return false
	}
	kindText, ordinalText, ok := strings.Cut(strings.TrimPrefix(key, prefix), "/")
	if !ok || len(ordinalText) != 8 {
		return false
	}
	ordinal, err := strconv.ParseUint(ordinalText, 16, 32)
	if err != nil || ordinal >= 100000 || strings.ToLower(ordinalText) != ordinalText {
		return false
	}
	var kind ObjectKind
	switch kindText {
	case "data":
		kind = DataExtent
	case "page":
		kind = ManifestPage
	case "bootstrap":
		kind = Bootstrap
	case "complete":
		kind = Complete
	default:
		return false
	}
	return (kind < Bootstrap || ordinal == 0) && size > 0 && size <= maxKindSize(kind)
}
