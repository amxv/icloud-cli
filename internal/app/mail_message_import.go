package app

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/blob"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
)

const maxImportReconciliationUIDs uint32 = 256

type preparedMessageImport struct {
	account account.Account
	folder  folder.Folder
	plan    maildomain.MessageImportPlan
	raw     *blob.Staged
}

type importProviderRawMatcher struct {
	exactHash        string
	exactSize        int64
	terminalCRLFHash string
	terminalCRLFSize int64
}

func (matcher importProviderRawMatcher) match(contentHash string, byteSize int64) (string, bool) {
	contentHash = strings.ToLower(strings.TrimSpace(contentHash))
	switch {
	case byteSize == matcher.exactSize && contentHash == matcher.exactHash:
		return "exact", true
	case byteSize == matcher.terminalCRLFSize && contentHash == matcher.terminalCRLFHash:
		return "terminal_crlf", true
	default:
		return "", false
	}
}

func (prepared *preparedMessageImport) abort() {
	if prepared != nil && prepared.raw != nil {
		_ = prepared.raw.Abort()
	}
}

func (commands accountCommands) MailMessageImportPreview(ctx context.Context, home, accountSelector, sourcePath string, request maildomain.MessageImportRequest) (account.Account, folder.Folder, maildomain.MessageImportPlan, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageImportPlan{}, 0, err
	}
	defer state.Close()
	defer unlock()
	prepared, err := commands.prepareMessageImport(ctx, state, paths, accountSelector, sourcePath, request, false)
	if prepared != nil {
		defer prepared.abort()
	}
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageImportPlan{}, state.SchemaVersion(), err
	}
	return prepared.account, prepared.folder, prepared.plan, state.SchemaVersion(), nil
}

