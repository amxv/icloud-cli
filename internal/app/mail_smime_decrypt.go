package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) MailMessageDecrypt(ctx context.Context, home, accountSelector, messageID string) (account.Account, maildomain.Message, maildomain.ContentSource, int64, error) {
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, 0, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve the iCloud application data directory", false, err)
	}
	state, err := openState(ctx, home)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, 0, err
	}
	defer state.Close()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapAccountError(err)
	}
	messageValue, err := state.Mail().Message(ctx, accountValue.ID, strings.TrimSpace(messageID))
	if err != nil {
		if errors.Is(err, maildomain.ErrMessageNotFound) {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorNotFound, "message not found", false, err)
		}
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read the local message", false, err)
	}
	rawSource, ok := rawMessageSource(messageValue)
	if !ok {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorUnavailable, "raw encrypted message content is not available locally", false)
	}
	blobStore := commands.inboundBlobStore(paths)
	rawReader, err := blobStore.OpenVerifiedContext(ctx, rawSource.StorageKey, rawSource.ContentHash, rawSource.ByteSize)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapBlobReadError(err)
	}
	envelope, envelopeErr := maildomain.ParseSMIMEEnvelopeContext(ctx, rawReader)
	closeErr := rawReader.Close()
	if envelopeErr == nil {
		envelopeErr = closeErr
	}
	if envelopeErr != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEDecryptError(envelopeErr)
	}
	materials, err := state.SMIMEIdentities().Materials(ctx, accountValue.ID)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEIdentityError(err)
	}
	matches := make([]store.SMIMEIdentityMaterial, 0, 2)
	for _, material := range materials {
		matched, matchErr := envelope.Matches(material.Certificate)
		if matchErr != nil {
			return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEDecryptError(matchErr)
		}
		if matched {
			matches = append(matches, material)
		}
	}
	if len(matches) == 0 {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConfiguration, "no configured S/MIME identity matches this encrypted message recipient", false)
	}
	if len(matches) != 1 {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "multiple configured S/MIME identities match this encrypted message; remove the duplicate certificate binding", false)
	}
	selected := matches[0]
	manager := secret.NewManager(commands.keyring)
	var keyBytes []byte
	if selected.Identity.PrivateKeyScheme == account.CredentialFile {
		keyBytes, err = readSMIMEPrivateKeyFile(ctx, strings.TrimPrefix(selected.Identity.PrivateKeyReference, "file:"))
	} else {
		keyBytes, err = manager.Resolve(ctx, account.Credential{Scheme: selected.Identity.PrivateKeyScheme, Reference: selected.Identity.PrivateKeyReference, Configured: true})
	}
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	defer zeroBytes(keyBytes)
	privateKey, err := maildomain.ParseSMIMEPrivateKey(keyBytes)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEPrivateKeyError(err)
	}
	if !maildomain.SMIMEPrivateKeyMatchesCertificate(privateKey, selected.Certificate) {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEPrivateKeyError(maildomain.ErrSMIMEPrivateKeyMismatch)
	}
	plaintext, err := envelope.Decrypt(ctx, selected.Certificate, privateKey, paths.InboundLimits.RawMessageBytes)
	if err != nil {
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEDecryptError(err)
	}
	policy, err := commands.inboundSMIMEPolicy(ctx, state, accountValue.ID)
	if err != nil {
		zeroBytes(plaintext)
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot read local S/MIME trust state", false, err)
	}
	policyCopy := *policy
	policyCopy.ExpectedSignerEmail = messageValue.From.Email
	decrypted, err := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(ctx, bytes.NewReader(plaintext), memorySMIMEAttachmentSink(paths.InboundLimits.AttachmentBytes), &policyCopy)
	if err != nil {
		zeroBytes(plaintext)
		return account.Account{}, maildomain.Message{}, nil, state.SchemaVersion(), mapSMIMEDecryptError(err)
	}
	decryption := &maildomain.SMIMEDecryptionStatus{
		Status: "decrypted", SMIMEIdentityID: selected.Identity.ID, MailIdentityID: selected.Identity.MailIdentityID,
		CertificateID: selected.Identity.CertificateID, ContentAvailable: true,
	}
	if decrypted.SMIME == nil || (decrypted.SMIME.Status == maildomain.SMIMEStatusAbsent && !decrypted.SMIME.Signed) {
		status := maildomain.NewSMIMEEncryptedStatus("0")
		decrypted.SMIME = &status
	}
	decrypted.SMIME.Encrypted = true
	decrypted.SMIME.Decryption = decryption
	messageValue.Content = &decrypted
	messageValue.AttachmentCount = decrypted.Preview.AttachmentCount
	if strings.TrimSpace(decrypted.Preview.Snippet) != "" {
		messageValue.Snippet = decrypted.Preview.Snippet
	}
	source := &messageContentSource{store: blobStore, raw: rawSource, content: decrypted, bodyRaw: plaintext}
	return accountValue, messageValue, source, state.SchemaVersion(), nil
}

