package app

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/amxv/icloud-cli/internal/account"
	"github.com/amxv/icloud-cli/internal/cli"
	maildomain "github.com/amxv/icloud-cli/internal/mail"
	"github.com/amxv/icloud-cli/internal/secret"
	"github.com/amxv/icloud-cli/internal/store"
)

func (commands accountCommands) resolveOutboundSMIMEPlan(
	ctx context.Context,
	state *store.Store,
	accountID string,
	sender account.MailIdentity,
	request maildomain.SendRequest,
) (maildomain.SendRequest, error) {
	mode, err := maildomain.NormalizeSMIMEComposeMode(request.SMIMEMode)
	if err != nil {
		return maildomain.SendRequest{}, mapSMIMEOutboundError(err)
	}
	request.SMIMEMode = mode
	if mode == maildomain.SMIMEComposeNone {
		request.SMIMEPlan = nil
		return request, nil
	}
	identity, err := state.SMIMEIdentities().Resolve(ctx, accountID, sender.ID)
	if err != nil {
		if errors.Is(err, maildomain.ErrSMIMEIdentityNotFound) {
			return maildomain.SendRequest{}, cli.NewAppError(cli.ErrorConfiguration, "selected sender identity has no configured S/MIME identity", false)
		}
		return maildomain.SendRequest{}, mapSMIMEIdentityError(err)
	}
	certificate, err := state.SMIMECertificates().Resolve(ctx, accountID, identity.CertificateID)
	if err != nil {
		return maildomain.SendRequest{}, mapSMIMECertificateError(err)
	}
	cert, err := x509.ParseCertificate(certificate.DER)
	if err != nil {
		return maildomain.SendRequest{}, cli.WrapAppError(cli.ErrorInternal, "stored sender S/MIME certificate is invalid", false, err)
	}
	now := commands.timeNow()
	if mode.Signs() && !maildomain.SMIMESignerCertificateUsable(cert, sender.Address, now) {
		return maildomain.SendRequest{}, cli.NewAppError(cli.ErrorConfiguration, "selected sender S/MIME certificate is not currently usable for email signing", false)
	}
	if mode.Encrypts() && !maildomain.SMIMEEncryptionCertificateUsableForSelf(cert, sender.Address, now) {
		return maildomain.SendRequest{}, cli.NewAppError(cli.ErrorConfiguration, "selected sender S/MIME certificate is not currently usable for encrypted self-copy access", false)
	}
	plan := maildomain.SMIMEOutboundPlan{
		Mode: mode, SMIMEIdentityID: identity.ID,
		SenderCertificateID: certificate.ID, SenderFingerprintSHA256: certificate.FingerprintSHA256,
	}
	if mode.Encrypts() {
		policy, policyErr := commands.inboundSMIMEPolicy(ctx, state, accountID)
		if policyErr != nil {
			return maildomain.SendRequest{}, cli.WrapAppError(cli.ErrorInternal, "cannot read S/MIME certificate trust policy", false, policyErr)
		}
		materials, materialErr := state.SMIMECertificates().Materials(ctx, accountID)
		if materialErr != nil {
			return maildomain.SendRequest{}, mapSMIMECertificateError(materialErr)
		}
		seenEmails := make(map[string]struct{}, len(request.EnvelopeRecipients()))
		for _, recipient := range request.EnvelopeRecipients() {
			email := strings.ToLower(strings.TrimSpace(recipient))
			if _, duplicate := seenEmails[email]; duplicate {
				continue
			}
			seenEmails[email] = struct{}{}
			if strings.EqualFold(email, sender.Address) {
				plan.RecipientCertificates = append(plan.RecipientCertificates, maildomain.SMIMERecipientCertificate{
					Email: email, CertificateID: certificate.ID, FingerprintSHA256: certificate.FingerprintSHA256,
				})
				continue
			}
			scoped := false
			usable := 0
			for _, material := range materials {
				if !strings.EqualFold(strings.TrimSpace(material.Email), email) {
					continue
				}
				scoped = true
				if !maildomain.SMIMERecipientCertificateUsable(material.Certificate, email, policy, now) {
					continue
				}
				metadata := maildomain.SMIMECertificateFromX509(material.Certificate)
				plan.RecipientCertificates = append(plan.RecipientCertificates, maildomain.SMIMERecipientCertificate{
					Email: email, CertificateID: material.ID, FingerprintSHA256: metadata.FingerprintSHA256,
				})
				usable++
			}
			if usable == 0 {
				if !scoped {
					return maildomain.SendRequest{}, cli.WrapAppError(cli.ErrorSafety, fmt.Sprintf("S/MIME encryption requires a certificate for recipient %s", email), false, maildomain.ErrSMIMERecipientCertificateMissing)
				}
				return maildomain.SendRequest{}, cli.WrapAppError(cli.ErrorSafety, fmt.Sprintf("S/MIME encryption has no currently trusted usable certificate for recipient %s", email), false, maildomain.ErrSMIMERecipientCertificateUntrusted)
			}
		}
	}
	plan, err = maildomain.NormalizeSMIMEOutboundPlan(plan, mode)
	if err != nil {
		return maildomain.SendRequest{}, mapSMIMEOutboundError(err)
	}
	if request.SMIMEPlan != nil {
		reviewed, reviewedErr := maildomain.NormalizeSMIMEOutboundPlan(*request.SMIMEPlan, mode)
		if reviewedErr != nil || !reflect.DeepEqual(reviewed, plan) {
			return maildomain.SendRequest{}, cli.NewAppError(cli.ErrorSafety, "S/MIME certificate coverage changed after preview; review the message again before sending", false)
		}
	}
	request.SMIMEPlan = &plan
	return request, nil
}

