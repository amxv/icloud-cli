package app

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	"github.com/amxv/icloud-cli/internal/config"
	"github.com/amxv/icloud-cli/internal/folder"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/gofrs/flock"
)

const (
	defaultWatchPollInterval      = time.Minute
	defaultWatchReconcileInterval = 15 * time.Minute
	defaultWatchOperationTimeout  = 30 * time.Second
	defaultWatchReconnectMin      = time.Second
	defaultWatchReconnectMax      = 30 * time.Second
	watchAccountSchedulePageSize  = 25
)

var errTooManyWatchAccounts = errors.New("too many accounts selected for mail watch")

type watchSession struct {
	folder  folder.Folder
	session transport.IMAPMailboxWatchSession
}

type watchSyncWaiter struct {
	ready    chan struct{}
	granted  bool
	released bool
}

// watchSyncGate serializes durable sync work while guaranteeing FIFO handoff
// to already-waiting account workers. A channel semaphore permits a releasing
// noisy worker to race a queued worker for the next token; this explicit queue
// keeps the larger all-account worker set fair and cancellation-aware.
type watchSyncGate struct {
	mu      sync.Mutex
	held    bool
	waiters []*watchSyncWaiter
}

func (gate *watchSyncGate) acquire(ctx context.Context) (func(), error) {
	waiter := &watchSyncWaiter{ready: make(chan struct{})}
	gate.mu.Lock()
	if !gate.held && len(gate.waiters) == 0 {
		gate.held = true
		waiter.granted = true
		gate.mu.Unlock()
		return func() { gate.release(waiter) }, nil
	}
	gate.waiters = append(gate.waiters, waiter)
	gate.mu.Unlock()

	select {
	case <-waiter.ready:
		if err := ctx.Err(); err != nil {
			gate.release(waiter)
			return nil, err
		}
		return func() { gate.release(waiter) }, nil
	case <-ctx.Done():
		gate.mu.Lock()
		if waiter.granted {
			gate.mu.Unlock()
			gate.release(waiter)
			return nil, ctx.Err()
		}
		for index, queued := range gate.waiters {
			if queued == waiter {
				gate.waiters = append(gate.waiters[:index], gate.waiters[index+1:]...)
				break
			}
		}
		gate.mu.Unlock()
		return nil, ctx.Err()
	}
}

func (gate *watchSyncGate) release(waiter *watchSyncWaiter) {
	if waiter == nil {
		return
	}
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if !waiter.granted || waiter.released {
		return
	}
	waiter.released = true
	if len(gate.waiters) == 0 {
		gate.held = false
		return
	}
	next := gate.waiters[0]
	gate.waiters = gate.waiters[1:]
	next.granted = true
	close(next.ready)
}

type accountWatchEmitter struct {
	accountID string
	now       func() time.Time
	emit      func(maildomain.WatchEvent) error
	sequence  uint64
}

func (emitter *accountWatchEmitter) send(event maildomain.WatchEvent) error {
	emitter.sequence++
	event.Sequence = emitter.sequence
	event.AccountID = emitter.accountID
	event.Timestamp = emitter.now().UTC()
	return emitter.emit(event)
}

