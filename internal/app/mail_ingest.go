package app

import (
	"context"
	"errors"
	"fmt"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

type fetchedPreparationError struct {
	code      string
	message   string
	retryable bool
	err       error
}

func (value *fetchedPreparationError) Error() string {
	if value == nil {
		return ""
	}
	return value.message
}

func (value *fetchedPreparationError) Unwrap() error {
	if value == nil {
		return nil
	}
	return value.err
}

func (value *fetchedPreparationError) appError() error {
	if value == nil {
		return nil
	}
	code := cli.ErrorUnavailable
	if value.code == "raw_message_missing" || value.code == "blob_open" || value.code == "id_allocate" {
		code = cli.ErrorInternal
	}
	if errors.Is(value.err, context.Canceled) {
		return cli.WrapAppError(cli.ErrorUnavailable, "mail message parsing was canceled", false, value.err)
	}
	if errors.Is(value.err, context.DeadlineExceeded) {
		return cli.WrapAppError(cli.ErrorUnavailable, "mail message parsing timed out", true, value.err)
	}
	return cli.WrapAppError(code, value.message, value.retryable, value.err)
}

func (commands accountCommands) prepareFetchedMailRecords(
	ctx context.Context,
	state *store.Store,
	accountValue account.Account,
	folderValue folder.Folder,
	uidValidity uint32,
	messages []transport.FetchedMessage,
	attachmentSet *stagedAttachmentSet,
) ([]maildomain.IngestRecord, error) {
	selfAddresses, err := enabledMailIdentityAddresses(ctx, state, accountValue.ID)
	if err != nil {
		return nil, &fetchedPreparationError{code: "identity_state", message: "cannot classify mail direction from enabled identities", err: err}
	}
	self := mailIdentityAddressSet(selfAddresses)
	semanticKind, err := semanticFolderKind(ctx, state, accountValue.ID, folderValue)
	if err != nil {
		return nil, &fetchedPreparationError{code: "folder_role_state", message: "cannot classify mail from semantic folder roles", err: err}
	}
	classificationFolder := folderValue
	classificationFolder.Kind = semanticKind
	smimePolicy, err := commands.inboundSMIMEPolicy(ctx, state, accountValue.ID)
	if err != nil {
		return nil, &fetchedPreparationError{code: "smime_state", message: "cannot read local S/MIME trust state", err: err}
	}
	records := make([]maildomain.IngestRecord, 0, len(messages))
	for _, fetched := range messages {
		if fetched.Raw == nil {
			return nil, &fetchedPreparationError{code: "raw_message_missing", message: "iCloud returned a message without durable raw content"}
		}
		reader, err := fetched.Raw.Open()
		if err != nil {
			return nil, &fetchedPreparationError{code: "blob_open", message: "cannot read a staged mail message", err: err}
		}
		normalized, parseErr := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(ctx, reader, attachmentSet.Sink, smimePolicy)
		closeErr := reader.Close()
		if parseErr == nil {
			parseErr = closeErr
		}
		if parseErr != nil {
			code := "message_parse"
			message := "cannot safely normalize a mail message"
			if errors.Is(parseErr, maildomain.ErrAttachmentStorage) {
				code = "attachment_blob_stage"
				message = "cannot safely stage a decoded mail attachment"
			}
			return nil, &fetchedPreparationError{code: code, message: message, err: parseErr}
		}
		messageID, err := commands.generateID("msg")
		if err != nil {
			return nil, &fetchedPreparationError{code: "id_allocate", message: "cannot allocate a message ID", err: err}
		}
		locationID, err := commands.generateID("loc")
		if err != nil {
			return nil, &fetchedPreparationError{code: "id_allocate", message: "cannot allocate a message-location ID", err: err}
		}
		if err := assignContentIDs(commands, messageID, &normalized); err != nil {
			return nil, &fetchedPreparationError{code: "id_allocate", message: "cannot allocate message-content IDs", err: err}
		}
		record := maildomain.BuildIngestRecord(
			messageID, locationID, accountValue.ID, folderValue.ID, uidValidity, fetched.UID,
			fetched.Flags, fetched.InternalDate, fetched.RFC822Size,
			fetched.Raw.ContentHash, fetched.Raw.StorageKey, fetched.Raw.ByteSize, normalized.Preview,
		)
		record.Content = &normalized
		record.ModSequence = fetched.ModSequence
		record.Direction = messageDirection(self, classificationFolder, normalized.Preview, maildomain.ParseLocationFlags(fetched.Flags))
		record.ContentFingerprint = maildomain.BuildContentFingerprint(
			accountValue.ID, normalized.Preview, fetched.InternalDate, normalized.Attachments,
		)
		if record.UID == 0 {
			return nil, &fetchedPreparationError{code: "message_identity", message: "iCloud returned a message without a provider UID", err: fmt.Errorf("missing UID")}
		}
		records = append(records, record)
	}
	return records, nil
}