func (commands accountCommands) resolveOutboundSMIMEMaterial(
	ctx context.Context,
	state *store.Store,
	accountID string,
	request maildomain.SendRequest,
) (maildomain.SMIMEOutboundMaterial, error) {
	if request.SMIMEMode == maildomain.SMIMEComposeNone || request.SMIMEPlan == nil {
		return maildomain.SMIMEOutboundMaterial{}, nil
	}
	plan, err := maildomain.NormalizeSMIMEOutboundPlan(*request.SMIMEPlan, request.SMIMEMode)
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMEOutboundError(err)
	}
	identity, err := state.SMIMEIdentities().Resolve(ctx, accountID, plan.SMIMEIdentityID)
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMEIdentityError(err)
	}
	if identity.CertificateID != plan.SenderCertificateID || !strings.EqualFold(identity.FingerprintSHA256, plan.SenderFingerprintSHA256) {
		return maildomain.SMIMEOutboundMaterial{}, cli.NewAppError(cli.ErrorSafety, "sender S/MIME identity changed after planning; review the message again", false)
	}
	certificateValue, err := state.SMIMECertificates().Resolve(ctx, accountID, identity.CertificateID)
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMECertificateError(err)
	}
	senderCert, err := x509.ParseCertificate(certificateValue.DER)
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, cli.WrapAppError(cli.ErrorInternal, "stored sender S/MIME certificate is invalid", false, err)
	}
	manager := secret.NewManager(commands.keyring)
	var keyBytes []byte
	if identity.PrivateKeyScheme == account.CredentialFile {
		keyBytes, err = readSMIMEPrivateKeyFile(ctx, strings.TrimPrefix(identity.PrivateKeyReference, "file:"))
	} else {
		keyBytes, err = manager.Resolve(ctx, account.Credential{Scheme: identity.PrivateKeyScheme, Reference: identity.PrivateKeyReference, Configured: true})
	}
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMEPrivateKeyError(err)
	}
	defer zeroBytes(keyBytes)
	privateKey, err := maildomain.ParseSMIMEPrivateKey(keyBytes)
	if err != nil {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMEPrivateKeyError(err)
	}
	if !maildomain.SMIMEPrivateKeyMatchesCertificate(privateKey, senderCert) {
		return maildomain.SMIMEOutboundMaterial{}, mapSMIMEPrivateKeyError(maildomain.ErrSMIMEPrivateKeyMismatch)
	}
	material := maildomain.SMIMEOutboundMaterial{Plan: plan, SignerCertificate: senderCert, SignerPrivateKey: privateKey}
	if plan.Mode.Encrypts() {
		material.EncryptionCertificates = append(material.EncryptionCertificates, senderCert)
		seen := map[string]struct{}{strings.ToLower(plan.SenderFingerprintSHA256): {}}
		for _, coverage := range plan.RecipientCertificates {
			if _, duplicate := seen[strings.ToLower(coverage.FingerprintSHA256)]; duplicate {
				continue
			}
			value, resolveErr := state.SMIMECertificates().Resolve(ctx, accountID, coverage.CertificateID)
			if resolveErr != nil || !strings.EqualFold(value.Email, coverage.Email) || !strings.EqualFold(value.FingerprintSHA256, coverage.FingerprintSHA256) {
				return maildomain.SMIMEOutboundMaterial{}, cli.NewAppError(cli.ErrorSafety, "recipient S/MIME certificate coverage changed after planning; review the message again", false)
			}
			cert, parseErr := x509.ParseCertificate(value.DER)
			if parseErr != nil {
				return maildomain.SMIMEOutboundMaterial{}, cli.WrapAppError(cli.ErrorInternal, "stored recipient S/MIME certificate is invalid", false, parseErr)
			}
			material.EncryptionCertificates = append(material.EncryptionCertificates, cert)
			seen[strings.ToLower(coverage.FingerprintSHA256)] = struct{}{}
		}
	}
	return material, nil
}

func mapSMIMEOutboundError(err error) error {
	switch {
	case errors.Is(err, maildomain.ErrSMIMERecipientCertificateMissing), errors.Is(err, maildomain.ErrSMIMERecipientCertificateUntrusted):
		return cli.WrapAppError(cli.ErrorSafety, "S/MIME encryption certificate coverage is incomplete", false, err)
	case errors.Is(err, maildomain.ErrSMIMEOutboundKeyUnavailable):
		return cli.WrapAppError(cli.ErrorConfiguration, "S/MIME sender private key is unavailable", false, err)
	case errors.Is(err, maildomain.ErrSMIMEOutboundUnsupported):
		return cli.WrapAppError(cli.ErrorUnavailable, "S/MIME outbound cryptography is unsupported for the selected certificate or algorithm", false, err)
	case errors.Is(err, maildomain.ErrSMIMEOutboundInvalid):
		return cli.WrapAppError(cli.ErrorUsage, "S/MIME outbound request is invalid", false, err)
	default:
		return cli.WrapAppError(cli.ErrorInternal, "cannot prepare outbound S/MIME", false, err)
	}
}
