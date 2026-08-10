package app

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

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
)

const defaultMailboxImportMaxMessages = 100

type mailboxImportSource struct {
	path string
	file *os.File
	info os.FileInfo
	hash string
	size int64
}

func (source *mailboxImportSource) close() {
	if source != nil && source.file != nil {
		_ = source.file.Close()
	}
}

func (commands accountCommands) MailMailboxImportPreview(ctx context.Context, home, accountSelector, sourcePath string, request maildomain.MailboxImportRequest) (account.Account, folder.Folder, maildomain.MailboxImportPlan, int64, error) {
	request, err := normalizeMailboxImportRequest(request)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportPlan{}, 0, err
	}
	source, err := openMailboxImportSource(ctx, sourcePath)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportPlan{}, 0, err
	}
	defer source.close()

	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportPlan{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportPlan{}, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, request.Folder)
	if err != nil {
		return accountValue, folder.Folder{}, maildomain.MailboxImportPlan{}, state.SchemaVersion(), err
	}

	plan := maildomain.MailboxImportPlan{
		FolderID: folderValue.ID, FolderName: folderValue.Name,
		SourceContentHash: source.hash, SourceByteSize: source.size,
		StartOffset: 0, StartIndex: 1, MaxMessages: request.MaxMessages, Mode: request.Mode,
	}
	job, lookupErr := state.MailboxImportJobs().Resolve(ctx, accountValue.ID, folderValue.ID, source.path, source.hash, source.size)
	switch {
	case lookupErr == nil:
		plan.ExistingJobID = job.ID
		plan.StartOffset = job.NextOffset
		plan.StartIndex = job.NextIndex
		plan.AlreadyImported = job.ImportedCount
		plan.Complete = job.Status == maildomain.MailboxImportJobConfirmed
	case errors.Is(lookupErr, maildomain.ErrMailboxImportJobNotFound):
	case errors.Is(lookupErr, maildomain.ErrMailboxImportJobConflict):
		return accountValue, folderValue, plan, state.SchemaVersion(), cli.NewAppError(cli.ErrorConflict, "mailbox import cannot resume because the source file changed after the durable job began", false)
	default:
		return accountValue, folderValue, plan, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot inspect mailbox import recovery state", false, lookupErr)
	}

	if !plan.Complete && plan.StartOffset < source.size {
		if _, err := maildomain.DecodeMboxRDRecord(ctx, source.file, plan.StartOffset, io.Discard); err != nil {
			return accountValue, folderValue, plan, state.SchemaVersion(), mapMailboxImportDecodeError(err)
		}
		if err := source.verifyStable(); err != nil {
			return accountValue, folderValue, plan, state.SchemaVersion(), err
		}
	}
	return accountValue, folderValue, plan, state.SchemaVersion(), nil
}