func (commands accountCommands) MailMessageImport(ctx context.Context, home, accountSelector, sourcePath string, request maildomain.MessageImportRequest) (account.Account, folder.Folder, maildomain.MessageImportPlan, maildomain.MessageImport, maildomain.Message, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageImportPlan{}, maildomain.MessageImport{}, maildomain.Message{}, 0, err
	}
	defer state.Close()
	defer unlock()
	prepared, err := commands.prepareMessageImport(ctx, state, paths, accountSelector, sourcePath, request, true)
	if prepared != nil {
		defer prepared.abort()
	}
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MessageImportPlan{}, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), err
	}

	var importValue maildomain.MessageImport
	existing, lookupErr := state.MessageImports().ByIdempotency(ctx, prepared.account.ID, prepared.plan.IdempotencyKey)
	switch {
	case lookupErr == nil:
		if existing.RequestHash != prepared.plan.RequestHash || existing.FolderID != prepared.folder.ID || !strings.EqualFold(existing.RawContentHash, prepared.plan.RawContentHash) || existing.RawByteSize != prepared.plan.RawByteSize {
			return prepared.account, prepared.folder, prepared.plan, existing, maildomain.Message{}, state.SchemaVersion(), mapMessageImportStoreError(maildomain.ErrMessageImportConflict)
		}
		importValue = existing
		durableDate := existing.InternalDate.UTC()
		prepared.plan.InternalDate = &durableDate
		_ = prepared.raw.Abort()
		prepared.raw = nil
	case errors.Is(lookupErr, maildomain.ErrMessageImportNotFound):
		_, credential, credentialErr := state.Accounts().Credential(ctx, prepared.account.ID)
		if credentialErr != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), mapAccountError(credentialErr)
		}
		password, credentialErr := secret.NewManager(commands.keyring).Resolve(ctx, credential)
		if credentialErr != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), mapCredentialError(credentialErr)
		}
		status, _, statusErr := commands.fetchMailboxStatus(ctx, prepared.account, password, prepared.folder.Name)
		zeroBytes(password)
		if statusErr != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), mapMailboxTransportError(statusErr, prepared.folder.Name)
		}
		if status.UIDValidity == 0 || status.UIDNext == 0 || (prepared.folder.UIDValidity != 0 && prepared.folder.UIDValidity != status.UIDValidity) {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "destination mailbox identity changed before import; refresh folders and retry", false)
		}
		if err := prepared.raw.Commit(); err != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot commit the validated import source", true, err)
		}
		importID, idErr := commands.generateID("imp")
		if idErr != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a message import ID", false, idErr)
		}
		importValue, _, err = state.MessageImports().Begin(ctx, maildomain.BeginMessageImport{
			ImportID: importID, AccountID: prepared.account.ID, FolderID: prepared.folder.ID,
			RawContentHash: prepared.raw.ContentHash, RawStorageKey: prepared.raw.StorageKey, RawByteSize: prepared.raw.ByteSize,
			RFCMessageID: prepared.plan.RFCMessageID, InternalDate: resolvedImportInternalDate(prepared.plan, commands.timeNow()), Flags: prepared.plan.Flags,
			BaselineUIDValidity: status.UIDValidity, BaselineUIDNext: status.UIDNext,
			RequestHash: prepared.plan.RequestHash, IdempotencyKey: prepared.plan.IdempotencyKey, Now: commands.timeNow(),
		})
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), mapMessageImportStoreError(err)
		}
		prepared.raw = nil
	default:
		return prepared.account, prepared.folder, prepared.plan, maildomain.MessageImport{}, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect prior message import recovery state", false, lookupErr)
	}

	if importValue.Status == maildomain.MessageImportConfirmed {
		message, readErr := state.Mail().Message(ctx, prepared.account.ID, importValue.MessageID)
		if readErr != nil {
			return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot reload the confirmed imported message", false, readErr)
		}
		location := providerLocation(message, prepared.folder.ID, importValue.ProviderUIDValidity, importValue.ProviderUID)
		matcher, matchErr := commands.importProviderRawMatcher(ctx, paths, importValue)
		if matchErr != nil {
			return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), matchErr
		}
		variant, matched := matcher.match(location.RawContentHash, location.RawByteSize)
		if location.ID == "" || !matched {
			return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "confirmed provider import no longer matches the reviewed source bytes or the exact iCloud terminal-CRLF normalization", false)
		}
		importValue.ProviderRawVariant = variant
		return prepared.account, prepared.folder, prepared.plan, importValue, message, state.SchemaVersion(), nil
	}
	if importValue.Status == maildomain.MessageImportFailed {
		return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(cli.NewAppError(cli.ErrorConflict, "the prior import failed before provider acceptance; use a new idempotency key for a new deliberate import", false), importValue)
	}

	_, credential, err := state.Accounts().Credential(ctx, prepared.account.ID)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(ctx, credential)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), mapCredentialError(err)
	}
	defer zeroBytes(password)

	if importValue.Status == maildomain.MessageImportPending {
		importValue, err = state.MessageImports().MarkAppendUnresolved(ctx, importValue.ID, "append_inflight", commands.timeNow())
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot fence import APPEND before provider contact", false, err)
		}
		appendResult, appendErr := commands.appendImportedMessage(ctx, paths, prepared.account, password, prepared.folder, importValue)
		if appendErr != nil {
			if definitelyBeforeRewriteAppend(appendErr) {
				importValue, _ = state.MessageImports().MarkFailed(ctx, importValue.ID, messageRewriteErrorCode(appendErr), commands.timeNow())
				return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(mapMailboxTransportError(appendErr, prepared.folder.Name), importValue)
			}
			if reconciled, reconcileErr := commands.reconcileMessageImportAppend(ctx, state, paths, prepared.account, password, prepared.folder, importValue); reconcileErr == nil {
				importValue = reconciled
			} else {
				return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(cli.WrapAppError(cli.ErrorUnavailable, "import APPEND may have been accepted; retry the same idempotency key so the destination is reconciled before any new APPEND", false, appendErr), importValue)
			}
		} else if appendResult.UIDValidity != 0 && appendResult.UID != 0 {
			importValue, err = state.MessageImports().ConfirmAppend(ctx, importValue.ID, appendResult.UIDValidity, appendResult.UID, "append_uid", commands.timeNow())
			if err != nil {
				return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(cli.WrapAppError(cli.ErrorInternal, "import was appended but its provider mapping could not be persisted; retry the same idempotency key", true, err), importValue)
			}
		} else {
			importValue, err = commands.reconcileMessageImportAppend(ctx, state, paths, prepared.account, password, prepared.folder, importValue)
			if err != nil {
				return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(err, importValue)
			}
		}
	} else if importValue.Status == maildomain.MessageImportAppendUnresolved {
		importValue, err = commands.reconcileMessageImportAppend(ctx, state, paths, prepared.account, password, prepared.folder, importValue)
		if err != nil {
			return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(err, importValue)
		}
	}

	if importValue.Status != maildomain.MessageImportAppended {
		return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(cli.NewAppError(cli.ErrorUnavailable, "message import has not reached a proven provider copy", true), importValue)
	}
	message, location, providerRawVariant, err := commands.hydrateImportedProviderCopy(ctx, state, paths, prepared.account, password, prepared.folder, importValue)
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, importValue, maildomain.Message{}, state.SchemaVersion(), withMessageImportDetails(err, importValue)
	}
	importValue, err = state.MessageImports().ConfirmIngest(ctx, importValue.ID, message.ID, location.ID, location.RawContentHash, location.RawByteSize, commands.timeNow())
	if err != nil {
		return prepared.account, prepared.folder, prepared.plan, importValue, message, state.SchemaVersion(), withMessageImportDetails(cli.WrapAppError(cli.ErrorInternal, "provider import is canonical but recovery finalization failed; retry the same idempotency key", true, err), importValue)
	}
	importValue.ProviderRawVariant = providerRawVariant
	return prepared.account, prepared.folder, prepared.plan, importValue, message, state.SchemaVersion(), nil
}

