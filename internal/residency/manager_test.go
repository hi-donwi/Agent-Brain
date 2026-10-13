package residency_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/hi-donwi/Agent-Local/internal/backend"
	"github.com/hi-donwi/Agent-Local/internal/residency"
)

type mockResidencyAdapter struct {
	unloaded []string
	mu       sync.Mutex
}

func (m *mockResidencyAdapter) Name() string     { return "ollama" }
func (m *mockResidencyAdapter) Endpoint() string { return "http://127.0.0.1:11434" }
func (m *mockResidencyAdapter) Ping(ctx context.Context) error {
	return nil
}
func (m *mockResidencyAdapter) Version(ctx context.Context) (string, error) {
	return "0.30.8", nil
}
func (m *mockResidencyAdapter) ListModels(ctx context.Context) ([]backend.ModelInfo, error) {
	return nil, nil
}
func (m *mockResidencyAdapter) ShowModel(ctx context.Context, model string) (backend.ModelDetails, error) {
	return backend.ModelDetails{}, nil
}
func (m *mockResidencyAdapter) RunningModels(ctx context.Context) ([]backend.ProcessInfo, error) {
	return nil, nil
}
func (m *mockResidencyAdapter) Chat(ctx context.Context, req backend.ChatRequest) (*backend.ChatResponse, error) {
	return nil, nil
}
func (m *mockResidencyAdapter) ChatStream(ctx context.Context, req backend.ChatRequest, onChunk func(backend.ChatChunk) error) (*backend.ChatResponse, error) {
	return nil, nil
}
func (m *mockResidencyAdapter) Embed(ctx context.Context, req backend.EmbedRequest) (*backend.EmbedResponse, error) {
	return nil, nil
}
func (m *mockResidencyAdapter) Pull(ctx context.Context, model string, onProgress func(backend.Progress)) error {
	return nil
}
func (m *mockResidencyAdapter) Delete(ctx context.Context, model string) error {
	return nil
}
func (m *mockResidencyAdapter) Unload(ctx context.Context, model string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unloaded = append(m.unloaded, model)
	return nil
}

func TestManager_BasicAcquireAndRelease(t *testing.T) {
	adapter := &mockResidencyAdapter{}
	mgr := residency.New(residency.Config{
		TotalBudget:    8 * 1024 * 1024 * 1024,
		MaxConcurrency: 2,
		Adapter:        adapter,
	})
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()
	release, err := mgr.Acquire(ctx, "qwen2.5-coder:1.5b", 4096)
	if err != nil {
		t.Fatalf("Acquire failed: %v", err)
	}

	stats := mgr.Stats()
	if stats.ActiveRequests != 1 {
		t.Errorf("expected 1 active request, got %d", stats.ActiveRequests)
	}
	if len(stats.ResidentModels) != 1 {
		t.Errorf("expected 1 resident model, got %d", len(stats.ResidentModels))
	}

	release()

	statsAfter := mgr.Stats()
	if statsAfter.ActiveRequests != 0 {
		t.Errorf("expected 0 active requests after release, got %d", statsAfter.ActiveRequests)
	}
	if len(statsAfter.ResidentModels) != 1 {
		t.Errorf("model should remain resident after release")
	}
}

func TestManager_ConcurrencyQueueing(t *testing.T) {
	mgr := residency.New(residency.Config{
		TotalBudget:    8 * 1024 * 1024 * 1024,
		MaxConcurrency: 1, // Only 1 active request allowed
		QueueTimeout:   2 * time.Second,
	})
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()
	rel1, err := mgr.Acquire(ctx, "qwen2.5-coder:1.5b", 2048)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}

	// Second acquire in goroutine should wait in queue
	acquiredSecond := make(chan bool)
	go func() {
		rel2, err2 := mgr.Acquire(ctx, "qwen2.5-coder:1.5b", 2048)
		if err2 == nil {
			acquiredSecond <- true
			rel2()
		} else {
			acquiredSecond <- false
		}
	}()

	// Give goroutine time to enter queue
	time.Sleep(50 * time.Millisecond)
	statsQueued := mgr.Stats()
	if statsQueued.QueuedRequests != 1 {
		t.Errorf("expected 1 queued request, got %d", statsQueued.QueuedRequests)
	}

	// Release first request -> second should acquire immediately
	rel1()

	select {
	case ok := <-acquiredSecond:
		if !ok {
			t.Errorf("second acquire failed")
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for second acquire")
	}
}