func (commands accountCommands) MailMailboxImport(ctx context.Context, home, accountSelector, sourcePath string, request maildomain.MailboxImportRequest) (account.Account, folder.Folder, maildomain.MailboxImportRun, int64, error) {
	request, err := normalizeMailboxImportRequest(request)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportRun{}, 0, err
	}
	source, err := openMailboxImportSource(ctx, sourcePath)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportRun{}, 0, err
	}
	defer source.close()

	accountValue, folderValue, job, paths, schemaVersion, err := commands.beginMailboxImportJob(ctx, home, accountSelector, source, request)
	if err != nil {
		return accountValue, folderValue, maildomain.MailboxImportRun{}, schemaVersion, err
	}
	run := maildomain.MailboxImportRun{Job: job}
	if job.Status == maildomain.MailboxImportJobConfirmed {
		run.Summary = mailboxImportSummary(job, request.Mode, 0, 0, 0, 0)
		return accountValue, folderValue, run, schemaVersion, nil
	}
	if source.size == 0 {
		job, err = commands.completeMailboxImportJob(ctx, home, job.ID)
		if err != nil {
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(err, run)
		}
		run.Job = job
		run.Summary = mailboxImportSummary(job, request.Mode, 0, 0, 0, 0)
		return accountValue, folderValue, run, schemaVersion, nil
	}

	scanOffset := job.NextOffset
	scanIndex := job.NextIndex
	attempted, confirmed, failed, reused := 0, 0, 0, 0
	reachedEOF := false
	for attempted < request.MaxMessages && scanOffset < source.size {
		if err := ctx.Err(); err != nil {
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(cli.WrapAppError(cli.ErrorUnavailable, "mailbox import was canceled", true, err), run)
		}
		if err := source.verifyStable(); err != nil {
			job, _ = commands.markMailboxImportJobError(ctx, home, job.ID, string(cli.ErrorConflict))
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(err, run)
		}

		temporary, tempErr := os.CreateTemp(paths.Root, ".icloud-mailbox-import-*.eml")
		if tempErr != nil {
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(cli.WrapAppError(cli.ErrorConfiguration, "cannot create a private mailbox import staging file", false, tempErr), run)
		}
		temporaryPath := temporary.Name()
		cleanupTemp := func() {
			_ = temporary.Close()
			_ = os.Remove(temporaryPath)
		}
		if err := temporary.Chmod(0o600); err != nil {
			cleanupTemp()
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(cli.WrapAppError(cli.ErrorConfiguration, "cannot secure the mailbox import staging file", false, err), run)
		}
		record, decodeErr := maildomain.DecodeMboxRDRecord(ctx, source.file, scanOffset, temporary)
		closeErr := temporary.Close()
		if decodeErr == nil {
			decodeErr = closeErr
		}
		if decodeErr != nil {
			_ = os.Remove(temporaryPath)
			job, _ = commands.markMailboxImportJobError(ctx, home, job.ID, string(cli.NormalizeError(mapMailboxImportDecodeError(decodeErr)).Code))
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(mapMailboxImportDecodeError(decodeErr), run)
		}
		if err := source.verifyStable(); err != nil {
			_ = os.Remove(temporaryPath)
			job, _ = commands.markMailboxImportJobError(ctx, home, job.ID, string(cli.ErrorConflict))
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(err, run)
		}

		item, existed, itemErr := commands.prepareMailboxImportItem(ctx, home, maildomain.PrepareMailboxImportItem{
			JobID: job.ID, Index: scanIndex, StartOffset: record.StartOffset, NextOffset: record.NextOffset,
			RawContentHash: record.RawContentHash, RawByteSize: record.RawByteSize, Now: commands.timeNow(),
		})
		if itemErr != nil {
			_ = os.Remove(temporaryPath)
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(itemErr, run)
		}
		attempted++
		if existed && item.Status == maildomain.MailboxImportItemConfirmed {
			reused++
			confirmed++
			run.Items = append(run.Items, maildomain.MailboxImportItemResult{
				Index: scanIndex, StartOffset: record.StartOffset, NextOffset: record.NextOffset,
				ImportID: item.ImportID, MessageID: item.MessageID, LocationID: item.LocationID, Status: string(item.Status), Reused: true,
			})
			_ = os.Remove(temporaryPath)
			scanOffset = record.NextOffset
			scanIndex++
			reachedEOF = record.EOF
			if reachedEOF {
				break
			}
			continue
		}

		_, _, _, importValue, message, _, importErr := commands.MailMessageImport(ctx, home, accountValue.ID, temporaryPath, maildomain.MessageImportRequest{
			Folder: folderValue.ID, InternalDate: record.EnvelopeDate, IdempotencyKey: item.IdempotencyKey,
		})
		_ = os.Remove(temporaryPath)
		if importErr != nil {
			errorCode := string(cli.NormalizeError(importErr).Code)
			var failedItem maildomain.MailboxImportItem
			retryNewKey := importValue.Status == maildomain.MessageImportFailed
			job, failedItem, itemErr = commands.failMailboxImportItem(ctx, home, job.ID, scanIndex, errorCode, retryNewKey)
			if itemErr != nil {
				run.Job = job
				run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
				return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(itemErr, run)
			}
			failed++
			run.Items = append(run.Items, maildomain.MailboxImportItemResult{
				Index: scanIndex, StartOffset: record.StartOffset, NextOffset: record.NextOffset,
				ImportID: failedItem.ImportID, MessageID: failedItem.MessageID, LocationID: failedItem.LocationID, Status: string(failedItem.Status), ErrorCode: errorCode,
			})
			if ctx.Err() != nil || request.Mode == "atomic" {
				run.Job = job
				run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
				return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(importErr, run)
			}
			scanOffset = record.NextOffset
			scanIndex++
			reachedEOF = record.EOF
			if reachedEOF {
				break
			}
			continue
		}
		if importValue.Status != maildomain.MessageImportConfirmed || message.ID == "" || importValue.LocationID == "" {
			unexpected := cli.NewAppError(cli.ErrorInternal, "single-message import returned without proven canonical provider state", false)
			job, _, _ = commands.failMailboxImportItem(ctx, home, job.ID, scanIndex, string(unexpected.Code), false)
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(unexpected, run)
		}
		job, item, itemErr = commands.confirmMailboxImportItem(ctx, home, job.ID, scanIndex, importValue.ID, message.ID, importValue.LocationID)
		if itemErr != nil {
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(itemErr, run)
		}
		confirmed++
		run.Items = append(run.Items, maildomain.MailboxImportItemResult{
			Index: scanIndex, StartOffset: record.StartOffset, NextOffset: record.NextOffset,
			ImportID: item.ImportID, MessageID: item.MessageID, LocationID: item.LocationID, Status: string(item.Status),
		})
		scanOffset = record.NextOffset
		scanIndex++
		reachedEOF = record.EOF
		if reachedEOF {
			break
		}
	}

	if err := source.verifyStable(); err != nil {
		job, _ = commands.markMailboxImportJobError(ctx, home, job.ID, string(cli.ErrorConflict))
		run.Job = job
		run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
		return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(err, run)
	}
	if reachedEOF || scanOffset >= source.size {
		completed, completeErr := commands.completeMailboxImportJob(ctx, home, job.ID)
		if completeErr == nil {
			job = completed
		} else if !errors.Is(completeErr, maildomain.ErrMailboxImportJobConflict) {
			run.Job = job
			run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
			return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(completeErr, run)
		} else {
			job, _ = commands.mailboxImportJob(ctx, home, job.ID)
		}
	} else {
		job, _ = commands.mailboxImportJob(ctx, home, job.ID)
	}
	run.Job = job
	run.Summary = mailboxImportSummary(job, request.Mode, attempted, confirmed, failed, reused)
	if failed > 0 {
		return accountValue, folderValue, run, schemaVersion, mailboxImportRunError(cli.NewAppError(cli.ErrorUnavailable, "mailbox import completed this bounded pass with one or more failed records; durable resume remains at the earliest unconfirmed record", true), run)
	}
	return accountValue, folderValue, run, schemaVersion, nil
}

