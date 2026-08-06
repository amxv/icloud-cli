package blob

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const DefaultMaxRawMessageBytes int64 = 64 << 20

var (
	ErrTooLarge = errors.New("blob exceeds the configured size limit")
	ErrCorrupt  = errors.New("blob content does not match its content address")
)

// Stager writes an incoming stream into a private temporary file. The caller
// must either Commit or Abort the returned staged blob.
type Stager interface {
	Stage(context.Context, io.Reader, int64) (*Staged, error)
}

// Store owns the content-addressed blob directory beneath one CLI state root.
type Store struct {
	root     string
	maxBytes int64
}

func New(root string) *Store {
	return &Store{root: filepath.Clean(root), maxBytes: DefaultMaxRawMessageBytes}
}

func NewWithLimit(root string, maxBytes int64) *Store {
	if maxBytes <= 0 {
		maxBytes = DefaultMaxRawMessageBytes
	}
	return &Store{root: filepath.Clean(root), maxBytes: maxBytes}
}

// Staged is a durable-intent blob. Before Commit, Open reads the temporary
// file. After Commit, Open reads the canonical content-addressed file.
type Staged struct {
	ContentHash string
	StorageKey  string
	ByteSize    int64

	tempPath  string
	finalPath string
	committed bool
}

func (store *Store) Stage(ctx context.Context, reader io.Reader, expectedSize int64) (*Staged, error) {
	if store == nil || strings.TrimSpace(store.root) == "" {
		return nil, errors.New("blob store root is required")
	}
	if reader == nil {
		return nil, errors.New("blob reader is required")
	}
	if expectedSize > store.maxBytes {
		return nil, ErrTooLarge
	}
	stagingRoot := filepath.Join(store.root, ".staging")
	if err := ensurePrivateDir(store.root); err != nil {
		return nil, err
	}
	if err := ensurePrivateDir(stagingRoot); err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(stagingRoot, "raw-*.tmp")
	if err != nil {
		return nil, fmt.Errorf("create staged blob: %w", err)
	}
	tempPath := file.Name()
	cleanup := func(stageErr error) (*Staged, error) {
		_ = file.Close()
		_ = os.Remove(tempPath)
		return nil, stageErr
	}
	if err := file.Chmod(0o600); err != nil {
		return cleanup(fmt.Errorf("secure staged blob: %w", err))
	}

	hash := sha256.New()
	limited := &contextReader{ctx: ctx, reader: reader}
	written, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(limited, store.maxBytes+1))
	if err != nil {
		return cleanup(fmt.Errorf("stage blob: %w", err))
	}
	if written > store.maxBytes {
		return cleanup(ErrTooLarge)
	}
	if err := file.Sync(); err != nil {
		return cleanup(fmt.Errorf("flush staged blob: %w", err))
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(tempPath)
		return nil, fmt.Errorf("close staged blob: %w", err)
	}

	contentHash := hex.EncodeToString(hash.Sum(nil))
	storageKey := filepath.ToSlash(filepath.Join("raw", "sha256", contentHash[:2], contentHash+".eml"))
	return &Staged{
		ContentHash: contentHash,
		StorageKey:  storageKey,
		ByteSize:    written,
		tempPath:    tempPath,
		finalPath:   filepath.Join(store.root, filepath.FromSlash(storageKey)),
	}, nil
}

// OpenVerified opens one canonical blob only after validating its path, size,
// and SHA-256 content address. Callers may then stream the returned file
// without retaining it in memory.
func (store *Store) OpenVerified(storageKey, contentHash string, expectedSize int64) (io.ReadCloser, error) {
	return store.OpenVerifiedContext(context.Background(), storageKey, contentHash, expectedSize)
}

func (store *Store) OpenVerifiedContext(ctx context.Context, storageKey, contentHash string, expectedSize int64) (io.ReadCloser, error) {
	if store == nil || strings.TrimSpace(store.root) == "" {
		return nil, errors.New("blob store root is required")
	}
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	if len(contentHash) != 64 {
		return nil, ErrCorrupt
	}
	path, err := store.canonicalPath(storageKey)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open canonical blob: %w", err)
	}
	cleanup := func(err error) (io.ReadCloser, error) {
		_ = file.Close()
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		return cleanup(fmt.Errorf("inspect canonical blob: %w", err))
	}
	if expectedSize >= 0 && info.Size() != expectedSize {
		return cleanup(ErrCorrupt)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &contextReader{ctx: ctx, reader: file}); err != nil {
		return cleanup(fmt.Errorf("verify canonical blob: %w", err))
	}
	if hex.EncodeToString(hash.Sum(nil)) != contentHash {
		return cleanup(ErrCorrupt)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return cleanup(fmt.Errorf("rewind canonical blob: %w", err))
	}
	return file, nil
}

func (store *Store) canonicalPath(storageKey string) (string, error) {
	storageKey = filepath.ToSlash(strings.TrimSpace(storageKey))
	if storageKey == "" || strings.HasPrefix(storageKey, "/") {
		return "", errors.New("invalid blob storage key")
	}
	cleanKey := filepath.Clean(filepath.FromSlash(storageKey))
	if cleanKey == "." || cleanKey == ".." || strings.HasPrefix(cleanKey, ".."+string(filepath.Separator)) {
		return "", errors.New("invalid blob storage key")
	}
	root, err := filepath.Abs(store.root)
	if err != nil {
		return "", errors.New("invalid blob root")
	}
	path, err := filepath.Abs(filepath.Join(root, cleanKey))
	if err != nil || (path != root && !strings.HasPrefix(path, root+string(filepath.Separator))) {
		return "", errors.New("invalid blob storage key")
	}
	return path, nil
}

func (staged *Staged) Open() (io.ReadCloser, error) {
	if staged == nil {
		return nil, errors.New("staged blob is nil")
	}
	path := staged.tempPath
	if staged.committed {
		path = staged.finalPath
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open staged blob: %w", err)
	}
	return file, nil
}

// Commit installs the staged file at its content-addressed path. Repeating the
// same content is idempotent and never rewrites an existing canonical blob.
func (staged *Staged) Commit() error {
	if staged == nil {
		return errors.New("staged blob is nil")
	}
	if staged.committed {
		return nil
	}
	if err := ensurePrivateDir(filepath.Dir(staged.finalPath)); err != nil {
		return err
	}
	if info, err := os.Stat(staged.finalPath); err == nil {
		if info.Size() != staged.ByteSize {
			return ErrCorrupt
		}
		contentHash, hashErr := hashFile(staged.finalPath)
		if hashErr != nil {
			return hashErr
		}
		if contentHash != staged.ContentHash {
			return ErrCorrupt
		}
		if err := os.Remove(staged.tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("discard duplicate staged blob: %w", err)
		}
		staged.committed = true
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect canonical blob: %w", err)
	}
	if err := os.Rename(staged.tempPath, staged.finalPath); err != nil {
		return fmt.Errorf("commit staged blob: %w", err)
	}
	if err := os.Chmod(staged.finalPath, 0o600); err != nil {
		return fmt.Errorf("secure canonical blob: %w", err)
	}
	staged.committed = true
	return nil
}

func (staged *Staged) Abort() error {
	if staged == nil || staged.committed || strings.TrimSpace(staged.tempPath) == "" {
		return nil
	}
	if err := os.Remove(staged.tempPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove staged blob: %w", err)
	}
	return nil
}

func hashFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open canonical blob for verification: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("verify canonical blob: %w", err)
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create blob directory: %w", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		return fmt.Errorf("secure blob directory: %w", err)
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *contextReader) Read(buffer []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(buffer)
	}
}
