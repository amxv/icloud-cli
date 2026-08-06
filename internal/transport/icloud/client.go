package icloud

import (
	"context"
	"crypto/tls"
	"errors"
	"mime"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-message/charset"
	"github.com/emersion/go-sasl"
	smtp "github.com/emersion/go-smtp"
)

const (
	DefaultIMAPHost = "imap.mail.me.com"
	DefaultIMAPPort = "993"
	DefaultSMTPHost = "smtp.mail.me.com"
	DefaultSMTPPort = "587"
)

type DialContextFunc func(context.Context, string, string) (net.Conn, error)

type Config struct {
	IMAPHost     string
	IMAPPort     string
	SMTPHost     string
	SMTPPort     string
	StageTimeout time.Duration
	TLSConfig    *tls.Config
	DialContext  DialContextFunc
}

type Client struct {
	config Config
	dial   DialContextFunc
}

func New(config Config) *Client {
	if strings.TrimSpace(config.IMAPHost) == "" {
		config.IMAPHost = DefaultIMAPHost
	}
	if strings.TrimSpace(config.IMAPPort) == "" {
		config.IMAPPort = DefaultIMAPPort
	}
	if strings.TrimSpace(config.SMTPHost) == "" {
		config.SMTPHost = DefaultSMTPHost
	}
	if strings.TrimSpace(config.SMTPPort) == "" {
		config.SMTPPort = DefaultSMTPPort
	}
	if config.StageTimeout <= 0 {
		config.StageTimeout = 10 * time.Second
	}
	dial := config.DialContext
	if dial == nil {
		dialer := &net.Dialer{Timeout: config.StageTimeout}
		dial = dialer.DialContext
	}
	return &Client{config: config, dial: dial}
}

func (client *Client) ValidateIMAP(ctx context.Context, request transport.AuthRequest) (transport.IMAPResult, error) {
	address := net.JoinHostPort(client.config.IMAPHost, client.config.IMAPPort)
	plainConn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.IMAPResult{}, classifyConnectError(err)
	}
	defer plainConn.Close()

	tlsConn := tls.Client(plainConn, client.tlsConfig(client.config.IMAPHost))
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, tlsConn.Handshake); err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageTLS, err)
	}

	imapClient := imapclient.New(tlsConn, &imapclient.Options{
		WordDecoder: &mime.WordDecoder{CharsetReader: charset.Reader},
	})
	defer closeIMAPClient(plainConn, imapClient)

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, imapClient.WaitGreeting); err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPGreeting, err)
	}
	preAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	if !preAuthCaps.Has(imap.CapIMAP4rev1) && !preAuthCaps.Has(imap.CapIMAP4rev2) {
		return transport.IMAPResult{}, transport.NewFailure(transport.StageIMAPCapability, transport.FailureUnavailable, false, errors.New("required IMAP capability missing"))
	}

	password := string(request.Password)
	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Login(request.Username, password).Wait()
	}); err != nil {
		return transport.IMAPResult{}, classifyAuthenticationFailure(transport.StageIMAPAuth, err)
	}

	postAuthCaps, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (imap.CapSet, error) {
		return imapClient.Capability().Wait()
	})
	if err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPCapability, err)
	}
	mailboxes, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() ([]*imap.ListData, error) {
		return imapClient.List("", "*", nil).Collect()
	})
	if err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPList, err)
	}
	status, err := runValueWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() (*imap.StatusData, error) {
		return imapClient.Status("INBOX", &imap.StatusOptions{
			NumMessages: true,
			NumUnseen:   true,
			UIDNext:     true,
			UIDValidity: true,
		}).Wait()
	})
	if err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPStatus, err)
	}

	result := transport.IMAPResult{
		Capabilities: capabilityStrings(postAuthCaps),
		Mailboxes:    len(mailboxes),
		Inbox: transport.MailboxStatus{
			Mailbox:     "INBOX",
			UIDNext:     uint32(status.UIDNext),
			UIDValidity: status.UIDValidity,
		},
	}
	if status.NumMessages != nil {
		result.Inbox.Messages = *status.NumMessages
	}
	if status.NumUnseen != nil {
		result.Inbox.Unseen = *status.NumUnseen
	}

	if err := runWithDeadline(ctx, tlsConn, client.config.StageTimeout, func() error {
		return imapClient.Logout().Wait()
	}); err != nil {
		return transport.IMAPResult{}, classifyFailure(transport.StageIMAPLogout, err)
	}
	return result, nil
}