func (commands accountCommands) prepareMessageImport(ctx context.Context, state *store.Store, paths config.AppPaths, accountSelector, sourcePath string, request maildomain.MessageImportRequest, requireKey bool) (*preparedMessageImport, error) {
	request.Folder = strings.TrimSpace(request.Folder)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	if request.Folder == "" {
		return nil, cli.NewAppError(cli.ErrorUsage, "--to destination folder is required", false)
	}
	if requireKey && request.IdempotencyKey == "" {
		return nil, cli.NewAppError(cli.ErrorUsage, "--idempotency-key is required for message import", false)
	}
	if len(request.IdempotencyKey) > 256 {
		return nil, cli.NewAppError(cli.ErrorUsage, "--idempotency-key exceeds 256 characters", false)
	}
	flags, err := maildomain.NormalizeImportFlags(request.Flags)
	if err != nil {
		return nil, cli.NewAppError(cli.ErrorUsage, "import flags support only seen, flagged, and answered", false)
	}
	if request.InternalDate != nil && request.InternalDate.IsZero() {
		return nil, cli.NewAppError(cli.ErrorUsage, "import internal date must be a valid RFC3339 timestamp", false)
	}
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return nil, mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, request.Folder)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(sourcePath)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorNotFound, "import source file is unavailable", false, err)
	}
	if !info.Mode().IsRegular() {
		return nil, cli.NewAppError(cli.ErrorSafety, "import source must be one regular .eml file", false)
	}
	file, err := os.Open(sourcePath)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot open import source file", false, err)
	}
	openedInfo, statErr := file.Stat()
	if statErr != nil || !openedInfo.Mode().IsRegular() || openedInfo.Size() != info.Size() || !os.SameFile(info, openedInfo) {
		_ = file.Close()
		return nil, cli.WrapAppError(cli.ErrorSafety, "import source changed while it was being reviewed", false, statErr)
	}
	staged, stageErr := commands.blobStager(paths).Stage(ctx, file, openedInfo.Size())
	closeErr := file.Close()
	if stageErr == nil {
		stageErr = closeErr
	}
	if stageErr != nil {
		if staged != nil {
			_ = staged.Abort()
		}
		if errors.Is(stageErr, blob.ErrTooLarge) {
			return nil, cli.WrapAppError(cli.ErrorSafety, "import source exceeds the configured raw-message limit", false, stageErr)
		}
		if errors.Is(stageErr, context.Canceled) || errors.Is(stageErr, context.DeadlineExceeded) {
			return nil, cli.WrapAppError(cli.ErrorUnavailable, "message import validation was canceled", true, stageErr)
		}
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot stage import source", false, stageErr)
	}
	if staged.ByteSize != openedInfo.Size() {
		_ = staged.Abort()
		return nil, cli.NewAppError(cli.ErrorSafety, "import source changed size while it was being staged", false)
	}
	prepared := &preparedMessageImport{account: accountValue, folder: folderValue, raw: staged}
	reader, err := staged.Open()
	if err != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot reopen staged import source", false, err)
	}
	smimePolicy, policyErr := commands.inboundSMIMEPolicy(ctx, state, accountValue.ID)
	if policyErr != nil {
		_ = reader.Close()
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot read local S/MIME trust state", false, policyErr)
	}
	content, parseErr := maildomain.ParseNormalizedWithAttachmentsAndSMIMEContext(ctx, reader, nil, smimePolicy)
	closeErr = reader.Close()
	if parseErr == nil {
		parseErr = closeErr
	}
	if parseErr != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorUsage, "import source is not one parseable RFC/MIME message", false, parseErr)
	}
	resolvedDate := commands.timeNow().UTC()
	if content.Preview.HeaderDate != nil {
		resolvedDate = content.Preview.HeaderDate.UTC()
	}
	if request.InternalDate != nil {
		resolvedDate = request.InternalDate.UTC()
	}
	prepared.plan = maildomain.MessageImportPlan{
		FolderID: folderValue.ID, FolderName: folderValue.Name, RawContentHash: staged.ContentHash, RawByteSize: staged.ByteSize,
		RFCMessageID: content.Preview.RFCMessageID, Subject: content.Preview.Subject, AttachmentCount: content.Preview.AttachmentCount,
		Flags: flags, InternalDate: &resolvedDate, IdempotencyKey: request.IdempotencyKey,
	}
	prepared.plan.RequestHash, err = maildomain.MessageImportHash(accountValue.ID, prepared.plan, request.InternalDate != nil)
	if err != nil {
		prepared.abort()
		return nil, cli.WrapAppError(cli.ErrorInternal, "cannot fingerprint the import request", false, err)
	}
	return prepared, nil
}

