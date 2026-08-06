package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	DefaultExcerptRunes = 160
	DefaultPageSize     = 20
	MaximumPageSize     = 100
)

type OutputOptions struct {
	JSON     bool
	JSONL    bool
	Full     bool
	Verbose  bool
	Format   string
	Headers  bool
	BodyOnly bool
}

type OutputSupport struct {
	JSON     bool
	JSONL    bool
	Full     bool
	Verbose  bool
	Text     bool
	HTML     bool
	Raw      bool
	Headers  bool
	BodyOnly bool
}

func ValidateOutputOptions(options OutputOptions, support OutputSupport) *AppError {
	if options.JSON && options.JSONL {
		return NewAppError(ErrorUsage, "--json and --jsonl cannot be used together", false)
	}
	if options.JSON && !support.JSON {
		return NewAppError(ErrorUsage, "--json is not supported by this command", false)
	}
	if options.JSONL && !support.JSONL {
		return NewAppError(ErrorUsage, "--jsonl is not supported by this command", false)
	}
	if options.Full && !support.Full {
		return NewAppError(ErrorUsage, "--full is not supported by this command", false)
	}
	if options.Verbose && !support.Verbose {
		return NewAppError(ErrorUsage, "--verbose is not supported by this command", false)
	}
	if options.Headers && !support.Headers {
		return NewAppError(ErrorUsage, "--headers is not supported by this command", false)
	}
	if options.BodyOnly && !support.BodyOnly {
		return NewAppError(ErrorUsage, "--body-only is not supported by this command", false)
	}
	if options.Headers && options.BodyOnly {
		return NewAppError(ErrorUsage, "--headers and --body-only cannot be used together", false)
	}

	format := strings.ToLower(strings.TrimSpace(options.Format))
	if format == "" || format == "compact" {
		return nil
	}
	switch format {
	case "text":
		if !support.Text {
			return NewAppError(ErrorUsage, "--format text is not supported by this command", false)
		}
	case "html":
		if !support.HTML {
			return NewAppError(ErrorUsage, "--format html is not supported by this command", false)
		}
	case "raw":
		if !support.Raw {
			return NewAppError(ErrorUsage, "--format raw is not supported by this command", false)
		}
		if options.Headers || options.BodyOnly {
			return NewAppError(ErrorUsage, "--format raw cannot be combined with --headers or --body-only", false)
		}
	default:
		return NewAppError(ErrorUsage, "--format must be one of compact, text, html, or raw", false)
	}
	return nil
}

type CompactField struct {
	Key   string
	Value any
}

func Field(key string, value any) CompactField {
	return CompactField{Key: key, Value: value}
}

// CompactTextRenderer is the universal undecorated default-output boundary.
// Callers control field order; empty and default-valued fields are omitted.
// Each record stays on one dense line, while consecutive records are separated
// by one blank line so bounded multi-record output remains easy to scan.
type CompactTextRenderer struct {
	w       io.Writer
	records int
}

func NewCompactTextRenderer(w io.Writer) *CompactTextRenderer {
	return &CompactTextRenderer{w: w}
}

func (r *CompactTextRenderer) Record(kind, id string, fields ...CompactField) error {
	parts := make([]string, 0, 2+len(fields))
	if kind = strings.TrimSpace(kind); kind != "" {
		parts = append(parts, kind)
	}
	if id = strings.TrimSpace(id); id != "" {
		parts = append(parts, compactToken(id))
	}
	for _, field := range fields {
		if strings.TrimSpace(field.Key) == "" || isEmptyValue(field.Value) {
			continue
		}
		parts = append(parts, strings.TrimSpace(field.Key)+"="+compactValue(field.Value))
	}
	line := strings.Join(parts, " ")
	if r.records > 0 {
		line = "\n" + line
	}
	if _, err := fmt.Fprintln(r.w, line); err != nil {
		return err
	}
	r.records++
	return nil
}

func (r *CompactTextRenderer) Summary(kind string, fields ...CompactField) error {
	return r.Record(kind, "", fields...)
}

func (r *CompactTextRenderer) Event(name string, sequence uint64, fields ...CompactField) error {
	eventFields := make([]CompactField, 0, len(fields)+1)
	if sequence > 0 {
		eventFields = append(eventFields, Field("sequence", sequence))
	}
	eventFields = append(eventFields, fields...)
	return r.Record("event", name, eventFields...)
}

func (r *CompactTextRenderer) Error(err *AppError) error {
	fields := []CompactField{
		Field("code", err.Code),
		Field("message", err.Message),
	}
	if err.Retryable {
		fields = append(fields, Field("retryable", true))
	}
	return r.Record("error", "", fields...)
}

func Excerpt(value string, maxRunes int) string {
	value = compactWhitespace(value)
	if maxRunes <= 0 || utf8.RuneCountInString(value) <= maxRunes {
		return value
	}
	runes := []rune(value)
	if maxRunes == 1 {
		return "…"
	}
	return strings.TrimSpace(string(runes[:maxRunes-1])) + "…"
}

func compactToken(value string) string {
	value = compactWhitespace(value)
	if value == "" {
		return ""
	}
	if strings.ContainsAny(value, " \t\r\n\"=") {
		return strconv.Quote(value)
	}
	return value
}

func compactValue(value any) string {
	switch typed := value.(type) {
	case string:
		return compactToken(typed)
	case ErrorCode:
		return compactToken(string(typed))
	case time.Time:
		return typed.UTC().Format(time.RFC3339)
	case *time.Time:
		if typed == nil {
			return ""
		}
		return typed.UTC().Format(time.RFC3339)
	case fmt.Stringer:
		return compactToken(typed.String())
	case bool:
		return strconv.FormatBool(typed)
	case []string:
		return compactToken(strings.Join(typed, ","))
	default:
		return compactToken(fmt.Sprint(value))
	}
}

func isEmptyValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.String, reflect.Array, reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Pointer, reflect.Interface:
		return v.IsNil()
	}
	return false
}

type EnvelopeMeta struct {
	RequestID string `json:"request_id"`
}

type OutputEnvelope struct {
	OK    bool         `json:"ok"`
	Data  any          `json:"data,omitempty"`
	Error *AppError    `json:"error,omitempty"`
	Meta  EnvelopeMeta `json:"meta"`
}

func WriteJSON(w io.Writer, envelope OutputEnvelope) error {
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(envelope)
}

type JSONLEvent struct {
	Event     string    `json:"event"`
	Sequence  uint64    `json:"sequence"`
	AccountID string    `json:"account_id,omitempty"`
	Timestamp time.Time `json:"timestamp"`
	Payload   any       `json:"payload,omitempty"`
	Error     *AppError `json:"error,omitempty"`
}

func WriteJSONLEvent(w io.Writer, event JSONLEvent) error {
	if strings.TrimSpace(event.Event) == "" {
		return NewAppError(ErrorUsage, "JSONL event type is required", false)
	}
	if event.Timestamp.IsZero() {
		return NewAppError(ErrorUsage, "JSONL event timestamp is required", false)
	}
	encoder := json.NewEncoder(w)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(event)
}

func NewRequestID() string {
	var bytes [12]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "req_unavailable"
	}
	return "req_" + hex.EncodeToString(bytes[:])
}
