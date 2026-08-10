package mail

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var (
	ErrInvalidMailboxImport  = errors.New("invalid mboxrd mailbox import")
	ErrMailboxImportNoRecord = errors.New("no mboxrd record at offset")
)

const maxMboxSeparatorBytes = 64 << 10

type MboxRDRecord struct {
	StartOffset    int64      `json:"start_offset"`
	NextOffset     int64      `json:"next_offset"`
	RawContentHash string     `json:"raw_content_hash"`
	RawByteSize    int64      `json:"raw_byte_size"`
	EnvelopeDate   *time.Time `json:"envelope_date,omitempty"`
	EOF            bool       `json:"eof"`
}

// DecodeMboxRDRecord seeks to one mbox separator, decodes exactly one mboxrd
// record into writer, and returns the byte offset of the next separator. It
// buffers only a small parser window plus pending LF-only framing lines.
func DecodeMboxRDRecord(ctx context.Context, source io.ReadSeeker, startOffset int64, writer io.Writer) (MboxRDRecord, error) {
	if source == nil || writer == nil || startOffset < 0 {
		return MboxRDRecord{}, ErrInvalidMailboxImport
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if _, err := source.Seek(startOffset, io.SeekStart); err != nil {
		return MboxRDRecord{}, fmt.Errorf("seek mboxrd source: %w", err)
	}
	reader := bufio.NewReaderSize(&mailboxImportContextReader{ctx: ctx, reader: source}, 64<<10)
	separatorBytes, envelopeDate, err := consumeMboxSeparator(reader)
	if err != nil {
		return MboxRDRecord{}, err
	}
	archiveOffset := startOffset + separatorBytes
	hash := sha256.New()
	decoded := &countingWriter{writer: io.MultiWriter(writer, hash)}
	pendingBlankLF := int64(0)

	flushPending := func(stripOne bool) error {
		count := pendingBlankLF
		if stripOne && count > 0 {
			count--
		}
		for count > 0 {
			if _, err := decoded.Write([]byte{'\n'}); err != nil {
				return err
			}
			count--
		}
		pendingBlankLF = 0
		return nil
	}

	for {
		if err := ctx.Err(); err != nil {
			return MboxRDRecord{}, err
		}
		prefix, peekErr := reader.Peek(5)
		if len(prefix) >= 5 && string(prefix[:5]) == "From " {
			if err := flushPending(true); err != nil {
				return MboxRDRecord{}, err
			}
			return MboxRDRecord{
				StartOffset: startOffset, NextOffset: archiveOffset,
				RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
				EnvelopeDate: envelopeDate,
				EOF:          false,
			}, nil
		}
		if errors.Is(peekErr, io.EOF) && len(prefix) == 0 {
			if err := flushPending(true); err != nil {
				return MboxRDRecord{}, err
			}
			return MboxRDRecord{
				StartOffset: startOffset, NextOffset: archiveOffset,
				RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
				EnvelopeDate: envelopeDate,
				EOF:          true,
			}, nil
		}

		first, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				if err := flushPending(true); err != nil {
					return MboxRDRecord{}, err
				}
				return MboxRDRecord{
					StartOffset: startOffset, NextOffset: archiveOffset,
					RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
					EnvelopeDate: envelopeDate,
					EOF:          true,
				}, nil
			}
			return MboxRDRecord{}, err
		}
		archiveOffset++
		if first == '\n' {
			pendingBlankLF++
			continue
		}
		if err := flushPending(false); err != nil {
			return MboxRDRecord{}, err
		}

		if first == '>' {
			leading := int64(1)
			for {
				value, err := reader.ReadByte()
				if err != nil {
					if errors.Is(err, io.EOF) {
						if err := writeRepeatedImportByte(decoded, '>', leading); err != nil {
							return MboxRDRecord{}, err
						}
						return MboxRDRecord{
							StartOffset: startOffset, NextOffset: archiveOffset,
							RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
							EnvelopeDate: envelopeDate,
							EOF:          true,
						}, nil
					}
					return MboxRDRecord{}, err
				}
				archiveOffset++
				if value == '>' {
					leading++
					continue
				}
				lookahead := []byte{value}
				if value == 'F' {
					for len(lookahead) < len("From ") {
						next, nextErr := reader.ReadByte()
						if nextErr != nil {
							if !errors.Is(nextErr, io.EOF) {
								return MboxRDRecord{}, nextErr
							}
							break
						}
						archiveOffset++
						lookahead = append(lookahead, next)
						if next != "From "[len(lookahead)-1] {
							break
						}
					}
				}
				if string(lookahead) == "From " {
					leading--
				}
				if err := writeRepeatedImportByte(decoded, '>', leading); err != nil {
					return MboxRDRecord{}, err
				}
				if _, err := decoded.Write(lookahead); err != nil {
					return MboxRDRecord{}, err
				}
				if len(lookahead) > 0 && lookahead[len(lookahead)-1] == '\n' {
					break
				}
				if err := streamMboxRecordLineRemainder(reader, decoded, &archiveOffset); err != nil {
					if errors.Is(err, io.EOF) {
						return MboxRDRecord{
							StartOffset: startOffset, NextOffset: archiveOffset,
							RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
							EnvelopeDate: envelopeDate,
							EOF:          true,
						}, nil
					}
					return MboxRDRecord{}, err
				}
				break
			}
			continue
		}

		if _, err := decoded.Write([]byte{first}); err != nil {
			return MboxRDRecord{}, err
		}
		if first != '\n' {
			if err := streamMboxRecordLineRemainder(reader, decoded, &archiveOffset); err != nil {
				if errors.Is(err, io.EOF) {
					return MboxRDRecord{
						StartOffset: startOffset, NextOffset: archiveOffset,
						RawContentHash: hex.EncodeToString(hash.Sum(nil)), RawByteSize: decoded.count,
						EnvelopeDate: envelopeDate,
						EOF:          true,
					}, nil
				}
				return MboxRDRecord{}, err
			}
		}
	}
}