func resolvedImportInternalDate(plan maildomain.MessageImportPlan, fallback time.Time) time.Time {
	if plan.InternalDate != nil && !plan.InternalDate.IsZero() {
		return plan.InternalDate.UTC()
	}
	return fallback.UTC()
}

func (commands accountCommands) appendImportedMessage(ctx context.Context, paths config.AppPaths, accountValue account.Account, password []byte, folderValue folder.Folder, value maildomain.MessageImport) (transport.MailboxAppendResult, error) {
	var result transport.MailboxAppendResult
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		reader, err := commands.inboundBlobStore(paths).OpenVerifiedContext(ctx, value.RawStorageKey, value.RawContentHash, value.RawByteSize)
		if err != nil {
			return transport.MailboxAppendResult{}, mapBlobReadError(err)
		}
		result, lastErr = commands.mailboxAppender().AppendMailbox(ctx, transport.AuthRequest{Username: username, Password: password}, transport.MailboxAppendRequest{
			Mailbox: folderValue.Name, Message: reader, Size: value.RawByteSize, InternalDate: value.InternalDate, Flags: append([]string(nil), value.Flags...),
		})
		closeErr := reader.Close()
		if lastErr == nil {
			lastErr = closeErr
		}
		if lastErr == nil {
			return result, nil
		}
		if failure, ok := transport.AsFailure(lastErr); !ok || failure.Kind != transport.FailureAuthentication {
			break
		}
	}
	return result, lastErr
}

func (commands accountCommands) reconcileMessageImportAppend(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, password []byte, folderValue folder.Folder, value maildomain.MessageImport) (maildomain.MessageImport, error) {
	status, _, err := commands.fetchMailboxStatus(ctx, accountValue, password, folderValue.Name)
	if err != nil {
		return value, cli.WrapAppError(cli.ErrorUnavailable, "import APPEND needs reconciliation but destination status is unavailable", true, err)
	}
	if status.UIDValidity != value.BaselineUIDValidity {
		return value, cli.NewAppError(cli.ErrorConflict, "import APPEND is unresolved because destination UIDVALIDITY changed", false)
	}
	upper := value.CandidateUIDUpper
	if upper == 0 {
		if status.UIDNext <= value.BaselineUIDNext {
			return value, cli.NewAppError(cli.ErrorUnavailable, "import APPEND is unresolved; no post-fence provider UID is visible yet", true)
		}
		upper = status.UIDNext - 1
		value, err = state.MessageImports().SetCandidateUIDUpper(ctx, value.ID, upper, commands.timeNow())
		if err != nil {
			return value, cli.WrapAppError(cli.ErrorInternal, "cannot persist the import reconciliation bound", true, err)
		}
	}
	if upper < value.BaselineUIDNext || upper-value.BaselineUIDNext+1 > maxImportReconciliationUIDs {
		return value, cli.NewAppError(cli.ErrorConflict, fmt.Sprintf("import APPEND is unresolved; the exact post-fence candidate range exceeds the %d-UID reconciliation bound", maxImportReconciliationUIDs), false)
	}
	matcher, err := commands.importProviderRawMatcher(ctx, paths, value)
	if err != nil {
		return value, err
	}
	matching := make([]transport.RemoteFetchResult, 0, 2)
	for uid := upper; ; uid-- {
		result, fetchErr := commands.fetchImportCandidate(ctx, accountValue, password, folderValue, value.BaselineUIDValidity, uid, commands.blobStager(paths))
		if fetchErr == nil {
			if result.Message.Raw != nil {
				_, matched := matcher.match(result.Message.Raw.ContentHash, result.Message.Raw.ByteSize)
				if matched {
					matching = append(matching, result)
				} else {
					_ = result.Message.Raw.Abort()
				}
			}
		} else if !errors.Is(fetchErr, transport.ErrRemoteMessageUnavailable) {
			for _, candidate := range matching {
				if candidate.Message.Raw != nil {
					_ = candidate.Message.Raw.Abort()
				}
			}
			return value, mapRemoteTransportError(fetchErr, "import reconciliation fetch", folderValue.Name)
		}
		if uid == value.BaselineUIDNext {
			break
		}
	}
	if len(matching) != 1 {
		for _, candidate := range matching {
			if candidate.Message.Raw != nil {
				_ = candidate.Message.Raw.Abort()
			}
		}
		if len(matching) == 0 {
			return value, cli.NewAppError(cli.ErrorUnavailable, "import APPEND is unresolved; no provider copy matching the reviewed source or exact terminal-CRLF normalization exists in the fenced candidate range", true)
		}
		return value, cli.NewAppError(cli.ErrorConflict, "import APPEND is unresolved because multiple provider copies match the reviewed source bytes in the fenced candidate range", false)
	}
	match := matching[0]
	if match.Message.Raw != nil {
		_ = match.Message.Raw.Abort()
	}
	return state.MessageImports().ConfirmAppend(ctx, value.ID, match.UIDValidity, match.Message.UID, "exact_raw_reconcile", commands.timeNow())
}

