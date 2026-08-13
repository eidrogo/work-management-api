package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProbeRequestsExactReadyPathOverNumericLoopback(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodGet || request.URL.Path != "/health/ready" || request.URL.RawQuery != "" || request.Host != listener.Addr().String() {
			t.Errorf("request=%s %s?%s host=%s", request.Method, request.URL.Path, request.URL.RawQuery, request.Host)
		}
		writer.WriteHeader(http.StatusOK)
	})}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	go func() { _ = server.Serve(listener) }()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := probe(ctx, listener.Addr().String()); err != nil {
		t.Fatal(err)
	}
}

func TestValidateAddressAcceptsNumericIPv4AndIPv6Loopback(t *testing.T) {
	t.Parallel()
	for address, expected := range map[string]string{
		"127.0.0.1:9090":          "http://127.0.0.1:9090/health/ready",
		"[::1]:9091":              "http://[::1]:9091/health/ready",
		"[::ffff:127.0.0.1]:9090": "http://127.0.0.1:9090/health/ready",
	} {
		endpoint, err := readinessEndpoint(address)
		if err != nil {
			t.Fatalf("address %q: %v", address, err)
		}
		if endpoint != expected {
			t.Fatalf("endpoint=%q want=%q", endpoint, expected)
		}
	}
}

func TestProbeRejectsNonNumericOrNonLoopbackAddressBeforeDial(t *testing.T) {
	t.Parallel()
	for _, address := range []string{"localhost:9090", "example.com:80", "192.0.2.1:80", "[2001:db8::1]:80", "127.0.0.1:0", "127.0.0.1:65536", "127.0.0.1", "127.0.0.1:80/path", "127.0.0.1:80?query", "user@127.0.0.1:80"} {
		if _, err := readinessEndpoint(address); err == nil {
			t.Errorf("address %q unexpectedly accepted", address)
		}
	}
}

func TestProbeRejectsRedirectAndIgnoresProxyEnvironment(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		http.Redirect(writer, request, "http://127.0.0.1:1/health/ready", http.StatusFound)
	})}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	go func() { _ = server.Serve(listener) }()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err = probe(ctx, listener.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "HTTP 302") {
		t.Fatalf("error=%v", err)
	}
}

func TestProbeTransportDisablesProxyAndCapsHeaders(t *testing.T) {
	t.Parallel()
	transport := probeTransport()
	if transport.Proxy != nil {
		t.Fatal("probe transport must not consult a proxy")
	}
	if transport.MaxResponseHeaderBytes != maxResponseHeaderSize || maxResponseHeaderSize != 16<<10 {
		t.Fatalf("maximum response headers=%d", transport.MaxResponseHeaderBytes)
	}
}

func TestProbeRejectsOversizedResponseHeaders(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = connection.Close() }()
		buffer := make([]byte, 4096)
		_, _ = connection.Read(buffer)
		_, _ = io.WriteString(connection, "HTTP/1.1 200 OK\r\nX-Oversized: "+strings.Repeat("x", maxResponseHeaderSize+1)+"\r\nContent-Length: 0\r\n\r\n")
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err = probe(ctx, listener.Addr().String()); err == nil || !strings.Contains(strings.ToLower(err.Error()), "header") {
		t.Fatalf("error=%v", err)
	}
}

func TestProbeRequires200AndBoundsResponseBody(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "not ready", status: http.StatusServiceUnavailable, body: "not ready", want: "HTTP 503"},
		{name: "oversized success body", status: http.StatusOK, body: strings.Repeat("x", maxResponseBodyBytes+1), want: "body exceeds"},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			listener, err := net.Listen("tcp4", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			})}
			t.Cleanup(func() {
				_ = server.Close()
				_ = listener.Close()
			})
			go func() { _ = server.Serve(listener) }()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = probe(ctx, listener.Addr().String())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}
}

var errPastSentinel = errors.New("read past sentinel")

type sentinelReader struct{ bytes int }

func (reader *sentinelReader) Read(payload []byte) (int, error) {
	remaining := maxResponseBodyBytes + 1 - reader.bytes
	if remaining <= 0 {
		return 0, errPastSentinel
	}
	if len(payload) > remaining {
		payload = payload[:remaining]
	}
	for index := range payload {
		payload[index] = 'x'
	}
	reader.bytes += len(payload)
	return len(payload), nil
}

func TestDrainBoundedNeverReadsPastSentinelByte(t *testing.T) {
	t.Parallel()
	reader := &sentinelReader{}
	err := drainBounded(reader)
	if err == nil || !strings.Contains(err.Error(), "body exceeds") {
		t.Fatalf("error=%v", err)
	}
	if reader.bytes != maxResponseBodyBytes+1 {
		t.Fatalf("reader bytes=%d want=%d", reader.bytes, maxResponseBodyBytes+1)
	}
}

func TestProbeHasBoundedTimeout(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			defer func() { _ = connection.Close() }()
			time.Sleep(2 * time.Second)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err = probe(ctx, listener.Addr().String()); err == nil || !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Fatalf("error=%v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("elapsed=%s", elapsed)
	}
}

func TestRunReturnsStableExitCodes(t *testing.T) {
	t.Parallel()
	var stderr bytes.Buffer
	if code := run(nil, &stderr); code != 2 || !strings.Contains(stderr.String(), "usage:") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
	stderr.Reset()
	if code := run([]string{"example.com:80"}, &stderr); code != 1 || !strings.Contains(stderr.String(), "numeric loopback") {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}

func TestRunReturnsSuccessForReadyListener(t *testing.T) {
	t.Parallel()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := &http.Server{Handler: http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	})}
	t.Cleanup(func() {
		_ = server.Close()
		_ = listener.Close()
	})
	go func() { _ = server.Serve(listener) }()
	var stderr bytes.Buffer
	if code := run([]string{listener.Addr().String()}, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("code=%d stderr=%q", code, stderr.String())
	}
}
