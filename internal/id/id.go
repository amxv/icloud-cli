package id

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/google/uuid"
)

var prefixPattern = regexp.MustCompile(`^[a-z][a-z0-9]{1,15}$`)

// New returns an immutable prefixed UUIDv7 identifier.
func New(prefix string) (string, error) {
	prefix = strings.TrimSpace(prefix)
	if !prefixPattern.MatchString(prefix) {
		return "", fmt.Errorf("invalid ID prefix")
	}
	value, err := uuid.NewV7()
	if err != nil {
		return "", fmt.Errorf("generate UUIDv7: %w", err)
	}
	return prefix + "_" + value.String(), nil
}
