package main

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"testing"
	"time"
)

func TestHTTPServerBudgets(t *testing.T) {
	for _, tc := range []struct {
		refresh, wantWrite time.Duration
	}{
		{10 * time.Second, 15 * time.Second},
		{time.Minute, 65 * time.Second},
		{time.Duration(1<<63 - 1), time.Duration(1<<63 - 1)},
	} {
		srv := newHTTPServer("127.0.0.1:61765", http.NotFoundHandler(), tc.refresh)
		if srv.WriteTimeout != tc.wantWrite || srv.IdleTimeout != 30*time.Second || srv.ReadHeaderTimeout != 5*time.Second || srv.ReadTimeout != 5*time.Second {
			t.Fatalf("refresh=%s: write=%s idle=%s headers=%s", tc.refresh, srv.WriteTimeout, srv.IdleTimeout, srv.ReadHeaderTimeout)
		}
	}
}

func TestHTTPServerClosesIdleKeepAliveConnection(t *testing.T) {
	srv := newHTTPServer("127.0.0.1:0", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}), time.Second)
	// Exercise the production configuration with a short idle interval.
	srv.IdleTimeout = 50 * time.Millisecond
	listener, err := net.Listen("tcp", srv.Addr)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(listener) }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != http.ErrServerClosed {
			t.Errorf("serve: %v", err)
		}
	})
	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(2 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: localhost\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	resp, err := http.ReadResponse(reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || string(body) != "ok" || resp.Close {
		t.Fatalf("response: body=%q close=%v err=%v", body, resp.Close, err)
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		t.Fatalf("idle connection was not closed by server: %v", err)
	}
}