func (client *Client) ValidateSMTP(ctx context.Context, request transport.AuthRequest) (transport.SMTPResult, error) {
	address := net.JoinHostPort(client.config.SMTPHost, client.config.SMTPPort)
	conn, err := client.dial(ctx, "tcp", address)
	if err != nil {
		return transport.SMTPResult{}, classifyConnectError(err)
	}
	defer conn.Close()

	var smtpClient *smtp.Client
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		var startTLSErr error
		smtpClient, startTLSErr = smtp.NewClientStartTLS(conn, client.tlsConfig(client.config.SMTPHost))
		return startTLSErr
	}); err != nil {
		return transport.SMTPResult{}, classifyFailure(transport.StageSMTPStartTLS, err)
	}
	defer smtpClient.Close()
	smtpClient.CommandTimeout = client.config.StageTimeout
	smtpClient.SubmissionTimeout = client.config.StageTimeout

	password := string(request.Password)
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, func() error {
		return smtpClient.Auth(sasl.NewPlainClient("", request.Username, password))
	}); err != nil {
		return transport.SMTPResult{}, classifyAuthenticationFailure(transport.StageSMTPAuth, err)
	}

	capabilities := make([]string, 0, 8)
	for _, capability := range []string{"8BITMIME", "AUTH", "CHUNKING", "DSN", "PIPELINING", "REQUIRETLS", "SIZE", "SMTPUTF8"} {
		if ok, _ := smtpClient.Extension(capability); ok {
			capabilities = append(capabilities, capability)
		}
	}
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, smtpClient.Noop); err != nil {
		return transport.SMTPResult{}, classifyFailure(transport.StageSMTPNoop, err)
	}
	if err := runWithDeadline(ctx, conn, client.config.StageTimeout, smtpClient.Quit); err != nil {
		return transport.SMTPResult{}, classifyFailure(transport.StageSMTPQuit, err)
	}
	return transport.SMTPResult{Capabilities: transport.SanitizeCapabilities(capabilities)}, nil
}

func (client *Client) tlsConfig(host string) *tls.Config {
	var config *tls.Config
	if client.config.TLSConfig != nil {
		config = client.config.TLSConfig.Clone()
	} else {
		config = &tls.Config{}
	}
	if config.ServerName == "" {
		config.ServerName = host
	}
	if config.MinVersion == 0 || config.MinVersion < tls.VersionTLS12 {
		config.MinVersion = tls.VersionTLS12
	}
	return config
}

func capabilityStrings(capabilities imap.CapSet) []string {
	values := make([]string, 0, len(capabilities))
	for capability := range capabilities {
		values = append(values, string(capability))
	}
	sort.Strings(values)
	return transport.SanitizeCapabilities(values)
}

func classifyConnectError(err error) error {
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return transport.NewFailure(transport.StageDNS, transport.FailureUnavailable, true, err)
	}
	return classifyFailure(transport.StageConnect, err)
}

func classifyAuthenticationFailure(stage transport.Stage, err error) error {
	if isTimeout(err) {
		return transport.NewFailure(transport.StageTimeout, transport.FailureUnavailable, true, err)
	}
	return transport.NewFailure(stage, transport.FailureAuthentication, false, err)
}

func classifyFailure(stage transport.Stage, err error) error {
	if isTimeout(err) {
		return transport.NewFailure(transport.StageTimeout, transport.FailureUnavailable, true, err)
	}
	return transport.NewFailure(stage, transport.FailureUnavailable, retryableNetworkError(err), err)
}

func isTimeout(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netError net.Error
	return errors.As(err, &netError) && netError.Timeout()
}

func retryableNetworkError(err error) bool {
	var netError net.Error
	return errors.As(err, &netError) && (netError.Timeout() || netError.Temporary())
}

func runWithDeadline(ctx context.Context, conn net.Conn, timeout time.Duration, fn func() error) error {
	_, err := runValueWithDeadline(ctx, conn, timeout, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}

func runValueWithDeadline[T any](ctx context.Context, conn net.Conn, timeout time.Duration, fn func() (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(timeout)
	if contextDeadline, ok := ctx.Deadline(); ok && contextDeadline.Before(deadline) {
		deadline = contextDeadline
	}
	type operationResult struct {
		value T
		err   error
	}
	result := make(chan operationResult, 1)
	go func() {
		value, err := fn()
		result <- operationResult{value: value, err: err}
	}()
	timer := time.NewTimer(time.Until(deadline))
	defer timer.Stop()
	select {
	case value := <-result:
		return value.value, value.err
	case <-ctx.Done():
		_ = conn.Close()
		return zero, ctx.Err()
	case <-timer.C:
		_ = conn.Close()
		return zero, context.DeadlineExceeded
	}
}

func closeIMAPClient(conn net.Conn, client *imapclient.Client) {
	_ = conn.Close()
	_ = client.Close()
}