func (commands accountCommands) MailWatch(ctx context.Context, home, selector string, options maildomain.WatchOptions, emit func(maildomain.WatchEvent) error) error {
	if emit == nil {
		return cli.NewAppError(cli.ErrorInternal, "mail watch event sink is unavailable", false)
	}
	options = normalizeWatchOptions(options)
	if options.Limit < 1 || options.Limit > 100 {
		return cli.NewAppError(cli.ErrorUsage, "watch sync limit must be between 1 and 100", false)
	}
	if options.AllAccounts && strings.TrimSpace(selector) != "" {
		return cli.NewAppError(cli.ErrorUsage, "--all-accounts cannot be combined with --account", false)
	}

	setupCtx, setupCancel := context.WithTimeout(ctx, options.OperationTimeout)
	state, err := openState(setupCtx, home)
	if err != nil {
		setupCancel()
		return err
	}
	accounts, err := watchAccounts(setupCtx, state, selector, options.AllAccounts)
	state.Close()
	setupCancel()
	if errors.Is(err, errTooManyWatchAccounts) {
		return cli.NewAppError(cli.ErrorConflict, fmt.Sprintf("mail watch supports at most %d accounts per process; select one account with --account or run separate bounded watchers", maildomain.MaxWatchAccounts), false)
	}
	if err != nil {
		return mapAccountError(err)
	}
	if len(accounts) == 0 {
		return cli.NewAppError(cli.ErrorNotFound, "no accounts are available to watch", false)
	}
	if len(accounts) > maildomain.MaxWatchAccounts {
		return cli.NewAppError(cli.ErrorConflict, fmt.Sprintf("mail watch supports at most %d accounts per process; select one account with --account or run separate bounded watchers", maildomain.MaxWatchAccounts), false)
	}
	paths, err := config.ResolveAppPaths(home)
	if err != nil {
		return cli.WrapAppError(cli.ErrorConfiguration, "cannot resolve local state paths", false, err)
	}
	if err := config.EnsureAppPaths(paths); err != nil {
		return cli.WrapAppError(cli.ErrorConfiguration, "cannot initialize local state paths", false, err)
	}
	unlockWatches, err := acquireWatchLocks(paths, accounts)
	if err != nil {
		return err
	}
	defer unlockWatches()

	watchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	syncGate := &watchSyncGate{}
	workerErrors := make(chan error, len(accounts))
	var workers sync.WaitGroup
	for _, value := range accounts {
		value := value
		workers.Add(1)
		go func() {
			defer workers.Done()
			emitter := &accountWatchEmitter{accountID: value.ID, now: commands.timeNow, emit: emit}
			if err := commands.watchAccount(watchCtx, home, value, options, syncGate, emitter); err != nil && !errors.Is(err, context.Canceled) {
				select {
				case workerErrors <- err:
				default:
				}
				cancel()
			}
		}()
	}

	var result error
	select {
	case <-ctx.Done():
		cancel()
	case result = <-workerErrors:
		cancel()
	}
	workers.Wait()
	if result != nil {
		return result
	}
	return nil
}

func normalizeWatchOptions(options maildomain.WatchOptions) maildomain.WatchOptions {
	if options.Limit == 0 {
		options.Limit = 20
	}
	if options.PollInterval <= 0 {
		options.PollInterval = defaultWatchPollInterval
	}
	if options.ReconcileInterval <= 0 {
		options.ReconcileInterval = defaultWatchReconcileInterval
	}
	if options.OperationTimeout <= 0 {
		options.OperationTimeout = defaultWatchOperationTimeout
	}
	if options.ReconnectMin <= 0 {
		options.ReconnectMin = defaultWatchReconnectMin
	}
	if options.ReconnectMax <= 0 {
		options.ReconnectMax = defaultWatchReconnectMax
	}
	if options.ReconnectMax < options.ReconnectMin {
		options.ReconnectMax = options.ReconnectMin
	}
	return options
}

