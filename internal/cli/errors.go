package cli

import (
	"errors"
	"strings"
)

type ErrorCode string

const (
	ErrorUsage          ErrorCode = "usage"
	ErrorConfiguration  ErrorCode = "configuration"
	ErrorAuthentication ErrorCode = "authentication"
	ErrorUnavailable    ErrorCode = "unavailable"
	ErrorConflict       ErrorCode = "conflict"
	ErrorNotFound       ErrorCode = "not_found"
	ErrorSafety         ErrorCode = "safety"
	ErrorInternal       ErrorCode = "internal"
)

const (
	ExitSuccess        = 0
	ExitInternal       = 1
	ExitUsage          = 2
	ExitConfiguration  = 3
	ExitAuthentication = 4
	ExitUnavailable    = 5
	ExitConflict       = 6
	ExitNotFound       = 7
	ExitSafety         = 8
)

// AppError is the stable application error boundary used by text and JSON output.
type AppError struct {
	Code      ErrorCode      `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
	cause     error
}

// jsonlHandledError preserves the command's non-zero exit status after a bulk
// JSONL command has already emitted its ordered item records and terminal
// completed event. The root must not append a second, detached stderr error
// event because the item stream already contains the authoritative failures.
type jsonlHandledError struct{ err error }

func (e jsonlHandledError) Error() string { return e.err.Error() }
func (e jsonlHandledError) Unwrap() error { return e.err }

func markJSONLHandled(err error) error {
	if err == nil {
		return nil
	}
	return jsonlHandledError{err: err}
}

func isJSONLHandled(err error) bool {
	var handled jsonlHandledError
	return errors.As(err, &handled)
}

func (e *AppError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *AppError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.cause
}

func NewAppError(code ErrorCode, message string, retryable bool) *AppError {
	return &AppError{
		Code:      code,
		Message:   compactWhitespace(message),
		Retryable: retryable,
	}
}

func WrapAppError(code ErrorCode, message string, retryable bool, cause error) *AppError {
	err := NewAppError(code, message, retryable)
	err.cause = cause
	return err
}

func NormalizeError(err error) *AppError {
	if err == nil {
		return nil
	}
	var appErr *AppError
	if errors.As(err, &appErr) {
		copy := *appErr
		copy.Message = compactWhitespace(copy.Message)
		return &copy
	}

	message := compactWhitespace(err.Error())
	if isUsageMessage(message) {
		return WrapAppError(ErrorUsage, message, false, err)
	}
	return WrapAppError(ErrorInternal, "unexpected internal failure", false, err)
}

func ExitCodeFor(err *AppError) int {
	if err == nil {
		return ExitSuccess
	}
	switch err.Code {
	case ErrorUsage:
		return ExitUsage
	case ErrorConfiguration:
		return ExitConfiguration
	case ErrorAuthentication:
		return ExitAuthentication
	case ErrorUnavailable:
		return ExitUnavailable
	case ErrorConflict:
		return ExitConflict
	case ErrorNotFound:
		return ExitNotFound
	case ErrorSafety:
		return ExitSafety
	default:
		return ExitInternal
	}
}

func isUsageMessage(message string) bool {
	for _, prefix := range []string{
		"unknown command",
		"unknown flag",
		"required flag",
		"accepts ",
		"requires ",
		"invalid argument",
	} {
		if strings.HasPrefix(strings.ToLower(message), prefix) {
			return true
		}
	}
	return false
}

func compactWhitespace(value string) string {
	return strings.Join(strings.Fields(value), " ")
}
