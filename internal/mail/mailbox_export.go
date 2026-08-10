package mail

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
)

var ErrInvalidMailboxExport = errors.New("invalid mailbox export request")

type MailboxExportCursor struct {
	UIDValidity uint32 `json:"uid_validity"`
	UID         uint32 `json:"uid"`
	LocationID  string `json:"location_id"`
}

type MailboxExportRecord struct {
	MessageID      string    `json:"message_id"`
	LocationID     string    `json:"location_id"`
	UIDValidity    uint32    `json:"uid_validity"`
	UID            uint32    `json:"uid"`
	EnvelopeSender string    `json:"-"`
	EnvelopeDate   time.Time `json:"-"`
	RawContentHash string    `json:"-"`
	RawStorageKey  string    `json:"-"`
	RawByteSize    int64     `json:"raw_byte_size"`
}

type MailboxExportPage struct {
	Records    []MailboxExportRecord `json:"records"`
	More       bool                  `json:"more"`
	NextCursor *MailboxExportCursor  `json:"next_cursor,omitempty"`
}

type MailboxExportSummary struct {
	Format     string               `json:"format"`
	Count      int                  `json:"count"`
	Bytes      int64                `json:"bytes"`
	Complete   bool                 `json:"complete"`
	LastCursor *MailboxExportCursor `json:"last_cursor,omitempty"`
}

// WriteMboxRDMessage emits one mboxrd record without buffering the message.
// mboxrd reversibly quotes every line beginning with zero or more '>' bytes
// followed by "From " by adding one leading '>'. Message bytes otherwise pass
// through unchanged; the archive framing uses LF separators.
func WriteMboxRDMessage(ctx context.Context, writer io.Writer, envelopeSender string, envelopeDate time.Time, raw io.Reader) (int64, error) {
	if writer == nil || raw == nil {
		return 0, ErrInvalidMailboxExport
	}
	if ctx == nil {
		ctx = context.Background()
	}
	envelopeSender = sanitizeMboxEnvelopeSender(envelopeSender)
	if envelopeDate.IsZero() {
		envelopeDate = time.Unix(0, 0).UTC()
	}
	bufferedWriter := bufio.NewWriterSize(writer, 64<<10)
	counter := &countingWriter{writer: bufferedWriter}
	if _, err := fmt.Fprintf(counter, "From %s %s\n", envelopeSender, envelopeDate.UTC().Format("Mon Jan _2 15:04:05 2006")); err != nil {
		return counter.count, err
	}

	reader := bufio.NewReaderSize(&mailboxExportContextReader{ctx: ctx, reader: raw}, 64<<10)
	wroteRaw := false
	lastRaw := byte(0)
	atLineStart := true
	for {
		if err := ctx.Err(); err != nil {
			return counter.count, err
		}
		if atLineStart {
			leading, lookahead, eof, err := readMboxRDLinePrefix(reader)
			if err != nil {
				return counter.count, err
			}
			if leading == 0 && len(lookahead) == 0 && eof {
				break
			}
			if string(lookahead) == "From " {
				if _, err := counter.Write([]byte{'>'}); err != nil {
					return counter.count, err
				}
			}
			if err := writeRepeatedByte(counter, '>', leading); err != nil {
				return counter.count, err
			}
			if len(lookahead) > 0 {
				if _, err := counter.Write(lookahead); err != nil {
					return counter.count, err
				}
				wroteRaw = true
				lastRaw = lookahead[len(lookahead)-1]
				atLineStart = lastRaw == '\n'
			} else if leading > 0 {
				wroteRaw = true
				lastRaw = '>'
				atLineStart = false
			}
			if eof {
				break
			}
			continue
		}

		value, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return counter.count, err
		}
		if _, err := counter.Write([]byte{value}); err != nil {
			return counter.count, err
		}
		wroteRaw = true
		lastRaw = value
		atLineStart = value == '\n'
	}

	if !wroteRaw || lastRaw == '\n' {
		if _, err := counter.Write([]byte{'\n'}); err != nil {
			return counter.count, err
		}
	} else {
		if _, err := counter.Write([]byte{'\n', '\n'}); err != nil {
			return counter.count, err
		}
	}
	if err := bufferedWriter.Flush(); err != nil {
		return counter.count, err
	}
	return counter.count, nil
}

func readMboxRDLinePrefix(reader *bufio.Reader) (uint64, []byte, bool, error) {
	var leading uint64
	for {
		value, err := reader.ReadByte()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return leading, nil, true, nil
			}
			return 0, nil, false, err
		}
		if value == '>' {
			leading++
			continue
		}
		lookahead := []byte{value}
		if value != 'F' {
			return leading, lookahead, false, nil
		}
		expected := []byte("From ")
		for index := 1; index < len(expected); index++ {
			value, err = reader.ReadByte()
			if err != nil {
				if errors.Is(err, io.EOF) {
					return leading, lookahead, true, nil
				}
				return 0, nil, false, err
			}
			lookahead = append(lookahead, value)
			if value != expected[index] {
				return leading, lookahead, false, nil
			}
		}
		return leading, lookahead, false, nil
	}
}

func sanitizeMboxEnvelopeSender(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "MAILER-DAEMON"
	}
	for _, current := range value {
		if current <= ' ' || current == 0x7f {
			return "MAILER-DAEMON"
		}
	}
	return value
}

func writeRepeatedByte(writer io.Writer, value byte, count uint64) error {
	if count == 0 {
		return nil
	}
	var chunk [4096]byte
	for index := range chunk {
		chunk[index] = value
	}
	for count > 0 {
		current := uint64(len(chunk))
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

type countingWriter struct {
	writer io.Writer
	count  int64
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	written, err := writer.writer.Write(value)
	writer.count += int64(written)
	return written, err
}

type mailboxExportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *mailboxExportContextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}
