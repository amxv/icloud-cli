package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/amxv/icloud-cli/internal/blob"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

func TestAttachmentContentSourceVerifiesHashAndMissingBlob(t *testing.T) {
	root := filepath.Join(t.TempDir(), "blobs")
	store := blob.NewWithLimit(root, 1024)
	content := []byte("canonical attachment bytes")
	staged, err := store.StageAttachment(context.Background(), bytes.NewReader(content), int64(len(content)))
	if err != nil {
		t.Fatal(err)
	}
	if err := staged.Commit(); err != nil {
		t.Fatal(err)
	}
	source := &attachmentContentSource{store: store, attachment: maildomain.Attachment{
		StorageKey: staged.StorageKey, ContentHash: staged.ContentHash, ByteSize: staged.ByteSize,
	}}
	var output bytes.Buffer
	if err := source.Write(context.Background(), &output); err != nil || !bytes.Equal(output.Bytes(), content) {
		t.Fatalf("verified attachment=%q err=%v", output.Bytes(), err)
	}
	path := filepath.Join(root, filepath.FromSlash(staged.StorageKey))
	corrupt := append([]byte(nil), content...)
	corrupt[0] ^= 0x7f
	if err := os.WriteFile(path, corrupt, 0o600); err != nil {
		t.Fatal(err)
	}
	output.Reset()
	if err := source.Write(context.Background(), &output); !errors.Is(err, maildomain.ErrContentCorrupt) || output.Len() != 0 {
		t.Fatalf("corrupt attachment output=%q err=%v", output.Bytes(), err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := source.Write(context.Background(), &output); !errors.Is(err, maildomain.ErrContentUnavailable) {
		t.Fatalf("missing attachment err=%v", err)
	}
}
