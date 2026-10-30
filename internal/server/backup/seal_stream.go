package backup

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/binary"
	"hash"
	"io"

	sio "github.com/secure-io/sio-go"
)

const sealFrame = 1 << 20
const sealExtentLimit = 64 << 20
const sealHeaderSize = 96
const sealTrailerSize = 80

type sealHeader struct {
	purpose byte
	attempt attemptData
	ordinal uint32
	length  int64
	nonce   [8]byte
}

func (header sealHeader) encode() (encoded [96]byte) {
	copy(encoded[:8], "CGBKSE01")
	binary.BigEndian.PutUint16(encoded[8:10], 1)
	binary.BigEndian.PutUint16(encoded[10:12], 1)
	encoded[12] = header.purpose
	copy(encoded[16:32], header.attempt.instance[:])
	copy(encoded[32:48], header.attempt.backup[:])
	copy(encoded[48:64], header.attempt.attempt[:])
	binary.BigEndian.PutUint32(encoded[64:68], header.ordinal)
	binary.BigEndian.PutUint32(encoded[68:72], sealFrame)
	binary.BigEndian.PutUint64(encoded[72:80], uint64(header.length))
	copy(encoded[80:88], header.nonce[:])
	return encoded
}

func sealCipherSize(length int64) (int64, error) {
	if length <= 0 || length > sealExtentLimit {
		return 0, ErrInvalid
	}
	return 96 + length + 80 + 16*((length+80+sealFrame-1)/sealFrame), nil
}

func parseSealHeader(encoded []byte) (header sealHeader, err error) {
	if len(encoded) != 96 || string(encoded[:8]) != "CGBKSE01" || binary.BigEndian.Uint16(encoded[8:10]) != 1 || binary.BigEndian.Uint16(encoded[10:12]) != 1 || encoded[12] < 1 || encoded[12] > 4 || !bytes.Equal(encoded[13:16], make([]byte, 3)) || !bytes.Equal(encoded[88:96], make([]byte, 8)) || binary.BigEndian.Uint32(encoded[68:72]) != sealFrame {
		return header, ErrInvalid
	}
	header.purpose = encoded[12]
	copy(header.attempt.instance[:], encoded[16:32])
	copy(header.attempt.backup[:], encoded[32:48])
	copy(header.attempt.attempt[:], encoded[48:64])
	header.ordinal = binary.BigEndian.Uint32(encoded[64:68])
	header.length = int64(binary.BigEndian.Uint64(encoded[72:80]))
	copy(header.nonce[:], encoded[80:88])
	maximum := int64(sealExtentLimit)
	if header.purpose == 2 || header.purpose == 3 {
		maximum = sealFrame - 192
	}
	if header.purpose == 4 {
		maximum = 16384 - 192
	}
	if header.attempt.instance == [16]byte{} || header.attempt.backup == [16]byte{} || header.attempt.attempt == [16]byte{} || header.ordinal >= 100000 || header.purpose >= 3 && header.ordinal != 0 || header.purpose == 2 && header.ordinal == 0 || header.length <= 0 || header.length > maximum {
		return sealHeader{}, ErrInvalid
	}
	return header, nil
}

func sealDerive(material []byte, attempt [16]byte, label string, header []byte) ([]byte, error) {
	derived, err := hkdf.Key(sha256.New, material, attempt[:], label+string(header), 32)
	if err != nil {
		return nil, ErrInvalid
	}
	return derived, nil
}

func objectStream(header sealHeader, secret []byte) (*sio.Stream, []byte, error) {
	encoded := header.encode()
	key, err := sealDerive(secret, header.attempt.attempt, "cognate/f4/object-key/v1\x00", encoded[:])
	if err != nil {
		return nil, nil, err
	}
	defer clear(key)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, ErrInvalid
	}
	return sio.NewStream(aead, sealFrame), append([]byte("cognate/f4/object-aad/v1\x00"), encoded[:]...), nil
}

func sealTrailer(length int64, purpose byte, digest []byte) (trailer [80]byte) {
	copy(trailer[:8], "CGBKEND1")
	binary.BigEndian.PutUint64(trailer[8:16], uint64(length))
	binary.BigEndian.PutUint32(trailer[16:20], uint32((length+sealFrame-1)/sealFrame))
	binary.BigEndian.PutUint32(trailer[20:24], uint32((length+80+sealFrame-1)/sealFrame))
	copy(trailer[24:56], digest)
	trailer[56] = purpose
	return trailer
}

