package mcp

import (
	"context"
	"testing"
	"time"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

// A host stops the server with SIGINT or SIGTERM; that is a normal shutdown, so serve
// reports no error and the command exits 0.
func TestServeTreatsCancellationAsCleanShutdown(t *testing.T) {
	s := NewServer(Config{RootDir: t.TempDir()})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	serverTransport, clientTransport := protocol.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- s.serve(ctx, serverTransport) }()

	client := protocol.NewClient(&protocol.Implementation{Name: "serve-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = session.Close() }()

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve after cancellation = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not stop on cancellation")
	}
}

// The client closing its side ends the session without an error.
func TestServeReturnsWhenTheClientDisconnects(t *testing.T) {
	s := NewServer(Config{RootDir: t.TempDir()})
	serverTransport, clientTransport := protocol.NewInMemoryTransports()
	done := make(chan error, 1)
	go func() { done <- s.serve(context.Background(), serverTransport) }()

	client := protocol.NewClient(&protocol.Implementation{Name: "serve-test", Version: "1.0.0"}, nil)
	session, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	_ = session.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve after disconnect = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serve did not return after the client disconnected")
	}
}