func TestManager_LRUEviction(t *testing.T) {
	adapter := &mockResidencyAdapter{}
	// Total budget: 2 GiB.
	// Model A (1.5B) takes ~1.5 GiB.
	// Model B (bge-reranker-large) takes ~1.2 GiB.
	// Together they cannot fit in 2 GiB.
	mgr := residency.New(residency.Config{
		TotalBudget:    2 * 1024 * 1024 * 1024,
		MaxConcurrency: 2,
		Adapter:        adapter,
	})
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()

	// 1. Acquire Model A then release it (becomes idle resident)
	relA, err := mgr.Acquire(ctx, "qwen2.5-coder:1.5b", 2048)
	if err != nil {
		t.Fatalf("acquire model A failed: %v", err)
	}
	relA()

	statsA := mgr.Stats()
	if len(statsA.ResidentModels) != 1 {
		t.Fatalf("expected 1 resident model A")
	}

	// 2. Acquire Model B which exceeds remaining budget -> triggers eviction of Model A
	relB, err := mgr.Acquire(ctx, "bge-reranker-large:latest", 512)
	if err != nil {
		t.Fatalf("acquire model B failed: %v", err)
	}
	defer relB()

	// Verify adapter received unload command for Model A
	adapter.mu.Lock()
	unloadedCount := len(adapter.unloaded)
	firstUnloaded := ""
	if unloadedCount > 0 {
		firstUnloaded = adapter.unloaded[0]
	}
	adapter.mu.Unlock()

	if unloadedCount != 1 || firstUnloaded != "qwen2.5-coder:1.5b" {
		t.Errorf("expected 1 unload call for qwen2.5-coder:1.5b, got %d (models: %v)", unloadedCount, adapter.unloaded)
	}

	statsB := mgr.Stats()
	if len(statsB.ResidentModels) != 1 || statsB.ResidentModels[0].BackendTag != "bge-reranker-large:latest" {
		t.Errorf("expected only model B resident, got %+v", statsB.ResidentModels)
	}
}

func TestManager_MemoryExceeded(t *testing.T) {
	mgr := residency.New(residency.Config{
		TotalBudget: 1 * 1024 * 1024 * 1024, // 1 GiB budget
	})
	defer func() { _ = mgr.Close() }()

	ctx := context.Background()
	// qwen2.5-coder:7b requires ~5.5 GiB, which exceeds 1 GiB budget
	_, err := mgr.Acquire(ctx, "qwen2.5-coder:7b", 4096)
	if !errors.Is(err, residency.ErrMemoryExceeded) {
		t.Errorf("expected ErrMemoryExceeded, got %v", err)
	}
}

func TestManager_ContextCancellationAndTimeout(t *testing.T) {
	mgr := residency.New(residency.Config{
		TotalBudget:    8 * 1024 * 1024 * 1024,
		MaxConcurrency: 1,
		QueueTimeout:   100 * time.Millisecond,
	})
	defer func() { _ = mgr.Close() }()

	rel, err := mgr.Acquire(context.Background(), "qwen2.5-coder:1.5b", 2048)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer rel()

	// 1. Context cancellation
	ctxCancel, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately
	_, errCancel := mgr.Acquire(ctxCancel, "qwen2.5-coder:1.5b", 2048)
	if !errors.Is(errCancel, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", errCancel)
	}

	// 2. Queue timeout
	_, errTimeout := mgr.Acquire(context.Background(), "qwen2.5-coder:1.5b", 2048)
	if !errors.Is(errTimeout, residency.ErrQueueTimeout) {
		t.Errorf("expected ErrQueueTimeout, got %v", errTimeout)
	}
}

func TestManager_CloseWakesWaiters(t *testing.T) {
	mgr := residency.New(residency.Config{
		TotalBudget:    8 * 1024 * 1024 * 1024,
		MaxConcurrency: 1,
		QueueTimeout:   5 * time.Second,
	})

	rel, err := mgr.Acquire(context.Background(), "qwen2.5-coder:1.5b", 2048)
	if err != nil {
		t.Fatalf("first acquire failed: %v", err)
	}
	defer rel()

	waitErr := make(chan error)
	go func() {
		_, errQueued := mgr.Acquire(context.Background(), "qwen2.5-coder:1.5b", 2048)
		waitErr <- errQueued
	}()

	time.Sleep(50 * time.Millisecond)
	_ = mgr.Close()

	select {
	case errResult := <-waitErr:
		if !errors.Is(errResult, residency.ErrManagerClosed) {
			t.Errorf("expected ErrManagerClosed, got %v", errResult)
		}
	case <-time.After(1 * time.Second):
		t.Fatalf("timed out waiting for waiter to wake up on manager close")
	}
}