func watchAccounts(ctx context.Context, state *store.Store, selector string, all bool) ([]account.Account, error) {
	if !all {
		value, err := state.Accounts().ResolveSelected(ctx, selector)
		if err != nil {
			return nil, err
		}
		return []account.Account{value}, nil
	}
	values := make([]account.Account, 0, maildomain.MaxWatchAccounts)
	cursor := ""
	for {
		page, more, next, err := state.Accounts().ListPage(ctx, watchAccountSchedulePageSize, cursor)
		if err != nil {
			return nil, err
		}
		if len(values)+len(page) > maildomain.MaxWatchAccounts || (len(values)+len(page) == maildomain.MaxWatchAccounts && more) {
			return nil, errTooManyWatchAccounts
		}
		values = append(values, page...)
		if !more {
			break
		}
		if next == "" || next == cursor {
			return nil, errors.New("mail watch account pagination did not advance")
		}
		cursor = next
	}
	if len(values) < 2 {
		return values, nil
	}
	type candidate struct {
		value account.Account
		last  *time.Time
	}
	candidates := make([]candidate, 0, len(values))
	for _, value := range values {
		checkpoints, _, err := state.Mail().Checkpoints(ctx, value.ID, 100)
		if err != nil {
			return nil, err
		}
		var latest *time.Time
		for _, checkpoint := range checkpoints {
			if checkpoint.LastSuccessAt != nil && (latest == nil || checkpoint.LastSuccessAt.After(*latest)) {
				copy := checkpoint.LastSuccessAt.UTC()
				latest = &copy
			}
		}
		candidates = append(candidates, candidate{value: value, last: latest})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].last == nil && candidates[j].last != nil {
			return true
		}
		if candidates[i].last != nil && candidates[j].last == nil {
			return false
		}
		if candidates[i].last != nil && candidates[j].last != nil && !candidates[i].last.Equal(*candidates[j].last) {
			return candidates[i].last.Before(*candidates[j].last)
		}
		return candidates[i].value.ID < candidates[j].value.ID
	})
	for index := range candidates {
		values[index] = candidates[index].value
	}
	return values, nil
}

func acquireWatchLocks(paths config.AppPaths, accounts []account.Account) (func(), error) {
	locks := make([]*flock.Flock, 0, len(accounts))
	for _, value := range accounts {
		digest := sha256.Sum256([]byte(value.ID))
		path := filepath.Join(paths.LockRoot, fmt.Sprintf("watch-%x.lock", digest[:12]))
		lock := flock.New(path, flock.SetPermissions(0o600))
		locked, err := lock.TryLock()
		if err != nil || !locked {
			for index := len(locks) - 1; index >= 0; index-- {
				_ = locks[index].Unlock()
			}
			if err != nil {
				return nil, cli.WrapAppError(cli.ErrorConflict, "cannot acquire the account watch lock", true, err)
			}
			return nil, cli.NewAppError(cli.ErrorConflict, "another icloud watch already owns one selected account", true)
		}
		locks = append(locks, lock)
	}
	return func() {
		for index := len(locks) - 1; index >= 0; index-- {
			_ = locks[index].Unlock()
		}
	}, nil
}

