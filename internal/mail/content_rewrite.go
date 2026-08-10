package mail

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	message "github.com/emersion/go-message"
	"github.com/emersion/go-message/charset"
)

// RewriteRawWithoutParts reconstructs one RFC822 message while omitting exact
// canonical MIME part paths. It preserves semantic message headers and decoded
// leaf content, but deliberately regenerates MIME boundaries and transfer
// encodings. The caller owns review/provider replacement semantics.
func RewriteRawWithoutParts(ctx context.Context, reader io.Reader, removedPaths map[string]struct{}, writer io.Writer) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if reader == nil || writer == nil || len(removedPaths) == 0 {
		return errors.New("raw reader, writer, and at least one removed MIME path are required")
	}
	entity, err := message.ReadWithOptions(&contextReader{ctx: ctx, reader: reader}, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
	if entity == nil {
		return fmt.Errorf("read rewrite source: %w", err)
	}
	if _, removeRoot := removedPaths["0"]; removeRoot {
		header := rewriteMIMEHeader(entity.Header, true, "text/plain")
		header.SetContentType("text/plain", map[string]string{"charset": "utf-8"})
		header.Set("Content-Transfer-Encoding", "quoted-printable")
		header.Del("Content-Disposition")
		header.Del("Content-ID")
		root, createErr := message.CreateWriter(writer, header)
		if createErr != nil {
			return createErr
		}
		return root.Close()
	}
	return rewriteRootEntity(ctx, entity, "0", removedPaths, writer, true)
}

func rewriteRootEntity(ctx context.Context, entity *message.Entity, path string, removedPaths map[string]struct{}, writer io.Writer, topLevel bool) error {
	mediaType, _, _ := entity.Header.ContentType()
	header := rewriteMIMEHeader(entity.Header, topLevel, mediaType)
	root, err := message.CreateWriter(writer, header)
	if err != nil {
		return err
	}
	if err := rewriteEntityBody(ctx, entity, path, removedPaths, root); err != nil {
		_ = root.Close()
		return err
	}
	return root.Close()
}

func rewriteEntityBody(ctx context.Context, entity *message.Entity, path string, removedPaths map[string]struct{}, output *message.Writer) error {
	if multipartReader := entity.MultipartReader(); multipartReader != nil {
		for index := 1; ; index++ {
			child, err := multipartReader.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if child == nil {
				return err
			}
			childPath := fmt.Sprintf("%s.%d", path, index)
			if path == "0" {
				childPath = fmt.Sprintf("%d", index)
			}
			if _, removed := removedPaths[childPath]; removed {
				if err := drainRewriteEntity(ctx, child); err != nil {
					return err
				}
				continue
			}
			mediaType, _, _ := child.Header.ContentType()
			part, err := output.CreatePart(rewriteMIMEHeader(child.Header, false, mediaType))
			if err != nil {
				return err
			}
			if mediaType == "message/rfc822" {
				nested, readErr := message.ReadWithOptions(&contextReader{ctx: ctx, reader: child.Body}, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
				if nested == nil {
					_ = part.Close()
					return fmt.Errorf("read embedded rewrite source: %w", readErr)
				}
				if err := rewriteRootEntity(ctx, nested, childPath+".1", removedPaths, part, false); err != nil {
					_ = part.Close()
					return err
				}
			} else if err := rewriteEntityBody(ctx, child, childPath, removedPaths, part); err != nil {
				_ = part.Close()
				return err
			}
			if err := part.Close(); err != nil {
				return err
			}
		}
	}
	mediaType, params, _ := entity.Header.ContentType()
	if mediaType == "message/rfc822" {
		nested, err := message.ReadWithOptions(&contextReader{ctx: ctx, reader: entity.Body}, &message.ReadOptions{MaxHeaderBytes: 1 << 20})
		if nested == nil {
			return fmt.Errorf("read embedded rewrite source: %w", err)
		}
		return rewriteRootEntity(ctx, nested, path+".1", removedPaths, output, false)
	}
	body := io.Reader(&contextReader{ctx: ctx, reader: entity.Body})
	if strings.HasPrefix(strings.ToLower(mediaType), "text/") {
		if label := strings.TrimSpace(params["charset"]); label != "" && !strings.EqualFold(label, "utf-8") && !strings.EqualFold(label, "us-ascii") {
			decoded, err := charset.Reader(label, body)
			if err != nil {
				return fmt.Errorf("decode rewrite charset %q: %w", label, err)
			}
			body = decoded
		}
	}
	_, err := io.Copy(output, body)
	return err
}

func drainRewriteEntity(ctx context.Context, entity *message.Entity) error {
	if multipartReader := entity.MultipartReader(); multipartReader != nil {
		for {
			child, err := multipartReader.NextPart()
			if errors.Is(err, io.EOF) {
				return nil
			}
			if child == nil {
				return err
			}
			if err := drainRewriteEntity(ctx, child); err != nil {
				return err
			}
		}
	}
	_, err := io.Copy(io.Discard, &contextReader{ctx: ctx, reader: entity.Body})
	return err
}

func rewriteMIMEHeader(source message.Header, topLevel bool, mediaType string) message.Header {
	header := source.Copy()
	if topLevel {
		for _, key := range []string{
			"Return-Path", "Received", "Delivered-To", "Authentication-Results",
			"ARC-Authentication-Results", "ARC-Message-Signature", "ARC-Seal",
			"DKIM-Signature", "DomainKey-Signature",
		} {
			header.Del(key)
		}
	}
	header.Del("Content-Length")
	header.Del("Content-MD5")
	mediaType = strings.ToLower(strings.TrimSpace(mediaType))
	parsedType, params, _ := header.ContentType()
	if mediaType == "" {
		mediaType = parsedType
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		header.SetContentType(mediaType, params)
		header.Del("Content-Transfer-Encoding")
		return header
	}
	if strings.HasPrefix(mediaType, "text/") {
		if charsetValue := strings.TrimSpace(params["charset"]); charsetValue != "" && !strings.EqualFold(charsetValue, "utf-8") && !strings.EqualFold(charsetValue, "us-ascii") {
			params["charset"] = "utf-8"
		}
		header.SetContentType(mediaType, params)
		header.Set("Content-Transfer-Encoding", "quoted-printable")
	} else {
		header.SetContentType(mediaType, params)
		header.Set("Content-Transfer-Encoding", "base64")
	}
	return header
}
