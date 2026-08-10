package app

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-msgauth/dkim"
)

func (commands accountCommands) MailUnsubscribePreview(ctx context.Context, home, accountSelector, messageID, selector string) (account.Account, maildomain.UnsubscribePlan, int64, error) {
	accountValue, message, _, schemaVersion, err := commands.MailMessage(ctx, home, accountSelector, messageID)
	if err != nil {
		return account.Account{}, maildomain.UnsubscribePlan{}, schemaVersion, err
	}
	plan, err := unsubscribePlan(message, selector, true)
	if err != nil {
		return accountValue, plan, schemaVersion, mapUnsubscribeError(err)
	}
	return accountValue, plan, schemaVersion, nil
}

func (commands accountCommands) MailUnsubscribe(ctx context.Context, home, accountSelector, messageID, selector string) (account.Account, maildomain.UnsubscribePlan, maildomain.UnsubscribeResult, int64, error) {
	accountValue, message, source, schemaVersion, err := commands.MailMessage(ctx, home, accountSelector, messageID)
	if err != nil {
		return account.Account{}, maildomain.UnsubscribePlan{}, maildomain.UnsubscribeResult{}, schemaVersion, err
	}
	plan, err := unsubscribePlan(message, selector, false)
	if err != nil {
		return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, mapUnsubscribeError(err)
	}
	if plan.Selected == nil || message.Unsubscribe == nil {
		return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, mapUnsubscribeError(maildomain.ErrUnsubscribeSelection)
	}
	method, err := maildomain.SelectUnsubscribeMethod(*message.Unsubscribe, plan.Selected.ID)
	if err != nil {
		return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, mapUnsubscribeError(err)
	}

	switch method.Kind {
	case maildomain.UnsubscribeMethodOneClick:
		dkimDomain, verifyErr := commands.verifyUnsubscribeDKIM(ctx, source)
		if verifyErr != nil {
			return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, cli.WrapAppError(cli.ErrorSafety, "one-click unsubscribe requires a valid DKIM signature covering both unsubscribe headers", false, verifyErr)
		}
		client := commands.unsubscribe
		if client == nil {
			return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, cli.NewAppError(cli.ErrorConfiguration, "one-click unsubscribe transport is unavailable", false)
		}
		if err := client.UnsubscribeOneClick(ctx, method.URI); err != nil {
			return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, mapOneClickTransportError(err)
		}
		return accountValue, plan, maildomain.UnsubscribeResult{
			MessageID: message.ID, Method: method.Kind, Host: method.Host,
			Status: "confirmed", DKIMDomain: dkimDomain,
		}, schemaVersion, nil

	case maildomain.UnsubscribeMethodMailto:
		request := maildomain.SendRequest{
			To: method.MailtoTo, Subject: method.MailtoSubject, TextBody: method.MailtoBody,
			IdempotencyKey: maildomain.UnsubscribeIdempotencyKey(message.ID, method),
		}
		_, attempt, reused, sendSchemaVersion, sendErr := commands.MailSend(ctx, home, accountSelector, request)
		if sendSchemaVersion != 0 {
			schemaVersion = sendSchemaVersion
		}
		result := maildomain.UnsubscribeResult{
			MessageID: message.ID, Method: method.Kind, Host: method.Host,
			Status: string(attempt.Status), Reused: reused, AttemptID: attempt.ID,
		}
		if sendErr != nil {
			return accountValue, plan, result, schemaVersion, sendErr
		}
		return accountValue, plan, result, schemaVersion, nil
	default:
		return accountValue, plan, maildomain.UnsubscribeResult{}, schemaVersion, mapUnsubscribeError(maildomain.ErrInvalidUnsubscribe)
	}
}

