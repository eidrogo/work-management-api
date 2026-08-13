package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestServeEndpointsShutsDownBothOnCancellation(t *testing.T) {
	publicListener := listenLoopback(t)
	adminListener := listenLoopback(t)
	publicAddress := publicListener.Addr().String()
	adminAddress := adminListener.Addr().String()

	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- serveEndpoints(ctx, time.Second,
			serverEndpoint{name: "public", server: testHTTPServer("public"), listener: publicListener},
			serverEndpoint{name: "admin", server: testHTTPServer("admin"), listener: adminListener},
		)
	}()

	if body := getBody(t, publicAddress); body != "public" {
		t.Fatalf("public body=%q", body)
	}
	if body := getBody(t, adminAddress); body != "admin" {
		t.Fatalf("admin body=%q", body)
	}
	cancel()
	if err := receiveError(t, result); err != nil {
		t.Fatalf("serve endpoints: %v", err)
	}
	assertListenerClosed(t, publicAddress)
	assertListenerClosed(t, adminAddress)
}

func TestServeEndpointsShutsDownSiblingAfterListenerFailure(t *testing.T) {
	healthyListener := listenLoopback(t)
	healthyAddress := healthyListener.Addr().String()
	failedListener := listenLoopback(t)
	if err := failedListener.Close(); err != nil {
		t.Fatal(err)
	}

	err := serveEndpoints(context.Background(), time.Second,
		serverEndpoint{name: "healthy", server: testHTTPServer("healthy"), listener: healthyListener},
		serverEndpoint{name: "failed", server: testHTTPServer("failed"), listener: failedListener},
	)
	if err == nil || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("error=%v", err)
	}
	assertListenerClosed(t, healthyAddress)
}

func TestServeEndpointsForcesCloseAfterShutdownTimeout(t *testing.T) {
	listener := listenLoopback(t)
	address := listener.Addr().String()
	entered := make(chan struct{})
	release := make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "released")
	})}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() {
		result <- serveEndpoints(ctx, 25*time.Millisecond, serverEndpoint{name: "blocked", server: server, listener: listener})
	}()
	clientResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + address)
		if err == nil {
			_ = response.Body.Close()
		}
		clientResult <- err
	}()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("handler did not start")
	}
	cancel()
	err := receiveError(t, result)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error=%v", err)
	}
	assertListenerClosed(t, address)
	select {
	case <-clientResult:
	case <-time.After(time.Second):
		t.Fatal("forced close did not release client")
	}
	close(release)
}

func listenLoopback(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	return listener
}

func testHTTPServer(body string) *http.Server {
	return &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, body)
	})}
}

func getBody(t *testing.T, address string) string {
	t.Helper()
	response, err := http.Get("http://" + address)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

func receiveError(t *testing.T, result <-chan error) error {
	t.Helper()
	select {
	case err := <-result:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("serveEndpoints did not return")
		return fmt.Errorf("unreachable")
	}
}

func assertListenerClosed(t *testing.T, address string) {
	t.Helper()
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatalf("listener %s still accepts connections", address)
	}
}