type sealPayloadReader struct {
	ctx     context.Context
	input   io.Reader
	header  sealHeader
	digest  hash.Hash
	count   int64
	trailer []byte
}

func (reader *sealPayloadReader) Read(output []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	if reader.trailer != nil {
		if len(reader.trailer) == 0 {
			return 0, io.EOF
		}
		count := copy(output, reader.trailer)
		reader.trailer = reader.trailer[count:]
		return count, nil
	}
	remaining := reader.header.length - reader.count
	if remaining == 0 {
		var probe [1]byte
		count, err := reader.input.Read(probe[:])
		if count != 0 || err != io.EOF {
			return 0, ErrInvalid
		}
		trailer := sealTrailer(reader.count, reader.header.purpose, reader.digest.Sum(nil))
		reader.trailer = trailer[:]
		return reader.Read(output)
	}
	count, err := reader.input.Read(output[:min(int64(len(output)), remaining)])
	if count > 0 {
		reader.count += int64(count)
		_, _ = reader.digest.Write(output[:count])
	}
	if err != nil && (err != io.EOF || reader.count != reader.header.length) {
		return count, ErrInvalid
	}
	if count == 0 && err == nil {
		return 0, io.ErrNoProgress
	}
	return count, nil
}

func sealEncrypt(ctx context.Context, header sealHeader, secret []byte, input io.Reader, output io.Writer) ([32]byte, error) {
	var digest [32]byte
	stream, aad, err := objectStream(header, secret)
	if err != nil {
		return digest, err
	}
	payload := &sealPayloadReader{ctx: ctx, input: input, header: header, digest: sha256.New()}
	encoded := header.encode()
	if err = sealWrite(ctx, output, encoded[:]); err != nil {
		return digest, err
	}
	reader := stream.EncryptReader(payload, header.nonce[:], aad)
	buffer := make([]byte, 32768)
	for {
		count, readError := reader.Read(buffer)
		if count > 0 {
			if err = sealWrite(ctx, output, buffer[:count]); err != nil {
				return digest, err
			}
		}
		if readError == io.EOF {
			break
		}
		if readError != nil {
			return digest, ErrInvalid
		}
		if count == 0 {
			return digest, ErrInvalid
		}
	}
	if err = ctx.Err(); err != nil {
		return digest, err
	}
	copy(digest[:], payload.digest.Sum(nil))
	return digest, nil
}

func sealDecrypt(ctx context.Context, header sealHeader, secret []byte, input io.Reader, output io.Writer) ([32]byte, error) {
	var result [32]byte
	stream, aad, err := objectStream(header, secret)
	if err != nil {
		return result, err
	}
	reader := stream.DecryptReader(input, header.nonce[:], aad)
	buffer := make([]byte, 32768+80)
	held := 0
	var emitted int64
	digest := sha256.New()
	for {
		if err = ctx.Err(); err != nil {
			return result, err
		}
		count, readError := reader.Read(buffer[held:])
		total := held + count
		if total > 80 {
			amount := total - 80
			if emitted+int64(amount) > header.length {
				return result, ErrInvalid
			}
			if err = sealWrite(ctx, output, buffer[:amount]); err != nil {
				return result, err
			}
			_, _ = digest.Write(buffer[:amount])
			emitted += int64(amount)
			copy(buffer[:80], buffer[amount:total])
			held = 80
		} else {
			held = total
		}
		if readError == io.EOF {
			break
		}
		if readError != nil || count == 0 {
			return result, ErrInvalid
		}
	}
	expected := sealTrailer(emitted, header.purpose, digest.Sum(nil))
	if emitted != header.length || held != 80 || !bytes.Equal(buffer[:80], expected[:]) {
		return result, ErrInvalid
	}
	if err = ctx.Err(); err != nil {
		return result, err
	}
	copy(result[:], digest.Sum(nil))
	return result, nil
}

func sealWrite(ctx context.Context, writer io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	count, err := writer.Write(data)
	if err != nil {
		return sealError(ctx, err)
	}
	if count != len(data) {
		return ErrInvalid
	}
	return ctx.Err()
}
