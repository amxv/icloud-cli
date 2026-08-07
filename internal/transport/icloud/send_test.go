package icloud

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/amxv/icloud-cli/internal/transport"
)

func TestSendSMTPRequiresSTARTTLSAndStreamsEnvelopeWithHiddenBcc(t *testing.T) {
	server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true, AcceptSend: true})
	defer server.Close()
	client := New(Config{
		SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: time.Second,
		TLSConfig: server.ClientTLSConfig(),
	})
	raw := strings.Join([]string{
		"From: \"Ashray Sender\" <owner@icloud.com>",
		"To: primary@example.com",
		"Cc: visible@example.com",
		"Subject: Synthetic send",
		"Message-ID: <send@example.test>",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Synthetic body.",
	}, "\r\n") + "\r\n"
	result, err := client.SendSMTP(context.Background(), transport.AuthRequest{
		Username: "owner@icloud.com", Password: []byte("synthetic-password"),
	}, transport.SMTPSendRequest{
		Envelope: transport.SMTPEnvelope{
			From:       "owner@icloud.com",
			Recipients: []string{"primary@example.com", "visible@example.com", "hidden@example.com"},
		},
		Message: strings.NewReader(raw),
	})
	if err != nil || !result.Accepted {
		t.Fatalf("send result=%+v err=%v", result, err)
	}
	if !server.SawSTARTTLS() || !server.SawAuth() || !server.SawMutation() {
		t.Fatalf("starttls=%v auth=%v mutation=%v", server.SawSTARTTLS(), server.SawAuth(), server.SawMutation())
	}
	from, recipients := server.Envelope()
	if !strings.Contains(strings.ToLower(from), "owner@icloud.com") || len(recipients) != 3 || !strings.Contains(strings.ToLower(strings.Join(recipients, ",")), "hidden@example.com") {
		t.Fatalf("envelope from=%q recipients=%v", from, recipients)
	}
	message := string(server.Message())
	if !strings.Contains(message, "Ashray Sender") || !strings.Contains(message, "Synthetic body") || strings.Contains(strings.ToLower(message), "bcc:") || strings.Contains(message, "hidden@example.com") {
		t.Fatalf("captured message = %q", message)
	}
}

func TestSendSMTPFailsBeforeMutationWithoutSTARTTLS(t *testing.T) {
	server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: false, AuthOK: true, AcceptSend: true})
	defer server.Close()
	client := New(Config{
		SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: time.Second,
		TLSConfig: server.ClientTLSConfig(),
	})
	_, err := client.SendSMTP(context.Background(), transport.AuthRequest{
		Username: "owner@icloud.com", Password: []byte("synthetic-password"),
	}, transport.SMTPSendRequest{
		Envelope: transport.SMTPEnvelope{From: "owner@icloud.com", Recipients: []string{"recipient@example.com"}},
		Message:  strings.NewReader("From: owner@icloud.com\r\nTo: recipient@example.com\r\n\r\nbody\r\n"),
	})
	var sendErr *transport.SMTPSendError
	if !errors.As(err, &sendErr) || sendErr.Outcome != transport.SMTPSendFailed || sendErr.Failure.Stage != transport.StageSMTPStartTLS {
		t.Fatalf("STARTTLS send error = %#v", err)
	}
	if server.SawAuth() || server.SawMutation() {
		t.Fatalf("auth=%v mutation=%v", server.SawAuth(), server.SawMutation())
	}
}

