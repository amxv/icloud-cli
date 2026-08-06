package app

import (
	"context"
	"errors"
	"io"

	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

type attachmentBlobStore interface {
	StageAttachment(context.Context, io.Reader, int64) (*blob.Staged, error)
	OpenVerifiedContext(context.Context, string, string, int64) (io.ReadCloser, error)
}

type stagedAttachmentSet struct {
	store  attachmentBlobStore
	staged []*blob.Staged
}

func (commands accountCommands) attachmentBlobStore(paths config.AppPaths) attachmentBlobStore {
	if commands.attachmentBlobs != nil {
		return commands.attachmentBlobs(paths)
	}
	return blob.New(paths.BlobRoot)
}

func newStagedAttachmentSet(store attachmentBlobStore) *stagedAttachmentSet {
	return &stagedAttachmentSet{store: store}
}

func (set *stagedAttachmentSet) Sink(ctx context.Context, _ maildomain.MIMEPart, reader io.Reader) (maildomain.AttachmentBlob, error) {
	if set == nil || set.store == nil {
		return maildomain.AttachmentBlob{}, errors.New("attachment blob store is unavailable")
	}
	staged, err := set.store.StageAttachment(ctx, reader, -1)
	if err != nil {
		return maildomain.AttachmentBlob{}, err
	}
	set.staged = append(set.staged, staged)
	return maildomain.AttachmentBlob{
		ContentHash: staged.ContentHash,
		StorageKey:  staged.StorageKey,
		ByteSize:    staged.ByteSize,
		Open:        staged.Open,
	}, nil
}

func (set *stagedAttachmentSet) Commit() error {
	if set == nil {
		return nil
	}
	for _, staged := range set.staged {
		if err := staged.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (set *stagedAttachmentSet) Abort() {
	if set == nil {
		return
	}
	for _, staged := range set.staged {
		_ = staged.Abort()
	}
}

func assignContentIDs(commands accountCommands, messageID string, content *maildomain.NormalizedContent) error {
	if content == nil {
		return nil
	}
	partIDs := make(map[string]string, len(content.Parts))
	for index := range content.Parts {
		partID, err := commands.generateID("prt")
		if err != nil {
			return err
		}
		content.Parts[index].ID = partID
		partIDs[content.Parts[index].Path] = partID
	}
	for index := range content.Attachments {
		attachmentID, err := commands.generateID("att")
		if err != nil {
			return err
		}
		content.Attachments[index].ID = attachmentID
		content.Attachments[index].MessageID = messageID
		content.Attachments[index].PartID = partIDs[content.Attachments[index].PartPath]
	}
	return nil
}
