package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"
)

const (
	readinessPath         = "/health/ready"
	probeTimeout          = 2 * time.Second
	maxResponseHeaderSize = 16 << 10
	maxResponseBodyBytes  = 4 << 10
)

func main() {
	os.Exit(run(os.Args[1:], os.Stderr))
}

func run(arguments []string, stderr io.Writer) int {
	if len(arguments) != 1 {
		_, _ = fmt.Fprintln(stderr, "usage: healthcheck <numeric-loopback-host:port>")
		return 2
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeTimeout)
	defer cancel()
	if err := probe(ctx, arguments[0]); err != nil {
		_, _ = fmt.Fprintf(stderr, "readiness probe failed: %v\n", err)
		return 1
	}
	return 0
}

func probe(ctx context.Context, address string) error {
	endpoint, err := readinessEndpoint(address)
	if err != nil {
		return err
	}
	transport := probeTransport()
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return fmt.Errorf("construct readiness request: %w", err)
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("request readiness: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if err = drainBounded(response.Body); err != nil {
		return err
	}
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("readiness returned HTTP %d", response.StatusCode)
	}
	return nil
}

func probeTransport() *http.Transport {
	return &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: time.Second}).DialContext,
		DisableKeepAlives:      true,
		ResponseHeaderTimeout:  probeTimeout,
		MaxResponseHeaderBytes: maxResponseHeaderSize,
	}
}

func drainBounded(reader io.Reader) error {
	body, err := io.ReadAll(io.LimitReader(reader, maxResponseBodyBytes+1))
	if err != nil {
		return fmt.Errorf("read readiness response: %w", err)
	}
	if len(body) > maxResponseBodyBytes {
		return fmt.Errorf("readiness response body exceeds %d bytes", maxResponseBodyBytes)
	}
	return nil
}

func readinessEndpoint(address string) (string, error) {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return "", fmt.Errorf("invalid address: %w", err)
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return "", fmt.Errorf("address must use a numeric loopback host")
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return "", fmt.Errorf("address must use a valid TCP port")
	}
	return (&url.URL{Scheme: "http", Host: net.JoinHostPort(ip.String(), strconv.Itoa(port)), Path: readinessPath}).String(), nil
}