func consumeMboxSeparator(reader *bufio.Reader) (int64, *time.Time, error) {
	prefix, err := reader.Peek(5)
	if len(prefix) < 5 || string(prefix[:5]) != "From " {
		if errors.Is(err, io.EOF) && len(prefix) == 0 {
			return 0, nil, ErrMailboxImportNoRecord
		}
		return 0, nil, ErrInvalidMailboxImport
	}
	var count int64
	line := make([]byte, 0, 128)
	for count <= maxMboxSeparatorBytes {
		value, readErr := reader.ReadByte()
		if readErr != nil {
			return 0, nil, ErrInvalidMailboxImport
		}
		count++
		line = append(line, value)
		if value == '\n' {
			return count, parseMboxEnvelopeDate(line), nil
		}
	}
	return 0, nil, ErrInvalidMailboxImport
}

func parseMboxEnvelopeDate(line []byte) *time.Time {
	value := strings.TrimSpace(string(line))
	if !strings.HasPrefix(value, "From ") {
		return nil
	}
	rest := strings.TrimSpace(strings.TrimPrefix(value, "From "))
	separator := strings.IndexByte(rest, ' ')
	if separator < 1 || separator+1 >= len(rest) {
		return nil
	}
	dateText := strings.TrimSpace(rest[separator+1:])
	for _, layout := range []string{
		time.ANSIC,
		"Mon Jan _2 15:04:05 -0700 2006",
		"Mon Jan _2 15:04:05 2006 -0700",
	} {
		parsed, err := time.Parse(layout, dateText)
		if err == nil {
			parsed = parsed.UTC()
			return &parsed
		}
	}
	return nil
}

func streamMboxRecordLineRemainder(reader *bufio.Reader, writer io.Writer, archiveOffset *int64) error {
	for {
		chunk, err := reader.ReadSlice('\n')
		if len(chunk) > 0 {
			*archiveOffset += int64(len(chunk))
			if _, writeErr := writer.Write(chunk); writeErr != nil {
				return writeErr
			}
		}
		switch {
		case err == nil:
			return nil
		case errors.Is(err, bufio.ErrBufferFull):
			continue
		case errors.Is(err, io.EOF):
			return io.EOF
		default:
			return err
		}
	}
}

func writeRepeatedImportByte(writer io.Writer, value byte, count int64) error {
	var chunk [4096]byte
	for index := range chunk {
		chunk[index] = value
	}
	for count > 0 {
		current := int64(len(chunk))
		if count < current {
			current = count
		}
		if _, err := writer.Write(chunk[:current]); err != nil {
			return err
		}
		count -= current
	}
	return nil
}

type mailboxImportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *mailboxImportContextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}