func (commands accountCommands) watchAccount(ctx context.Context, home string, value account.Account, options maildomain.WatchOptions, syncGate *watchSyncGate, emitter *accountWatchEmitter) error {
	if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventStartup, PollInterval: options.PollInterval, ReconcileInterval: options.ReconcileInterval}); err != nil {
		return err
	}
	if err := commands.runWatchSync(ctx, home, value.ID, "", false, true, options, syncGate, emitter); err != nil {
		if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
			return emitErr
		}
	}

	lastMode := ""
	reconnectAttempt := 0
	for {
		if ctx.Err() != nil {
			_ = emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventStopped, Reason: "canceled"})
			_ = emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventCompleted})
			return nil
		}
		sessions, mode, reason, capabilities, err := commands.openWatchSessions(ctx, home, value.ID, options.OperationTimeout)
		if err != nil {
			reconnectAttempt++
			if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
				return emitErr
			}
			delay := watchReconnectDelay(value.ID, reconnectAttempt, options, watchErrorRetryable(err))
			if emitErr := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventReconnect, Attempt: reconnectAttempt, Backoff: delay, Reason: watchErrorStage(err)}); emitErr != nil {
				return emitErr
			}
			if err := sleepWatch(ctx, delay); err != nil {
				continue
			}
			continue
		}
		reconnectAttempt = 0
		modeKey := string(mode) + "|" + reason
		if modeKey != lastMode {
			if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventMode, Mode: mode, Reason: reason, Capabilities: capabilities}); err != nil {
				closeWatchSessions(sessions, options.OperationTimeout)
				return err
			}
			lastMode = modeKey
		}
		if mode == maildomain.WatchModePolling {
			if err := commands.runPollingWatch(ctx, home, value.ID, options, syncGate, emitter); err != nil {
				return err
			}
			continue
		}
		for _, session := range sessions {
			if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventIdle, Mode: maildomain.WatchModeIdle, FolderID: session.folder.ID, FolderName: session.folder.Name, Quiet: true}); err != nil {
				closeWatchSessions(sessions, options.OperationTimeout)
				return err
			}
		}
		trigger, waitErr := waitWatchSessions(ctx, sessions, options.ReconcileInterval)
		closeWatchSessions(sessions, options.OperationTimeout)
		if ctx.Err() != nil {
			continue
		}
		if waitErr != nil {
			reconnectAttempt++
			delay := watchReconnectDelay(value.ID, reconnectAttempt, options, true)
			if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventReconnect, Attempt: reconnectAttempt, Backoff: delay, Reason: watchErrorStage(waitErr)}); err != nil {
				return err
			}
			if err := sleepWatch(ctx, delay); err != nil {
				continue
			}
			continue
		}
		if trigger.reason == transport.MailboxWatchChanged {
			if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventChange, FolderID: trigger.folder.ID, FolderName: trigger.folder.Name, Reason: string(trigger.reason)}); err != nil {
				return err
			}
			if err := commands.runWatchSync(ctx, home, value.ID, trigger.folder.ID, false, false, options, syncGate, emitter); err != nil {
				if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
					return emitErr
				}
			}
			continue
		}
		if err := commands.runWatchSync(ctx, home, value.ID, "", true, false, options, syncGate, emitter); err != nil {
			if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
				return emitErr
			}
		}
	}
}

func (commands accountCommands) runPollingWatch(ctx context.Context, home, accountID string, options maildomain.WatchOptions, syncGate *watchSyncGate, emitter *accountWatchEmitter) error {
	poll := time.NewTimer(options.PollInterval)
	reconcile := time.NewTimer(options.ReconcileInterval)
	defer poll.Stop()
	defer reconcile.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-poll.C:
			if err := commands.runWatchSync(ctx, home, accountID, "", false, false, options, syncGate, emitter); err != nil {
				if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
					return emitErr
				}
			}
			poll.Reset(options.PollInterval)
		case <-reconcile.C:
			if err := commands.runWatchSync(ctx, home, accountID, "", true, false, options, syncGate, emitter); err != nil {
				if emitErr := emitter.send(degradedWatchEvent(err)); emitErr != nil {
					return emitErr
				}
			}
			return nil // re-probe capabilities and watchable folders after a full reconciliation.
		}
	}
}

func (commands accountCommands) runWatchSync(ctx context.Context, home, accountID, folderSelector string, refreshFolders, initial bool, options maildomain.WatchOptions, syncGate *watchSyncGate, emitter *accountWatchEmitter) error {
	release, err := syncGate.acquire(ctx)
	if err != nil {
		return err
	}
	defer release()
	operationCtx, cancel := context.WithTimeout(ctx, options.OperationTimeout)
	defer cancel()
	if refreshFolders {
		if _, _, _, err := commands.FolderStatus(operationCtx, home, accountID); err != nil {
			return err
		}
	}
	_, _, summaries, _, err := commands.MailSync(operationCtx, home, accountID, folderSelector, options.Limit)
	if err != nil {
		return err
	}
	for index := range summaries {
		summary := summaries[index]
		quiet := !initial && watchSummaryUnchanged(summary)
		if err := emitter.send(maildomain.WatchEvent{Type: maildomain.WatchEventSync, FolderID: summary.FolderID, FolderName: summary.FolderName, Summary: &summary, Quiet: quiet}); err != nil {
			return err
		}
	}
	return nil
}

