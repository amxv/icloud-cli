package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/amxv/icloud-cli/internal/blob"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type stagedOutboundFiles struct {
	values []*blob.Staged
}

func (set *stagedOutboundFiles) Abort() {
	if set == nil {
		return
	}
	for _, value := range set.values {
		_ = value.Abort()
	}
}

func inspectOutboundAttachments(ctx context.Context, values []maildomain.OutboundAttachment) ([]maildomain.OutboundAttachment, error) {
	return prepareOutboundAttachments(ctx, nil, values)
}

func stageOutboundAttachments(ctx context.Context, stager attachmentBlobStore, values []maildomain.OutboundAttachment) ([]maildomain.OutboundAttachment, *stagedOutboundFiles, error) {
	if len(values) > 0 && stager == nil {
		return nil, nil, errors.New("attachment blob store is unavailable")
	}
	prepared, staged, err := prepareOutboundAttachmentsWithStager(ctx, stager, values)
	return prepared, staged, err
}

func prepareOutboundAttachments(ctx context.Context, _ attachmentBlobStore, values []maildomain.OutboundAttachment) ([]maildomain.OutboundAttachment, error) {
	prepared, _, err := prepareOutboundAttachmentsWithStager(ctx, nil, values)
	return prepared, err
}

func prepareOutboundAttachmentsWithStager(ctx context.Context, stager attachmentBlobStore, values []maildomain.OutboundAttachment) ([]maildomain.OutboundAttachment, *stagedOutboundFiles, error) {
	if len(values) > maildomain.MaxSendAttachments {
		return nil, nil, fmt.Errorf("attachment count exceeds %d", maildomain.MaxSendAttachments)
	}
	result := make([]maildomain.OutboundAttachment, 0, len(values))
	staged := &stagedOutboundFiles{}
	var total int64
	for _, requested := range values {
		if err := ctx.Err(); err != nil {
			staged.Abort()
			return nil, nil, err
		}
		path := strings.TrimSpace(requested.SourcePath)
		if path == "" {
			prepared, value, err := prepareVerifiedOutboundAttachment(ctx, stager, requested)
			if err != nil {
				staged.Abort()
				return nil, nil, err
			}
			total += prepared.ByteSize
			if total > maildomain.MaxSendAttachmentTotalBytes {
				if value != nil {
					_ = value.Abort()
				}
				staged.Abort()
				return nil, nil, fmt.Errorf("attachment total exceeds %d bytes", maildomain.MaxSendAttachmentTotalBytes)
			}
			if value != nil {
				staged.values = append(staged.values, value)
			}
			result = append(result, prepared)
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			staged.Abort()
			return nil, nil, fmt.Errorf("open attachment: %w", err)
		}
		info, err := file.Stat()
		if err != nil {
			_ = file.Close()
			staged.Abort()
			return nil, nil, fmt.Errorf("inspect attachment: %w", err)
		}
		if !info.Mode().IsRegular() {
			_ = file.Close()
			staged.Abort()
			return nil, nil, errors.New("attachment path must be a regular file")
		}
		if info.Size() < 0 || info.Size() > maildomain.MaxSendAttachmentBytes {
			_ = file.Close()
			staged.Abort()
			return nil, nil, fmt.Errorf("attachment exceeds %d bytes", maildomain.MaxSendAttachmentBytes)
		}
		total += info.Size()
		if total > maildomain.MaxSendAttachmentTotalBytes {
			_ = file.Close()
			staged.Abort()
			return nil, nil, fmt.Errorf("attachment total exceeds %d bytes", maildomain.MaxSendAttachmentTotalBytes)
		}

		filename := strings.TrimSpace(requested.Filename)
		if filename == "" {
			filename = filepath.Base(path)
		}
		mediaType, err := outboundAttachmentMediaType(file, filename, requested.MediaType)
		if err != nil {
			_ = file.Close()
			staged.Abort()
			return nil, nil, err
		}

		prepared := maildomain.OutboundAttachment{
			Filename: filename, MediaType: mediaType, ByteSize: info.Size(), SourcePath: path,
		}
		if stager == nil {
			hash, size, err := hashOutboundAttachment(ctx, file)
			closeErr := file.Close()
			if err == nil {
				err = closeErr
			}
			if err != nil {
				staged.Abort()
				return nil, nil, fmt.Errorf("read attachment: %w", err)
			}
			if size != info.Size() {
				staged.Abort()
				return nil, nil, errors.New("attachment changed while it was being reviewed")
			}
			prepared.ContentHash = hash
			prepared.Open = func(sourcePath string) func() (io.ReadCloser, error) {
				return func() (io.ReadCloser, error) { return os.Open(sourcePath) }
			}(path)
		} else {
			value, stageErr := stager.StageAttachment(ctx, file, info.Size())
			closeErr := file.Close()
			if stageErr == nil {
				stageErr = closeErr
			}
			if stageErr != nil {
				if value != nil {
					_ = value.Abort()
				}
				staged.Abort()
				return nil, nil, fmt.Errorf("stage attachment: %w", stageErr)
			}
			if requested.ContentHash != "" && (!strings.EqualFold(requested.ContentHash, value.ContentHash) || requested.ByteSize != value.ByteSize) {
				_ = value.Abort()
				staged.Abort()
				return nil, nil, errors.New("attachment changed after review; review it again before sending")
			}
			prepared.ContentHash = value.ContentHash
			prepared.ByteSize = value.ByteSize
			prepared.Open = value.Open
			staged.values = append(staged.values, value)
		}
		result = append(result, prepared)
	}
	return result, staged, nil
}

