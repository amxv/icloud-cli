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