func unsubscribePlan(message maildomain.Message, selector string, dryRun bool) (maildomain.UnsubscribePlan, error) {
	plan := maildomain.UnsubscribePlan{MessageID: message.ID, DryRun: dryRun}
	if message.Unsubscribe == nil {
		return plan, maildomain.ErrUnsubscribeUnavailable
	}
	capability := *message.Unsubscribe
	plan.Available = capability.Available
	plan.OneClickDeclared = capability.OneClickDeclared
	plan.SelectionRequired = capability.SelectionRequired
	plan.Alternatives = publicUnsubscribeMethods(capability.Methods)
	method, err := maildomain.SelectUnsubscribeMethod(capability, selector)
	if errors.Is(err, maildomain.ErrUnsubscribeSelection) && strings.TrimSpace(selector) == "" {
		plan.SelectionRequired = true
		return plan, nil
	}
	if err != nil {
		return plan, err
	}
	publicMethod := publicUnsubscribeMethod(method)
	plan.Selected = &publicMethod
	plan.SelectionRequired = false
	return plan, nil
}

func publicUnsubscribeMethods(values []maildomain.UnsubscribeMethod) []maildomain.UnsubscribeMethod {
	result := make([]maildomain.UnsubscribeMethod, 0, len(values))
	for _, value := range values {
		result = append(result, publicUnsubscribeMethod(value))
	}
	return result
}

func publicUnsubscribeMethod(value maildomain.UnsubscribeMethod) maildomain.UnsubscribeMethod {
	return maildomain.UnsubscribeMethod{ID: value.ID, Kind: value.Kind, Host: value.Host, DKIMRequired: value.DKIMRequired}
}

func (commands accountCommands) verifyUnsubscribeDKIM(ctx context.Context, source maildomain.ContentSource) (string, error) {
	if source == nil {
		return "", errors.New("raw message source is unavailable")
	}
	reader, writer := io.Pipe()
	writeResult := make(chan error, 1)
	go func() {
		err := source.WriteRaw(ctx, writer)
		_ = writer.CloseWithError(err)
		writeResult <- err
	}()
	lookup := commands.lookupTXT
	if lookup == nil {
		lookup = func(ctx context.Context, domain string) ([]string, error) {
			return net.DefaultResolver.LookupTXT(ctx, domain)
		}
	}
	verifications, verifyErr := dkim.VerifyWithOptions(reader, &dkim.VerifyOptions{
		MaxVerifications: 20,
		LookupTXT: func(domain string) ([]string, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return lookup(ctx, domain)
		},
	})
	_ = reader.Close()
	writeErr := <-writeResult
	if verifyErr != nil {
		return "", verifyErr
	}
	if writeErr != nil {
		return "", writeErr
	}
	for _, verification := range verifications {
		if verification == nil || verification.Err != nil {
			continue
		}
		signed := make(map[string]struct{}, len(verification.HeaderKeys))
		for _, key := range verification.HeaderKeys {
			signed[strings.ToLower(strings.TrimSpace(key))] = struct{}{}
		}
		_, unsubscribeSigned := signed["list-unsubscribe"]
		_, postSigned := signed["list-unsubscribe-post"]
		if unsubscribeSigned && postSigned {
			return strings.ToLower(strings.TrimSpace(verification.Domain)), nil
		}
	}
	return "", errors.New("no valid DKIM signature covers both unsubscribe headers")
}

func mapUnsubscribeError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrUnsubscribeUnavailable):
		return cli.WrapAppError(cli.ErrorNotFound, "message does not advertise a supported mailing-list unsubscribe method", false, err)
	case errors.Is(err, maildomain.ErrUnsubscribeSelection):
		return cli.WrapAppError(cli.ErrorUsage, "multiple unsubscribe methods are available; select one by its safe method ID", false, err)
	case errors.Is(err, maildomain.ErrInvalidUnsubscribe):
		return cli.WrapAppError(cli.ErrorUsage, "unsubscribe method selection is invalid or unsafe", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot resolve mailing-list unsubscribe metadata", false, err)
	}
}

func mapOneClickTransportError(err error) error {
	failure, ok := transport.AsFailure(err)
	if !ok {
		return cli.WrapAppError(cli.ErrorUnavailable, "one-click unsubscribe request failed", true, err)
	}
	if failure.Kind == transport.FailureConfiguration {
		return cli.WrapAppError(cli.ErrorSafety, "one-click unsubscribe endpoint violated the safe HTTPS request contract", false, err)
	}
	return cli.WrapAppError(cli.ErrorUnavailable, "one-click unsubscribe request did not complete successfully", failure.Retryable, err)
}
