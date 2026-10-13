package trace

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestStore_WriteAndReadSession(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir)

	record1 := &Record{
		SessionID:    "sess-1",
		TurnID:       "turn-1",
		Model:        "qwen2.5-coder-1.5b",
		Role:         "code",
		Backend:      "ollama",
		Messages:     []Message{{Role: "user", Content: "Hello"}},
		Response:     "Hi there!",
		DurationMs:   150,
		PromptTokens: 10,
		EvalTokens:   20,
	}

	if err := store.WriteRecord(record1); err != nil {
		t.Fatalf("WriteRecord failed: %v", err)
	}

	record2 := &Record{
		SessionID:    "sess-1",
		TurnID:       "turn-2",
		Model:        "qwen2.5-coder-1.5b",
		Messages:     []Message{{Role: "user", Content: "What is 2+2?"}},
		Response:     "4",
		DurationMs:   80,
		PromptTokens: 8,
		EvalTokens:   5,
	}

	if err := store.WriteRecord(record2); err != nil {
		t.Fatalf("WriteRecord failed: %v", err)
	}

	records, err := store.ReadSession("sess-1")
	if err != nil {
		t.Fatalf("ReadSession failed: %v", err)
	}

	if len(records) != 2 {
		t.Fatalf("expected 2 records, got %d", len(records))
	}

	if records[0].TurnID != "turn-1" || records[0].Response != "Hi there!" {
		t.Errorf("unexpected record 0: %+v", records[0])
	}
	if records[1].TurnID != "turn-2" || records[1].Response != "4" {
		t.Errorf("unexpected record 1: %+v", records[1])
	}
	if records[0].Version != SchemaVersion {
		t.Errorf("expected SchemaVersion %d, got %d", SchemaVersion, records[0].Version)
	}
}

func TestStore_ListSessions(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir)

	// Write session A
	_ = store.WriteRecord(&Record{
		SessionID:    "session-a",
		TurnID:       "t1",
		Timestamp:    time.Now().Add(-1 * time.Hour),
		Model:        "model-a",
		PromptTokens: 100,
		EvalTokens:   50,
		DurationMs:   500,
	})

	// Write session B with an error
	_ = store.WriteRecord(&Record{
		SessionID:    "session-b",
		TurnID:       "t2",
		Timestamp:    time.Now(),
		Model:        "model-b",
		PromptTokens: 200,
		EvalTokens:   100,
		DurationMs:   1000,
		Error:        "backend timeout",
	})

	summaries, err := store.ListSessions()
	if err != nil {
		t.Fatalf("ListSessions failed: %v", err)
	}

	if len(summaries) != 2 {
		t.Fatalf("expected 2 summaries, got %d", len(summaries))
	}

	// Should be sorted most recent first
	if summaries[0].SessionID != "session-b" {
		t.Errorf("expected most recent session to be session-b, got %s", summaries[0].SessionID)
	}
	if !summaries[0].HasErrors {
		t.Errorf("expected session-b to have errors")
	}
	if summaries[0].TotalPromptToks != 200 || summaries[0].TotalEvalToks != 100 {
		t.Errorf("unexpected token counts for session-b: prompt=%d, eval=%d",
			summaries[0].TotalPromptToks, summaries[0].TotalEvalToks)
	}
}

func TestStore_ConcurrentWrites(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir)

	const workers = 8
	const recordsPerWorker = 20

	var wg sync.WaitGroup
	wg.Add(workers)

	for w := 0; w < workers; w++ {
		workerID := w
		go func() {
			defer wg.Done()
			for i := 0; i < recordsPerWorker; i++ {
				err := store.WriteRecord(&Record{
					SessionID: "concurrent-session",
					TurnID:    fmt.Sprintf("w%d-t%d", workerID, i),
					Model:     "model-test",
					Response:  "ok",
				})
				if err != nil {
					t.Errorf("concurrent WriteRecord error: %v", err)
				}
			}
		}()
	}

	wg.Wait()

	records, err := store.ReadSession("concurrent-session")
	if err != nil {
		t.Fatalf("ReadSession failed: %v", err)
	}

	expectedCount := workers * recordsPerWorker
	if len(records) != expectedCount {
		t.Fatalf("expected %d records, got %d", expectedCount, len(records))
	}
}

func TestStore_CorruptLine(t *testing.T) {
	tempDir := t.TempDir()
	store := NewStore(tempDir)

	sessionFile := filepath.Join(tempDir, "corrupt-sess.jsonl")
	_ = os.WriteFile(sessionFile, []byte("{invalid-json\n"), 0o600)

	_, err := store.ReadSession("corrupt-sess")
	if err == nil {
		t.Fatalf("expected error reading corrupt session, got nil")
	}
}
