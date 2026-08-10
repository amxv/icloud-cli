package transport

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxOneClickResponseBytes int64 = 64 << 10

var errUnsafeUnsubscribeAddress = errors.New("one-click endpoint resolves to a non-public address")

type HTTPUnsubscriber struct {
	transport http.RoundTripper
}

func NewHTTPUnsubscriber() *HTTPUnsubscriber {
	dialer := &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	return &HTTPUnsubscriber{transport: &http.Transport{
		Proxy: nil,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			return dialPublicUnsubscribeEndpoint(ctx, dialer, network, address)
		},
		TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:   15 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,
		ExpectContinueTimeout: time.Second,
	}}
}

func NewHTTPUnsubscriberWithTransport(roundTripper http.RoundTripper) *HTTPUnsubscriber {
	return &HTTPUnsubscriber{transport: roundTripper}
}

func (client *HTTPUnsubscriber) UnsubscribeOneClick(ctx context.Context, target string) error {
	parsed, err := url.Parse(strings.TrimSpace(target))
	if err != nil || parsed == nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return NewFailure(StageHTTPUnsubscribe, FailureConfiguration, false, errors.New("invalid one-click HTTPS endpoint"))
	}
	body := strings.NewReader("List-Unsubscribe=One-Click")
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, parsed.String(), body)
	if err != nil {
		return NewFailure(StageHTTPUnsubscribe, FailureConfiguration, false, err)
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/plain, */*;q=0.1")
	request.Header.Set("User-Agent", "icloud-cli-mail-unsubscribe")
	// Do not inherit cookies, Authorization, Referer, browser/session state, or
	// message-provided headers. The RFC 8058 POST is intentionally context-free.
	request.Header.Del("Cookie")
	request.Header.Del("Authorization")
	request.Header.Del("Proxy-Authorization")
	request.Header.Del("Referer")

	roundTripper := client.transport
	if roundTripper == nil {
		roundTripper = NewHTTPUnsubscriber().transport
	}
	httpClient := &http.Client{
		Transport: roundTripper,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := httpClient.Do(request)
	if err != nil {
		if errors.Is(err, errUnsafeUnsubscribeAddress) {
			return NewFailure(StageHTTPUnsubscribe, FailureConfiguration, false, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return NewFailure(StageHTTPUnsubscribe, FailureUnavailable, true, err)
		}
		return NewFailure(StageHTTPUnsubscribe, FailureUnavailable, true, err)
	}
	defer response.Body.Close()

	if response.StatusCode >= 300 && response.StatusCode < 400 {
		return NewFailure(StageHTTPUnsubscribe, FailureConfiguration, false, fmt.Errorf("one-click endpoint returned redirect status %d", response.StatusCode))
	}
	limited := io.LimitReader(response.Body, maxOneClickResponseBytes+1)
	read, readErr := io.Copy(io.Discard, limited)
	if readErr != nil {
		return NewFailure(StageHTTPUnsubscribe, FailureUnavailable, true, readErr)
	}
	if read > maxOneClickResponseBytes {
		return NewFailure(StageHTTPUnsubscribe, FailureUnavailable, false, errors.New("one-click response exceeds bounded limit"))
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return NewFailure(StageHTTPUnsubscribe, FailureUnavailable, response.StatusCode >= 500, fmt.Errorf("one-click endpoint status %d", response.StatusCode))
	}
	return nil
}

func dialPublicUnsubscribeEndpoint(ctx context.Context, dialer *net.Dialer, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil || strings.TrimSpace(host) == "" || strings.TrimSpace(port) == "" {
		return nil, fmt.Errorf("%w: invalid endpoint address", errUnsafeUnsubscribeAddress)
	}
	if dialer == nil {
		dialer = &net.Dialer{Timeout: 15 * time.Second, KeepAlive: 30 * time.Second}
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(addresses) == 0 {
		return nil, fmt.Errorf("resolve one-click endpoint %s: no addresses", host)
	}
	var lastErr error
	for _, resolved := range addresses {
		if !publicUnsubscribeIP(resolved.IP) {
			continue
		}
		conn, dialErr := dialer.DialContext(ctx, network, net.JoinHostPort(resolved.IP.String(), port))
		if dialErr == nil {
			return conn, nil
		}
		lastErr = dialErr
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("%w: %s", errUnsafeUnsubscribeAddress, host)
}

func publicUnsubscribeIP(address net.IP) bool {
	if address == nil || address.IsUnspecified() || address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() || address.IsMulticast() {
		return false
	}
	return address.IsGlobalUnicast()
}
