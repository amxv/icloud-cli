package store

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
)

var errInvalidOpaqueCursor = errors.New("invalid opaque cursor")

func encodeOpaqueCursor(prefix string, value any) (string, error) {
	payload, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return prefix + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeOpaqueCursor(prefix, value string, target any) error {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if !strings.HasPrefix(value, prefix) {
		return errInvalidOpaqueCursor
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return errInvalidOpaqueCursor
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return errInvalidOpaqueCursor
	}
	return nil
}