func TestSendSMTPRecordsPostDataDisconnectAsUnknownAndFinalRejectionAsFailed(t *testing.T) {
	tests := []struct {
		name    string
		options smtpServerOptions
		want    transport.SMTPSendOutcome
	}{
		{name: "disconnect", options: smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true, AcceptSend: true, DisconnectAfterData: true}, want: transport.SMTPSendUnknown},
		{name: "rejection", options: smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true, AcceptSend: true, RejectAfterData: true}, want: transport.SMTPSendFailed},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			server := newSMTPServer(t, testCase.options)
			defer server.Close()
			client := New(Config{
				SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: time.Second,
				TLSConfig: server.ClientTLSConfig(),
			})
			_, err := client.SendSMTP(context.Background(), transport.AuthRequest{
				Username: "owner@icloud.com", Password: []byte("synthetic-password"),
			}, transport.SMTPSendRequest{
				Envelope: transport.SMTPEnvelope{From: "owner@icloud.com", Recipients: []string{"recipient@example.com"}},
				Message:  strings.NewReader("From: owner@icloud.com\r\nTo: recipient@example.com\r\n\r\nbody\r\n"),
			})
			var sendErr *transport.SMTPSendError
			if !errors.As(err, &sendErr) || sendErr.Outcome != testCase.want || sendErr.Failure.Stage != transport.StageSMTPAccept {
				t.Fatalf("send error = %#v", err)
			}
			if !server.SawSTARTTLS() || !server.SawAuth() || len(server.Message()) == 0 {
				t.Fatalf("starttls=%v auth=%v message=%q", server.SawSTARTTLS(), server.SawAuth(), server.Message())
			}
		})
	}
}

func TestSendSMTPCancellationBeforeAndDuringDataPreservesOutcomeBoundary(t *testing.T) {
	t.Run("before data transmission is safely failed", func(t *testing.T) {
		server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true, AcceptSend: true, DelayDataResponse: time.Second})
		defer server.Close()
		client := New(Config{SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: 5 * time.Second, TLSConfig: server.ClientTLSConfig()})
		ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
		defer cancel()
		started := time.Now()
		_, err := client.SendSMTP(ctx, transport.AuthRequest{Username: "owner@icloud.com", Password: []byte("synthetic-password")}, transport.SMTPSendRequest{
			Envelope: transport.SMTPEnvelope{From: "owner@icloud.com", Recipients: []string{"recipient@example.com"}},
			Message:  strings.NewReader("From: owner@icloud.com\r\nTo: recipient@example.com\r\n\r\nbody\r\n"),
		})
		var sendErr *transport.SMTPSendError
		if !errors.As(err, &sendErr) || sendErr.Outcome != transport.SMTPSendFailed {
			t.Fatalf("pre-DATA cancellation = %#v", err)
		}
		if time.Since(started) > 500*time.Millisecond {
			t.Fatalf("pre-DATA cancellation was not prompt: %s", time.Since(started))
		}
	})

	t.Run("during data transmission is unknown", func(t *testing.T) {
		server := newSMTPServer(t, smtpServerOptions{AdvertiseSTARTTLS: true, AuthOK: true, AcceptSend: true})
		defer server.Close()
		client := New(Config{SMTPHost: "127.0.0.1", SMTPPort: server.Port(), StageTimeout: 5 * time.Second, TLSConfig: server.ClientTLSConfig()})
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		message := &cancelOnFirstRead{reader: strings.NewReader("From: owner@icloud.com\r\nTo: recipient@example.com\r\n\r\nbody\r\n"), cancel: cancel}
		started := time.Now()
		_, err := client.SendSMTP(ctx, transport.AuthRequest{Username: "owner@icloud.com", Password: []byte("synthetic-password")}, transport.SMTPSendRequest{
			Envelope: transport.SMTPEnvelope{From: "owner@icloud.com", Recipients: []string{"recipient@example.com"}},
			Message:  message,
		})
		var sendErr *transport.SMTPSendError
		if !errors.As(err, &sendErr) || sendErr.Outcome != transport.SMTPSendUnknown {
			t.Fatalf("mid-DATA cancellation = %#v", err)
		}
		if time.Since(started) > 500*time.Millisecond {
			t.Fatalf("mid-DATA cancellation was not prompt: %s", time.Since(started))
		}
	})
}

type cancelOnFirstRead struct {
	reader *strings.Reader
	cancel context.CancelFunc
	done   bool
}

func (reader *cancelOnFirstRead) Read(buffer []byte) (int, error) {
	if !reader.done {
		reader.done = true
		reader.cancel()
	}
	return reader.reader.Read(buffer)
}
