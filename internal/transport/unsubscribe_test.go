package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHTTPUnsubscriberSendsExactCredentialFreeOneClickPOST(t *testing.T) {
	var observed string
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		observed = fmt.Sprintf("%s|%s|%s|cookie=%s|auth=%s|referer=%s|body=%s", request.Method, request.URL.Path, request.Header.Get("Content-Type"), request.Header.Get("Cookie"), request.Header.Get("Authorization"), request.Header.Get("Referer"), body)
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	client := NewHTTPUnsubscriberWithTransport(server.Client().Transport)
	if err := client.UnsubscribeOneClick(context.Background(), server.URL+"/u/secret-token?opaque=yes"); err != nil {
		t.Fatal(err)
	}
	if observed != "POST|/u/secret-token|application/x-www-form-urlencoded|cookie=|auth=|referer=|body=List-Unsubscribe=One-Click" {
		t.Fatalf("observed=%q", observed)
	}
}

func TestHTTPUnsubscriberRejectsRedirectDowngradeOversizeAndCancellation(t *testing.T) {
	t.Run("private-address", func(t *testing.T) {
		err := NewHTTPUnsubscriber().UnsubscribeOneClick(context.Background(), "https://127.0.0.1/u")
		failure, ok := AsFailure(err)
		if !ok || failure.Kind != FailureConfiguration || failure.Retryable {
			t.Fatalf("failure=%#v err=%v", failure, err)
		}
	})
	t.Run("http", func(t *testing.T) {
		err := NewHTTPUnsubscriber().UnsubscribeOneClick(context.Background(), "http://example.test/u")
		failure, ok := AsFailure(err)
		if !ok || failure.Kind != FailureConfiguration || failure.Retryable {
			t.Fatalf("failure=%#v err=%v", failure, err)
		}
	})
	t.Run("redirect", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			http.Redirect(writer, request, "http://attacker.example.test/steal", http.StatusFound)
		}))
		defer server.Close()
		err := NewHTTPUnsubscriberWithTransport(server.Client().Transport).UnsubscribeOneClick(context.Background(), server.URL+"/u/token")
		failure, ok := AsFailure(err)
		if !ok || failure.Kind != FailureConfiguration || failure.Retryable {
			t.Fatalf("failure=%#v err=%v", failure, err)
		}
	})
	t.Run("oversize", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			_, _ = io.WriteString(writer, strings.Repeat("x", int(maxOneClickResponseBytes)+1))
		}))
		defer server.Close()
		err := NewHTTPUnsubscriberWithTransport(server.Client().Transport).UnsubscribeOneClick(context.Background(), server.URL)
		failure, ok := AsFailure(err)
		if !ok || failure.Retryable {
			t.Fatalf("failure=%#v err=%v", failure, err)
		}
	})
	t.Run("cancel", func(t *testing.T) {
		server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			time.Sleep(200 * time.Millisecond)
			writer.WriteHeader(http.StatusNoContent)
		}))
		server.TLS = &tls.Config{MinVersion: tls.VersionTLS12}
		server.StartTLS()
		defer server.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
		defer cancel()
		err := NewHTTPUnsubscriberWithTransport(server.Client().Transport).UnsubscribeOneClick(ctx, server.URL)
		failure, ok := AsFailure(err)
		if !ok || !failure.Retryable {
			t.Fatalf("failure=%#v err=%v", failure, err)
		}
	})
}