func watchSummaryUnchanged(summary maildomain.SyncSummary) bool {
	outboundChanges := 0
	if summary.OutboundReconcile != nil {
		outboundChanges = summary.OutboundReconcile.Confirmed + summary.OutboundReconcile.ResolvedUnknown + summary.OutboundReconcile.Ambiguous
	}
	return summary.Fetched == 0 && summary.Created == 0 && summary.Updated == 0 && summary.StateChanges == 0 && outboundChanges == 0
}

func (commands accountCommands) openWatchSessions(ctx context.Context, home, accountID string, operationTimeout time.Duration) ([]watchSession, maildomain.WatchMode, string, []string, error) {
	setupCtx, setupCancel := context.WithTimeout(ctx, operationTimeout)
	state, err := openState(setupCtx, home)
	if err != nil {
		setupCancel()
		return nil, "", "", nil, err
	}
	defer state.Close()
	defer setupCancel()
	value, err := state.Accounts().Resolve(setupCtx, accountID)
	if err != nil {
		return nil, "", "", nil, mapAccountError(err)
	}
	targets := make([]folder.Folder, 0, 2)
	cursor := ""
	for {
		folders, more, next, err := state.Folders().ListSyncEnabledPage(setupCtx, value.ID, syncFolderSchedulePageSize, cursor)
		if err != nil {
			return nil, "", "", nil, cli.WrapAppError(cli.ErrorInternal, "cannot page enabled folders for mail watch", false, err)
		}
		for _, candidate := range folders {
			if folder.DefaultSyncEnabled(candidate.Kind) {
				targets = append(targets, candidate)
			}
		}
		if !more {
			break
		}
		if next == "" || next == cursor {
			return nil, "", "", nil, cli.NewAppError(cli.ErrorInternal, "mail watch folder pagination did not advance", false)
		}
		cursor = next
	}
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Kind != targets[j].Kind {
			return targets[i].Kind == folder.KindInbox
		}
		return targets[i].ID < targets[j].ID
	})
	if len(targets) == 0 {
		return nil, maildomain.WatchModePolling, "no_high_priority_folder", append([]string(nil), value.IMAPCapabilities...), nil
	}
	_, credential, err := state.Accounts().Credential(setupCtx, value.ID)
	if err != nil {
		return nil, "", "", nil, mapAccountError(err)
	}
	password, err := secret.NewManager(commands.keyring).Resolve(setupCtx, credential)
	if err != nil {
		return nil, "", "", nil, mapCredentialError(err)
	}
	defer zeroBytes(password)

	sessions := make([]watchSession, 0, len(targets))
	for _, target := range targets {
		var session transport.IMAPMailboxWatchSession
		var openErr error
		for _, username := range imapUsernameCandidates(value) {
			operationCtx, cancel := context.WithTimeout(ctx, operationTimeout)
			session, openErr = commands.watchClient().OpenMailboxWatch(operationCtx, transport.AuthRequest{Username: username, Password: password}, target.Name)
			cancel()
			if openErr == nil {
				break
			}
			failure, ok := transport.AsFailure(openErr)
			if !ok || failure.Kind != transport.FailureAuthentication {
				break
			}
		}
		if openErr != nil {
			closeWatchSessions(sessions, operationTimeout)
			return nil, "", "", nil, mapMailboxTransportError(openErr, target.Name)
		}
		info := session.Info()
		if info.Mode == transport.MailboxWatchPolling {
			closeWatchSession(session, operationTimeout)
			closeWatchSessions(sessions, operationTimeout)
			return nil, maildomain.WatchModePolling, "idle_unsupported", info.Capabilities, nil
		}
		sessions = append(sessions, watchSession{folder: target, session: session})
	}
	capabilities := []string(nil)
	if len(sessions) > 0 {
		capabilities = sessions[0].session.Info().Capabilities
	}
	return sessions, maildomain.WatchModeIdle, "idle_supported", capabilities, nil
}

