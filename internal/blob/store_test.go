package blob

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestStageCommitIsPrivateContentAddressedAndIdempotent(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 1024)
	content := []byte("synthetic RFC822 fixture\r\n")
	staged, err := store.Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if staged.ByteSize != int64(len(content)) || len(staged.ContentHash) != 64 || !strings.HasPrefix(staged.StorageKey, "raw/sha256/") {
		t.Fatalf("staged = %+v", staged)
	}
	reader, err := staged.Open()
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("content=%q err=%v", got, err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(staged.StorageKey))
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("blob mode = %o", info.Mode().Perm())
	}
	for _, dir := range []string{root, filepath.Dir(path)} {
		info, err := os.Stat(dir)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("directory mode for %s = %o", dir, info.Mode().Perm())
		}
	}
}

func TestStageAttachmentUsesSeparateContentAddressedNamespace(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 1024)
	content := []byte{0, 1, 2, 3}
	staged, err := store.StageAttachment(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(staged.StorageKey, "attachments/sha256/") || !strings.HasSuffix(staged.StorageKey, ".blob") {
		t.Fatalf("attachment storage key = %q", staged.StorageKey)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	reader, err := store.OpenVerified(staged.StorageKey, staged.ContentHash, staged.ByteSize)
	if err != nil {
		t.Fatal(err)
	}
	got, readErr := io.ReadAll(reader)
	if closeErr := reader.Close(); readErr == nil {
		readErr = closeErr
	}
	if readErr != nil || !bytes.Equal(got, content) {
		t.Fatalf("attachment=%v err=%v", got, readErr)
	}
}

func TestStageRejectsOversizeAndCancellationWithoutLeavingTemporaryFiles(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 8)
	if _, err := store.Stage(context.Background(), strings.NewReader("123456789"), 9); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.Stage(ctx, strings.NewReader("1234"), 4); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error = %v", err)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".staging"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging entries = %d", len(entries))
	}
}

func TestStoreUsesIndependentRawAndAttachmentLimits(t *testing.T) {
	store := NewWithLimits(t.TempDir(), 4, 8)
	if _, err := store.Stage(context.Background(), strings.NewReader("12345"), 5); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("raw oversize error = %v", err)
	}
	staged, err := store.StageAttachment(context.Background(), strings.NewReader("12345"), 5)
	if err != nil {
		t.Fatalf("attachment within independent limit: %v", err)
	}
	_ = staged.Abort()
}

func TestCommitRejectsCorruptExistingCanonicalBlob(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 1024)
	content := []byte("same-size-content")
	first, err := store.Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Commit(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, filepath.FromSlash(first.StorageKey))
	if err := os.WriteFile(path, []byte("corrupt-content!"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := store.Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	defer second.Abort()
	if err := second.Commit(); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("commit error = %v", err)
	}
}

func TestOpenVerifiedStreamsExactContentAndRejectsCorruptionAndUnsafeKeys(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 1024)
	content := []byte("Message-ID: <verified@example.test>\r\n\r\nExact raw bytes.\r\n")
	staged, err := store.Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	reader, err := store.OpenVerified(staged.StorageKey, staged.ContentHash, staged.ByteSize)
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(reader)
	if closeErr := reader.Close(); err == nil {
		err = closeErr
	}
	if err != nil || !bytes.Equal(got, content) {
		t.Fatalf("verified content=%q err=%v", got, err)
	}
	if _, err := store.OpenVerified(staged.StorageKey, staged.ContentHash, staged.ByteSize+1); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("size mismatch = %v", err)
	}
	if _, err := store.OpenVerified("../../outside", staged.ContentHash, staged.ByteSize); err == nil {
		t.Fatal("unsafe storage key was accepted")
	}
	path := filepath.Join(root, filepath.FromSlash(staged.StorageKey))
	corrupt := append([]byte(nil), content...)
	corrupt[len(corrupt)-3] ^= 0x7f
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.OpenVerified(staged.StorageKey, staged.ContentHash, staged.ByteSize); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("hash mismatch = %v", err)
	}
}

func TestOpenVerifiedHonorsCancellationBeforeHashing(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := NewWithLimit(root, 1024)
	content := []byte("Message-ID: <cancel@example.test>\r\n\r\nbody")
	staged, err := store.Stage(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.OpenVerifiedContext(ctx, staged.StorageKey, staged.ContentHash, staged.ByteSize); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled verification = %v", err)
	}
}