func (commands accountCommands) fetchImportCandidate(ctx context.Context, accountValue account.Account, password []byte, folderValue folder.Folder, uidValidity, uid uint32, stager blob.Stager) (transport.RemoteFetchResult, error) {
	var lastErr error
	for _, username := range imapUsernameCandidates(accountValue) {
		result, err := commands.remoteClient().FetchRemoteMessage(ctx, transport.AuthRequest{Username: username, Password: password}, transport.RemoteFetchRequest{
			Mailbox: folderValue.Name, ExpectedUIDValidity: uidValidity, UID: uid,
		}, stager)
		if err == nil {
			return result, nil
		}
		lastErr = err
		if !isAuthenticationFailure(err) {
			break
		}
	}
	return transport.RemoteFetchResult{}, lastErr
}

func (commands accountCommands) hydrateImportedProviderCopy(ctx context.Context, state *store.Store, paths config.AppPaths, accountValue account.Account, password []byte, folderValue folder.Folder, value maildomain.MessageImport) (maildomain.Message, maildomain.Location, string, error) {
	if value.ProviderUIDValidity == 0 || value.ProviderUID == 0 {
		return maildomain.Message{}, maildomain.Location{}, "", cli.NewAppError(cli.ErrorConflict, "import provider identity is incomplete", false)
	}
	matcher, err := commands.importProviderRawMatcher(ctx, paths, value)
	if err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", err
	}
	if existing, err := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, value.ProviderUIDValidity, value.ProviderUID); err == nil {
		location := providerLocation(existing, folderValue.ID, value.ProviderUIDValidity, value.ProviderUID)
		variant, matched := matcher.match(location.RawContentHash, location.RawByteSize)
		if location.ID == "" || !matched {
			return maildomain.Message{}, maildomain.Location{}, "", cli.NewAppError(cli.ErrorConflict, "cached provider import differs from the reviewed source beyond the exact iCloud terminal-CRLF normalization", false)
		}
		return existing, location, variant, nil
	} else if !errors.Is(err, maildomain.ErrMessageNotFound) {
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot inspect canonical import state", false, err)
	}
	result, err := commands.fetchImportCandidate(ctx, accountValue, password, folderValue, value.ProviderUIDValidity, value.ProviderUID, commands.blobStager(paths))
	if err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", mapRemoteTransportError(err, "import provider fetch", folderValue.Name)
	}
	if result.Message.Raw == nil {
		return maildomain.Message{}, maildomain.Location{}, "", cli.NewAppError(cli.ErrorUnavailable, "provider import returned without raw content", true)
	}
	defer result.Message.Raw.Abort()
	providerRawVariant, matched := matcher.match(result.Message.Raw.ContentHash, result.Message.Raw.ByteSize)
	if !matched {
		return maildomain.Message{}, maildomain.Location{}, "", cli.NewAppError(cli.ErrorConflict, "provider import raw content differs from the reviewed source beyond the exact iCloud terminal-CRLF normalization", false)
	}
	attachmentSet := newStagedAttachmentSet(commands.attachmentBlobStore(paths))
	defer attachmentSet.Abort()
	records, prepareErr := commands.prepareFetchedMailRecords(ctx, state, accountValue, folderValue, result.UIDValidity, []transport.FetchedMessage{result.Message}, attachmentSet)
	if prepareErr != nil {
		var preparation *fetchedPreparationError
		if errors.As(prepareErr, &preparation) {
			return maildomain.Message{}, maildomain.Location{}, "", preparation.appError()
		}
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorUnavailable, "cannot canonically prepare imported provider message", false, prepareErr)
	}
	if err := result.Message.Raw.Commit(); err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot commit imported provider raw content", true, err)
	}
	if err := attachmentSet.Commit(); err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot commit imported provider attachment content", true, err)
	}
	if _, err := state.Mail().CommitHydration(ctx, maildomain.HydratePage{
		AccountID: accountValue.ID, FolderID: folderValue.ID, UIDValidity: result.UIDValidity,
		AttemptedAt: commands.timeNow(), Records: records,
	}); err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot atomically commit imported provider message", true, err)
	}
	message, err := state.Mail().MessageByProviderLocation(ctx, accountValue.ID, folderValue.ID, result.UIDValidity, value.ProviderUID)
	if err != nil {
		return maildomain.Message{}, maildomain.Location{}, "", cli.WrapAppError(cli.ErrorInternal, "cannot reload canonical imported message", false, err)
	}
	location := providerLocation(message, folderValue.ID, result.UIDValidity, value.ProviderUID)
	canonicalVariant, matched := matcher.match(location.RawContentHash, location.RawByteSize)
	if location.ID == "" || !matched || canonicalVariant != providerRawVariant {
		return maildomain.Message{}, maildomain.Location{}, "", cli.NewAppError(cli.ErrorConflict, "canonical imported provider copy differs from the reviewed source beyond the exact iCloud terminal-CRLF normalization", false)
	}
	return message, location, providerRawVariant, nil
}