func prepareVerifiedOutboundAttachment(
	ctx context.Context,
	stager attachmentBlobStore,
	requested maildomain.OutboundAttachment,
) (maildomain.OutboundAttachment, *blob.Staged, error) {
	if requested.Open == nil {
		return maildomain.OutboundAttachment{}, nil, errors.New("attachment path or verified content source is required")
	}
	filename := strings.TrimSpace(requested.Filename)
	mediaType, _, mediaErr := mime.ParseMediaType(strings.TrimSpace(requested.MediaType))
	if filename == "" || mediaErr != nil || !strings.Contains(mediaType, "/") {
		return maildomain.OutboundAttachment{}, nil, errors.New("verified attachment metadata is incomplete")
	}
	if requested.ByteSize < 0 || requested.ByteSize > maildomain.MaxSendAttachmentBytes || len(strings.TrimSpace(requested.ContentHash)) != sha256.Size*2 {
		return maildomain.OutboundAttachment{}, nil, errors.New("verified attachment size or hash is invalid")
	}
	source, err := requested.Open()
	if err != nil {
		return maildomain.OutboundAttachment{}, nil, fmt.Errorf("open verified attachment: %w", err)
	}
	prepared := requested
	prepared.Filename = filename
	prepared.MediaType = strings.ToLower(mediaType)
	prepared.ContentHash = strings.ToLower(strings.TrimSpace(requested.ContentHash))
	prepared.SourcePath = ""
	if stager == nil {
		hash, size, readErr := hashOutboundAttachment(ctx, source)
		closeErr := source.Close()
		if readErr == nil {
			readErr = closeErr
		}
		if readErr != nil {
			return maildomain.OutboundAttachment{}, nil, fmt.Errorf("read verified attachment: %w", readErr)
		}
		if size != requested.ByteSize || !strings.EqualFold(hash, requested.ContentHash) {
			return maildomain.OutboundAttachment{}, nil, errors.New("verified attachment content changed")
		}
		return prepared, nil, nil
	}
	value, stageErr := stager.StageAttachment(ctx, source, requested.ByteSize)
	closeErr := source.Close()
	if stageErr == nil {
		stageErr = closeErr
	}
	if stageErr != nil {
		if value != nil {
			_ = value.Abort()
		}
		return maildomain.OutboundAttachment{}, nil, fmt.Errorf("stage verified attachment: %w", stageErr)
	}
	if value.ByteSize != requested.ByteSize || !strings.EqualFold(value.ContentHash, requested.ContentHash) {
		_ = value.Abort()
		return maildomain.OutboundAttachment{}, nil, errors.New("verified attachment content changed after review")
	}
	prepared.ContentHash = value.ContentHash
	prepared.ByteSize = value.ByteSize
	prepared.Open = value.Open
	return prepared, value, nil
}

func outboundAttachmentMediaType(file *os.File, filename, override string) (string, error) {
	if value := strings.TrimSpace(override); value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err != nil || !strings.Contains(mediaType, "/") {
			return "", errors.New("attachment media-type override is invalid")
		}
		return strings.ToLower(mediaType), nil
	}
	if value := mime.TypeByExtension(strings.ToLower(filepath.Ext(filename))); value != "" {
		mediaType, _, err := mime.ParseMediaType(value)
		if err == nil && strings.Contains(mediaType, "/") {
			return strings.ToLower(mediaType), nil
		}
	}
	buffer := make([]byte, 512)
	count, err := file.Read(buffer)
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("detect attachment media type: %w", err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind attachment: %w", err)
	}
	return strings.ToLower(http.DetectContentType(buffer[:count])), nil
}

func hashOutboundAttachment(ctx context.Context, reader io.Reader) (string, int64, error) {
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(&outboundContextReader{ctx: ctx, reader: reader}, maildomain.MaxSendAttachmentBytes+1))
	if err != nil {
		return "", written, err
	}
	if written > maildomain.MaxSendAttachmentBytes {
		return "", written, blob.ErrTooLarge
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

type outboundContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *outboundContextReader) Read(buffer []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(buffer)
}