func memorySMIMEAttachmentSink(maximum int64) maildomain.AttachmentSink {
	return func(ctx context.Context, part maildomain.MIMEPart, reader io.Reader) (maildomain.AttachmentBlob, error) {
		if maximum <= 0 {
			maximum = blob.DefaultMaxAttachmentBytes
		}
		if !part.EmbeddedMessage {
			digest := sha256.New()
			count, err := io.Copy(digest, io.LimitReader(&appContextReader{ctx: ctx, reader: reader}, maximum+1))
			if err != nil {
				return maildomain.AttachmentBlob{}, err
			}
			if count > maximum {
				return maildomain.AttachmentBlob{}, maildomain.ErrContentLimit
			}
			hash := hex.EncodeToString(digest.Sum(nil))
			return maildomain.AttachmentBlob{ContentHash: hash, StorageKey: "memory:" + hash, ByteSize: count}, nil
		}
		value, err := io.ReadAll(io.LimitReader(&appContextReader{ctx: ctx, reader: reader}, maximum+1))
		if err != nil {
			return maildomain.AttachmentBlob{}, err
		}
		if int64(len(value)) > maximum {
			zeroBytes(value)
			return maildomain.AttachmentBlob{}, maildomain.ErrContentLimit
		}
		sum := sha256.Sum256(value)
		hash := hex.EncodeToString(sum[:])
		return maildomain.AttachmentBlob{
			ContentHash: hash, StorageKey: "memory:" + hash, ByteSize: int64(len(value)),
			Open: func() (io.ReadCloser, error) { return io.NopCloser(bytes.NewReader(value)), nil },
		}, nil
	}
}

func mapSMIMEDecryptError(err error) error {
	switch {
	case errors.Is(err, context.Canceled):
		return cli.WrapAppError(cli.ErrorUnavailable, "S/MIME decryption was canceled", false, err)
	case errors.Is(err, context.DeadlineExceeded):
		return cli.WrapAppError(cli.ErrorUnavailable, "S/MIME decryption timed out", true, err)
	case errors.Is(err, maildomain.ErrContentLimit):
		return cli.WrapAppError(cli.ErrorSafety, "decrypted S/MIME content exceeds the configured inbound content limit", false, err)
	case errors.Is(err, maildomain.ErrSMIMERecipientNotFound):
		return cli.WrapAppError(cli.ErrorConfiguration, "configured S/MIME certificate is not a recipient of this encrypted message", false, err)
	case errors.Is(err, maildomain.ErrSMIMEDecryptUnsupported):
		return cli.WrapAppError(cli.ErrorUnavailable, "this S/MIME encryption algorithm is not supported by the current local crypto implementation", false, err)
	case errors.Is(err, maildomain.ErrSMIMEEnvelopeInvalid), errors.Is(err, maildomain.ErrInvalidMessage), errors.Is(err, maildomain.ErrAttachmentStorage):
		return cli.WrapAppError(cli.ErrorUnavailable, "encrypted S/MIME content cannot be decoded safely", false, err)
	default:
		return cli.WrapAppError(cli.ErrorUnavailable, "S/MIME decryption failed", false, err)
	}
}