func (commands accountCommands) importProviderRawMatcher(ctx context.Context, paths config.AppPaths, value maildomain.MessageImport) (importProviderRawMatcher, error) {
	reader, err := commands.inboundBlobStore(paths).OpenVerifiedContext(ctx, value.RawStorageKey, value.RawContentHash, value.RawByteSize)
	if err != nil {
		return importProviderRawMatcher{}, mapBlobReadError(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, reader)
	closeErr := reader.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	if copyErr != nil {
		return importProviderRawMatcher{}, cli.WrapAppError(cli.ErrorUnavailable, "cannot verify the durable import source while proving provider bytes", true, copyErr)
	}
	if _, err := hash.Write([]byte("\r\n")); err != nil {
		return importProviderRawMatcher{}, cli.WrapAppError(cli.ErrorInternal, "cannot derive the iCloud terminal-CRLF import fingerprint", false, err)
	}
	return importProviderRawMatcher{
		exactHash: strings.ToLower(value.RawContentHash), exactSize: value.RawByteSize,
		terminalCRLFHash: hex.EncodeToString(hash.Sum(nil)), terminalCRLFSize: value.RawByteSize + 2,
	}, nil
}

func providerLocation(message maildomain.Message, folderID string, uidValidity, uid uint32) maildomain.Location {
	for _, location := range message.Locations {
		if location.FolderID == folderID && location.UIDValidity == uidValidity && location.UID == uid && location.Present {
			return location
		}
	}
	return maildomain.Location{}
}

func mapMessageImportStoreError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrMessageImportConflict):
		return cli.WrapAppError(cli.ErrorConflict, "message import idempotency/provider state conflicts with this normalized request", false, err)
	case errors.Is(err, maildomain.ErrInvalidMessageImport):
		return cli.WrapAppError(cli.ErrorUsage, "message import request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist the message import", false, err)
	}
}

func withMessageImportDetails(err error, value maildomain.MessageImport) error {
	appErr := cli.NormalizeError(err)
	appErr.Details = map[string]any{
		"import_id": value.ID, "status": value.Status, "folder_id": value.FolderID,
		"provider_uid_validity": value.ProviderUIDValidity, "provider_uid": value.ProviderUID,
		"reconciliation_required": value.ReconciliationRequired,
	}
	return appErr
}