func normalizeMailboxImportRequest(request maildomain.MailboxImportRequest) (maildomain.MailboxImportRequest, error) {
	request.Folder = strings.TrimSpace(request.Folder)
	if request.Folder == "" {
		return request, cli.NewAppError(cli.ErrorUsage, "--to destination folder is required", false)
	}
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Mode == "" {
		request.Mode = "atomic"
	}
	if request.Mode != "atomic" && request.Mode != "continue-on-error" {
		return request, cli.NewAppError(cli.ErrorUsage, "--mode must be atomic or continue-on-error", false)
	}
	if request.MaxMessages == 0 {
		request.MaxMessages = defaultMailboxImportMaxMessages
	}
	if request.MaxMessages < 1 || request.MaxMessages > cli.MaximumBulkItems {
		return request, cli.NewAppError(cli.ErrorUsage, fmt.Sprintf("--max-messages must be between 1 and %d", cli.MaximumBulkItems), false)
	}
	return request, nil
}

func openMailboxImportSource(ctx context.Context, sourcePath string) (*mailboxImportSource, error) {
	sourcePath = strings.TrimSpace(sourcePath)
	if sourcePath == "" {
		return nil, cli.NewAppError(cli.ErrorUsage, "one .mbox source file is required", false)
	}
	absolute, err := filepath.Abs(filepath.Clean(sourcePath))
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve mailbox import source", false, err)
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorNotFound, "mailbox import source file is unavailable", false, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, cli.NewAppError(cli.ErrorSafety, "mailbox import source must be one regular mboxrd file", false)
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot open mailbox import source file", false, err)
	}
	openedInfo, err := file.Stat()
	if err != nil || !openedInfo.Mode().IsRegular() || !os.SameFile(info, openedInfo) || openedInfo.Size() != info.Size() {
		_ = file.Close()
		return nil, cli.WrapAppError(cli.ErrorSafety, "mailbox import source changed while it was being opened", false, err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, &mailboxImportContextReader{ctx: ctx, reader: file}); err != nil {
		_ = file.Close()
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, cli.WrapAppError(cli.ErrorUnavailable, "mailbox import source fingerprinting was canceled", true, err)
		}
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot fingerprint mailbox import source", false, err)
	}
	afterInfo, err := file.Stat()
	if err != nil || afterInfo.Size() != openedInfo.Size() || !afterInfo.ModTime().Equal(openedInfo.ModTime()) {
		_ = file.Close()
		return nil, cli.WrapAppError(cli.ErrorSafety, "mailbox import source changed while it was being fingerprinted", false, err)
	}
	pathInfo, err := os.Lstat(absolute)
	if err != nil || !os.SameFile(afterInfo, pathInfo) {
		_ = file.Close()
		return nil, cli.WrapAppError(cli.ErrorSafety, "mailbox import source changed while it was being fingerprinted", false, err)
	}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, cli.WrapAppError(cli.ErrorUnavailable, "cannot rewind mailbox import source", false, err)
	}
	return &mailboxImportSource{path: absolute, file: file, info: afterInfo, hash: hex.EncodeToString(hash.Sum(nil)), size: afterInfo.Size()}, nil
}

