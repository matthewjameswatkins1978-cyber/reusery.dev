package server

import (
	"context"
	"net/http"
	"testing"
	"time"
)

// TestGracefulShutdown starts a real listener, cancels its context and
// requires Start to return nil and stop accepting connections.
func TestGracefulShutdown(t *testing.T) {
	const addr = "127.0.0.1:18081"

	s := New(addr, testLogger())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- s.Start(ctx) }()

	waitForHealthy(t, "http://"+addr+"/health")

	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Start returned error after cancel: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("server did not shut down in time")
	}

	client := http.Client{Timeout: 2 * time.Second}
	if _, err := client.Get("http://" + addr + "/health"); err == nil {
		t.Fatal("expected connection failure after shutdown, request succeeded")
	}
}

func waitForHealthy(t *testing.T, url string) {
	t.Helper()

	client := http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		res, err := client.Get(url)
		if err == nil {
			closeErr := res.Body.Close()
			if closeErr != nil {
				t.Fatalf("close health response: %v", closeErr)
			}
			if res.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("server did not become healthy in time")
}