func (commands accountCommands) watchClient() transport.IMAPWatchClient {
	if commands.watch != nil {
		return commands.watch
	}
	if candidate, ok := commands.imap.(transport.IMAPWatchClient); ok {
		return candidate
	}
	if candidate, ok := commands.mail.(transport.IMAPWatchClient); ok {
		return candidate
	}
	return unavailableWatchClient{}
}

type unavailableWatchClient struct{}

func (unavailableWatchClient) OpenMailboxWatch(context.Context, transport.AuthRequest, string) (transport.IMAPMailboxWatchSession, error) {
	return nil, transport.NewFailure(transport.StageIMAPIdle, transport.FailureConfiguration, false, errors.New("mail watch unavailable"))
}

type watchTrigger struct {
	folder folder.Folder
	reason transport.MailboxWatchReason
}

type watchWaitResult struct {
	trigger watchTrigger
	err     error
}

func waitWatchSessions(ctx context.Context, sessions []watchSession, reconcileInterval time.Duration) (watchTrigger, error) {
	waitCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	results := make(chan watchWaitResult, len(sessions))
	for _, item := range sessions {
		item := item
		go func() {
			reason, err := item.session.Wait(waitCtx, reconcileInterval)
			results <- watchWaitResult{trigger: watchTrigger{folder: item.folder, reason: reason}, err: err}
		}()
	}
	first := <-results
	cancel()
	for range sessions[1:] {
		<-results
	}
	if first.err != nil && errors.Is(first.err, context.Canceled) && ctx.Err() != nil {
		return watchTrigger{}, ctx.Err()
	}
	return first.trigger, first.err
}

func closeWatchSessions(sessions []watchSession, timeout time.Duration) {
	for _, item := range sessions {
		closeWatchSession(item.session, timeout)
	}
}

func closeWatchSession(session transport.IMAPMailboxWatchSession, timeout time.Duration) {
	if session == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	_ = session.Close(ctx)
	cancel()
}

func degradedWatchEvent(err error) maildomain.WatchEvent {
	appErr := cli.NormalizeError(err)
	stage := watchErrorStage(err)
	return maildomain.WatchEvent{Type: maildomain.WatchEventDegraded, Error: &maildomain.WatchError{Code: string(appErr.Code), Message: appErr.Message, Retryable: appErr.Retryable, Stage: stage}}
}

func watchErrorStage(err error) string {
	if failure, ok := transport.AsFailure(err); ok {
		return string(failure.Stage)
	}
	appErr := cli.NormalizeError(err)
	if appErr != nil && appErr.Details != nil {
		if stage, ok := appErr.Details["stage"]; ok {
			return fmt.Sprint(stage)
		}
	}
	return ""
}

func watchErrorRetryable(err error) bool {
	if failure, ok := transport.AsFailure(err); ok {
		return failure.Retryable
	}
	return cli.NormalizeError(err).Retryable
}

func watchReconnectDelay(accountID string, attempt int, options maildomain.WatchOptions, retryable bool) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	base := options.ReconnectMin
	for index := 1; index < attempt && base < options.ReconnectMax; index++ {
		if base > options.ReconnectMax/2 {
			base = options.ReconnectMax
			break
		}
		base *= 2
	}
	if base > options.ReconnectMax {
		base = options.ReconnectMax
	}
	if !retryable && base < options.ReconnectMax {
		base = options.ReconnectMax
	}
	digest := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", accountID, attempt)))
	jitterPermille := 900 + int(digest[0])%201 // deterministic 0.9x..1.1x jitter.
	delay := time.Duration(int64(base) * int64(jitterPermille) / 1000)
	if delay < options.ReconnectMin/2 {
		delay = options.ReconnectMin / 2
	}
	return delay
}

func sleepWatch(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