func (source *mailboxImportSource) verifyStable() error {
	if source == nil || source.file == nil || source.info == nil {
		return cli.NewAppError(cli.ErrorInternal, "mailbox import source state is unavailable", false)
	}
	openedInfo, err := source.file.Stat()
	if err != nil {
		return cli.WrapAppError(cli.ErrorUnavailable, "cannot verify mailbox import source", false, err)
	}
	pathInfo, err := os.Lstat(source.path)
	if err != nil || !pathInfo.Mode().IsRegular() || pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(openedInfo, pathInfo) || openedInfo.Size() != source.size || !openedInfo.ModTime().Equal(source.info.ModTime()) {
		return cli.WrapAppError(cli.ErrorConflict, "mailbox import source changed after the durable job began; continuation is refused", false, err)
	}
	return nil
}

func (commands accountCommands) beginMailboxImportJob(ctx context.Context, home, accountSelector string, source *mailboxImportSource, request maildomain.MailboxImportRequest) (account.Account, folder.Folder, maildomain.MailboxImportJob, config.AppPaths, int64, error) {
	state, paths, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportJob{}, config.AppPaths{}, 0, err
	}
	defer state.Close()
	defer unlock()
	accountValue, err := state.Accounts().ResolveSelected(ctx, accountSelector)
	if err != nil {
		return account.Account{}, folder.Folder{}, maildomain.MailboxImportJob{}, paths, state.SchemaVersion(), mapAccountError(err)
	}
	folderValue, err := resolveMailFolder(ctx, state, accountValue.ID, request.Folder)
	if err != nil {
		return accountValue, folder.Folder{}, maildomain.MailboxImportJob{}, paths, state.SchemaVersion(), err
	}
	jobID, err := commands.generateID("mbi")
	if err != nil {
		return accountValue, folderValue, maildomain.MailboxImportJob{}, paths, state.SchemaVersion(), cli.WrapAppError(cli.ErrorInternal, "cannot allocate a mailbox import job ID", false, err)
	}
	job, _, err := state.MailboxImportJobs().Begin(ctx, maildomain.BeginMailboxImportJob{
		JobID: jobID, AccountID: accountValue.ID, FolderID: folderValue.ID,
		SourcePath: source.path, SourceContentHash: source.hash, SourceByteSize: source.size, Now: commands.timeNow(),
	})
	if err != nil {
		if errors.Is(err, maildomain.ErrMailboxImportJobConflict) {
			return accountValue, folderValue, maildomain.MailboxImportJob{}, paths, state.SchemaVersion(), cli.WrapAppError(cli.ErrorConflict, "mailbox import source file changed after the durable job began; continuation is refused", false, err)
		}
		return accountValue, folderValue, maildomain.MailboxImportJob{}, paths, state.SchemaVersion(), mapMailboxImportStoreError(err)
	}
	return accountValue, folderValue, job, paths, state.SchemaVersion(), nil
}

func (commands accountCommands) prepareMailboxImportItem(ctx context.Context, home string, input maildomain.PrepareMailboxImportItem) (maildomain.MailboxImportItem, bool, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportItem{}, false, err
	}
	defer state.Close()
	defer unlock()
	item, reused, err := state.MailboxImportJobs().PrepareItem(ctx, input)
	if err != nil {
		return maildomain.MailboxImportItem{}, false, mapMailboxImportStoreError(err)
	}
	return item, reused, nil
}

func (commands accountCommands) failMailboxImportItem(ctx context.Context, home, jobID string, index int, errorCode string, retryNewKey bool) (maildomain.MailboxImportJob, maildomain.MailboxImportItem, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	defer state.Close()
	defer unlock()
	job, item, err := state.MailboxImportJobs().FailItem(ctx, jobID, index, errorCode, retryNewKey, commands.timeNow())
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, mapMailboxImportStoreError(err)
	}
	return job, item, nil
}

