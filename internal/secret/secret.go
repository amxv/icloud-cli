package secret

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"unicode"

	"github.com/amxv/icloud-cli/internal/account"
	keyring "github.com/zalando/go-keyring"
)

const MaxSecretBytes = 16 * 1024

var (
	ErrInvalidReference = errors.New("credential reference is invalid")
	ErrMissing          = errors.New("credential value is missing")
	ErrUnavailable      = errors.New("credential storage is unavailable")
)

type Keyring interface {
	Set(service, user, password string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type NativeKeyring struct{}

func (NativeKeyring) Set(service, user, password string) error {
	return keyring.Set(service, user, password)
}

func (NativeKeyring) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}

func (NativeKeyring) Delete(service, user string) error {
	return keyring.Delete(service, user)
}

type Manager struct {
	keyring   Keyring
	lookupEnv func(string) (string, bool)
	openFile  func(string) (io.ReadCloser, error)
}

func NewManager(backend Keyring) Manager {
	if backend == nil {
		backend = NativeKeyring{}
	}
	return Manager{
		keyring:   backend,
		lookupEnv: os.LookupEnv,
		openFile: func(path string) (io.ReadCloser, error) {
			return os.Open(path)
		},
	}
}

func KeyringReference(root, accountID string) (string, error) {
	root = strings.TrimSpace(root)
	accountID = strings.TrimSpace(accountID)
	if root == "" || accountID == "" || strings.Contains(accountID, ":") {
		return "", ErrInvalidReference
	}
	sum := sha256.Sum256([]byte(root))
	service := "icloud-cli-" + hex.EncodeToString(sum[:8])
	return "keyring:" + service + ":" + accountID, nil
}

func CanonicalReference(scheme account.CredentialScheme, reference string) (string, error) {
	switch scheme {
	case account.CredentialKeyring:
		service, user, err := parseKeyringReference(reference)
		if err != nil {
			return "", err
		}
		return "keyring:" + service + ":" + user, nil
	case account.CredentialEnv:
		name := environmentName(reference)
		if !envNamePattern.MatchString(name) {
			return "", ErrInvalidReference
		}
		return "env:" + name, nil
	case account.CredentialFile:
		path := filePath(reference)
		if strings.TrimSpace(path) == "" || containsControl(path) {
			return "", ErrInvalidReference
		}
		return "file:" + path, nil
	default:
		return "", ErrInvalidReference
	}
}

func (m Manager) StoreKeyring(ctx context.Context, reference string, value []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service, user, err := parseKeyringReference(reference)
	if err != nil {
		return err
	}
	secretValue, err := normalizeBytes(value)
	if err != nil {
		return err
	}
	password := string(secretValue)
	zeroBytes(secretValue)
	if err := m.keyring.Set(service, user, password); err != nil {
		return fmt.Errorf("%w: cannot write native keyring", ErrUnavailable)
	}
	return nil
}

func (m Manager) DeleteKeyring(ctx context.Context, reference string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	service, user, err := parseKeyringReference(reference)
	if err != nil {
		return err
	}
	if err := m.keyring.Delete(service, user); err != nil && !errors.Is(err, keyring.ErrNotFound) {
		return fmt.Errorf("%w: cannot delete native keyring entry", ErrUnavailable)
	}
	return nil
}

func (m Manager) Resolve(ctx context.Context, credential account.Credential) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch credential.Scheme {
	case account.CredentialKeyring:
		service, user, err := parseKeyringReference(credential.Reference)
		if err != nil {
			return nil, err
		}
		value, err := m.keyring.Get(service, user)
		if errors.Is(err, keyring.ErrNotFound) {
			return nil, fmt.Errorf("%w: native keyring entry is missing", ErrMissing)
		}
		if err != nil {
			return nil, fmt.Errorf("%w: cannot read native keyring", ErrUnavailable)
		}
		return normalizeBytes([]byte(value))
	case account.CredentialEnv:
		name := environmentName(credential.Reference)
		if !envNamePattern.MatchString(name) {
			return nil, ErrInvalidReference
		}
		value, ok := m.lookupEnv(name)
		if !ok {
			return nil, fmt.Errorf("%w: environment credential is not set", ErrMissing)
		}
		return normalizeBytes([]byte(value))
	case account.CredentialFile:
		path := filePath(credential.Reference)
		if path == "" {
			return nil, ErrInvalidReference
		}
		file, err := m.openFile(path)
		if err != nil {
			return nil, fmt.Errorf("%w: credential file cannot be read", ErrMissing)
		}
		defer file.Close()
		value, err := io.ReadAll(io.LimitReader(file, MaxSecretBytes+1))
		if err != nil {
			return nil, fmt.Errorf("%w: credential file cannot be read", ErrMissing)
		}
		return normalizeBytes(value)
	default:
		return nil, ErrInvalidReference
	}
}

func ValidateReference(scheme account.CredentialScheme, reference string) error {
	_, err := CanonicalReference(scheme, reference)
	return err
}

func parseKeyringReference(reference string) (string, string, error) {
	reference = strings.TrimSpace(reference)
	reference = strings.TrimPrefix(reference, "keyring:")
	service, user, ok := strings.Cut(reference, ":")
	if !ok || !keyringServicePattern.MatchString(service) || strings.TrimSpace(user) == "" || strings.Contains(user, ":") || containsControl(user) {
		return "", "", ErrInvalidReference
	}
	return service, user, nil
}

func environmentName(reference string) string {
	reference = strings.TrimSpace(reference)
	switch {
	case strings.HasPrefix(reference, "env://"):
		return strings.TrimSpace(strings.TrimPrefix(reference, "env://"))
	case strings.HasPrefix(reference, "env:"):
		return strings.TrimSpace(strings.TrimPrefix(reference, "env:"))
	case strings.HasPrefix(reference, "$"):
		return strings.TrimSpace(strings.TrimPrefix(reference, "$"))
	default:
		return reference
	}
}

func filePath(reference string) string {
	reference = strings.TrimSpace(reference)
	switch {
	case strings.HasPrefix(reference, "file://"):
		return strings.TrimSpace(strings.TrimPrefix(reference, "file://"))
	case strings.HasPrefix(reference, "file:"):
		return strings.TrimSpace(strings.TrimPrefix(reference, "file:"))
	default:
		return reference
	}
}

func containsControl(value string) bool {
	for _, character := range value {
		if unicode.IsControl(character) {
			return true
		}
	}
	return false
}

func normalizeBytes(value []byte) ([]byte, error) {
	if len(value) > MaxSecretBytes {
		return nil, fmt.Errorf("%w: credential exceeds the size limit", ErrInvalidReference)
	}
	value = []byte(strings.TrimRight(string(value), "\r\n"))
	if len(value) == 0 || strings.TrimSpace(string(value)) == "" {
		return nil, ErrMissing
	}
	return value, nil
}

var envNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
var keyringServicePattern = regexp.MustCompile(`^icloud-cli-[0-9a-f]{16}$`)

func zeroBytes(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
