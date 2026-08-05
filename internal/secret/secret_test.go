package secret

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/amxv/icloud-cli/internal/account"
	keyring "github.com/zalando/go-keyring"
)

type memoryKeyring struct {
	values map[string]string
	err    error
}

func newMemoryKeyring() *memoryKeyring {
	return &memoryKeyring{values: map[string]string{}}
}

func (m *memoryKeyring) Set(service, user, password string) error {
	if m.err != nil {
		return m.err
	}
	m.values[service+"\x00"+user] = password
	return nil
}

func (m *memoryKeyring) Get(service, user string) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	value, ok := m.values[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return value, nil
}

func (m *memoryKeyring) Delete(service, user string) error {
	if m.err != nil {
		return m.err
	}
	key := service + "\x00" + user
	if _, ok := m.values[key]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.values, key)
	return nil
}

func TestManagerResolvesEnvironmentAndFileReferencesWithoutLeakingTails(t *testing.T) {
	t.Setenv("ICLOUD_TEST_PASSWORD", "env-value\n")
	manager := NewManager(newMemoryKeyring())
	value, err := manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialEnv, Reference: "ICLOUD_TEST_PASSWORD"})
	if err != nil || string(value) != "env-value" {
		t.Fatalf("environment resolution = %q, %v", value, err)
	}

	path := filepath.Join(t.TempDir(), "credential")
	if err := os.WriteFile(path, []byte("file-value\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err = manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialFile, Reference: path})
	if err != nil || string(value) != "file-value" {
		t.Fatalf("file resolution = %q, %v", value, err)
	}

	_, err = manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialFile, Reference: filepath.Join(t.TempDir(), "private-tail")})
	if err == nil || strings.Contains(err.Error(), "private-tail") {
		t.Fatalf("missing-file error leaked the reference tail: %v", err)
	}
	_, err = manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialEnv, Reference: "ICLOUD_PRIVATE_MISSING"})
	if !errors.Is(err, ErrMissing) || strings.Contains(err.Error(), "ICLOUD_PRIVATE_MISSING") {
		t.Fatalf("missing-environment error leaked the reference: %v", err)
	}
}

func TestManagerStoresResolvesAndDeletesKeyringCredential(t *testing.T) {
	backend := newMemoryKeyring()
	manager := NewManager(backend)
	reference, err := KeyringReference("/isolated/state", "acct_01")
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.StoreKeyring(context.Background(), reference, []byte("keyring-value\n")); err != nil {
		t.Fatal(err)
	}
	value, err := manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialKeyring, Reference: reference})
	if err != nil || string(value) != "keyring-value" {
		t.Fatalf("keyring resolution = %q, %v", value, err)
	}
	if err := manager.DeleteKeyring(context.Background(), reference); err != nil {
		t.Fatal(err)
	}
	_, err = manager.Resolve(context.Background(), account.Credential{Scheme: account.CredentialKeyring, Reference: reference})
	if !errors.Is(err, ErrMissing) || strings.Contains(err.Error(), "acct_01") {
		t.Fatalf("missing-keyring error = %v", err)
	}
}

func TestCanonicalReferencePrefixesEveryPersistedScheme(t *testing.T) {
	keyringReference, err := KeyringReference("/isolated/state", "acct_01")
	if err != nil || !strings.HasPrefix(keyringReference, "keyring:") {
		t.Fatalf("keyring reference = %q, %v", keyringReference, err)
	}
	for _, test := range []struct {
		scheme   account.CredentialScheme
		input    string
		expected string
	}{
		{account.CredentialEnv, "env://ICLOUD_PASSWORD", "env:ICLOUD_PASSWORD"},
		{account.CredentialEnv, "$ICLOUD_PASSWORD", "env:ICLOUD_PASSWORD"},
		{account.CredentialFile, "file:/private/password", "file:/private/password"},
	} {
		actual, err := CanonicalReference(test.scheme, test.input)
		if err != nil || actual != test.expected {
			t.Fatalf("scheme=%s input=%q actual=%q err=%v", test.scheme, test.input, actual, err)
		}
	}
}

func TestManagerRejectsInvalidAndOversizeReferences(t *testing.T) {
	manager := NewManager(newMemoryKeyring())
	for _, credential := range []account.Credential{
		{Scheme: account.CredentialEnv, Reference: "bad-name"},
		{Scheme: account.CredentialKeyring, Reference: "secret://private-tail"},
		{Scheme: "unknown", Reference: "private-tail"},
	} {
		_, err := manager.Resolve(context.Background(), credential)
		if !errors.Is(err, ErrInvalidReference) || strings.Contains(err.Error(), "private-tail") {
			t.Fatalf("credential=%+v error=%v", credential, err)
		}
	}
	if err := manager.StoreKeyring(context.Background(), "icloud-cli-deadbeef:acct_01", []byte(strings.Repeat("x", MaxSecretBytes+1))); !errors.Is(err, ErrInvalidReference) {
		t.Fatalf("oversize error = %v", err)
	}
}