func (commands accountCommands) confirmMailboxImportItem(ctx context.Context, home, jobID string, index int, importID, messageID, locationID string) (maildomain.MailboxImportJob, maildomain.MailboxImportItem, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, err
	}
	defer state.Close()
	defer unlock()
	job, item, err := state.MailboxImportJobs().ConfirmItem(ctx, jobID, index, importID, messageID, locationID, commands.timeNow())
	if err != nil {
		return maildomain.MailboxImportJob{}, maildomain.MailboxImportItem{}, mapMailboxImportStoreError(err)
	}
	return job, item, nil
}

func (commands accountCommands) completeMailboxImportJob(ctx context.Context, home, jobID string) (maildomain.MailboxImportJob, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportJob{}, err
	}
	defer state.Close()
	defer unlock()
	job, err := state.MailboxImportJobs().MarkComplete(ctx, jobID, commands.timeNow())
	if err != nil {
		return job, mapMailboxImportStoreError(err)
	}
	return job, nil
}

func (commands accountCommands) markMailboxImportJobError(ctx context.Context, home, jobID, errorCode string) (maildomain.MailboxImportJob, error) {
	state, _, unlock, err := commands.openWriteState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportJob{}, err
	}
	defer state.Close()
	defer unlock()
	job, err := state.MailboxImportJobs().MarkError(ctx, jobID, errorCode, commands.timeNow())
	if err != nil {
		return maildomain.MailboxImportJob{}, mapMailboxImportStoreError(err)
	}
	return job, nil
}

func (commands accountCommands) mailboxImportJob(ctx context.Context, home, jobID string) (maildomain.MailboxImportJob, error) {
	state, err := openState(ctx, home)
	if err != nil {
		return maildomain.MailboxImportJob{}, err
	}
	defer state.Close()
	job, err := state.MailboxImportJobs().Job(ctx, jobID)
	if err != nil {
		return maildomain.MailboxImportJob{}, mapMailboxImportStoreError(err)
	}
	return job, nil
}

func mailboxImportSummary(job maildomain.MailboxImportJob, mode string, attempted, confirmed, failed, reused int) maildomain.MailboxImportSummary {
	complete := job.Status == maildomain.MailboxImportJobConfirmed
	return maildomain.MailboxImportSummary{
		JobID: job.ID, FolderID: job.FolderID, Mode: mode, Status: job.Status,
		Attempted: attempted, Confirmed: confirmed, Failed: failed, Reused: reused,
		ImportedCount: job.ImportedCount, NextIndex: job.NextIndex, NextOffset: job.NextOffset,
		SourceByteSize: job.SourceByteSize, Partial: job.Status == maildomain.MailboxImportJobPartial || failed > 0,
		More: !complete && job.NextOffset < job.SourceByteSize, Complete: complete,
	}
}

func mailboxImportRunError(err error, run maildomain.MailboxImportRun) error {
	appErr := cli.NormalizeError(err)
	if appErr.Details == nil {
		appErr.Details = map[string]any{}
	}
	if run.Job.ID != "" {
		appErr.Details["job_id"] = run.Job.ID
		appErr.Details["summary"] = run.Summary
	}
	if len(run.Items) > 0 {
		appErr.Details["items"] = append([]maildomain.MailboxImportItemResult(nil), run.Items...)
	}
	return appErr
}

func mapMailboxImportDecodeError(err error) error {
	switch {
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return cli.WrapAppError(cli.ErrorUnavailable, "mailbox import decoding was canceled", true, err)
	case errors.Is(err, maildomain.ErrMailboxImportNoRecord), errors.Is(err, maildomain.ErrInvalidMailboxImport):
		return cli.WrapAppError(cli.ErrorUsage, "mailbox import source is not valid resumable mboxrd at the durable frontier", false, err)
	default:
		return cli.WrapAppError(cli.ErrorUnavailable, "cannot decode the next mboxrd record", false, err)
	}
}

func mapMailboxImportStoreError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrMailboxImportJobConflict):
		return cli.WrapAppError(cli.ErrorConflict, "mailbox import durable source, record, or resume evidence conflicts with this request", false, err)
	case errors.Is(err, maildomain.ErrMailboxImportJobNotFound):
		return cli.WrapAppError(cli.ErrorNotFound, "mailbox import recovery state was not found", false, err)
	case errors.Is(err, maildomain.ErrInvalidMailboxImport):
		return cli.WrapAppError(cli.ErrorInternal, "mailbox import durable state is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot persist mailbox import recovery state", false, err)
	}
}

type mailboxImportContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *mailboxImportContextReader) Read(value []byte) (int, error) {
	select {
	case <-reader.ctx.Done():
		return 0, reader.ctx.Err()
	default:
		return reader.reader.Read(value)
	}
}
