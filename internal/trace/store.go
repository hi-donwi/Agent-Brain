package trace

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidSessionID = errors.New("invalid trace session ID")

// Store manages reading and writing trace records to JSONL files.
type Store struct {
	tracesDir string
	mu        sync.RWMutex
}

// NewStore initializes a trace store rooted at tracesDir.
func NewStore(tracesDir string) *Store {
	return &Store{
		tracesDir: filepath.Clean(tracesDir),
	}
}

// WriteRecord appends a single Record to the session's JSONL trace file.
func (s *Store) WriteRecord(rec *Record) error {
	if rec == nil {
		return errors.New("cannot write nil trace record")
	}

	if rec.Version == 0 {
		rec.Version = SchemaVersion
	}
	if rec.SessionID == "" {
		rec.SessionID = "default"
	}
	if !validSessionID(rec.SessionID) {
		return ErrInvalidSessionID
	}
	if rec.TurnID == "" {
		rec.TurnID = uuid.NewString()
	}
	if rec.Timestamp.IsZero() {
		rec.Timestamp = time.Now().UTC()
	}

	data, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("marshal trace record: %w", err)
	}
	data = append(data, '\n')

	s.mu.Lock()
	defer s.mu.Unlock()

	if err := os.MkdirAll(s.tracesDir, 0o700); err != nil {
		return fmt.Errorf("create traces directory %s: %w", s.tracesDir, err)
	}

	sessionFile := filepath.Join(s.tracesDir, fmt.Sprintf("%s.jsonl", rec.SessionID))
	f, err := os.OpenFile(sessionFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("open session trace file %s: %w", sessionFile, err)
	}
	defer f.Close()

	if _, err := f.Write(data); err != nil {
		return fmt.Errorf("write trace line: %w", err)
	}

	return nil
}

// ReadSession reads all trace records for a session ID.
func (s *Store) ReadSession(sessionID string) ([]Record, error) {
	if !validSessionID(sessionID) {
		return nil, ErrInvalidSessionID
	}
	s.mu.RLock()
	defer s.mu.RUnlock()

	sessionFile := filepath.Join(s.tracesDir, fmt.Sprintf("%s.jsonl", sessionID))
	info, err := os.Lstat(sessionFile)
	if err != nil {
		return nil, fmt.Errorf("read trace session %s: %w", sessionID, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, ErrInvalidSessionID
	}
	f, err := os.Open(sessionFile)
	if err != nil {
		return nil, fmt.Errorf("read trace session %s: %w", sessionID, err)
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !opened.Mode().IsRegular() || !os.SameFile(info, opened) {
		return nil, ErrInvalidSessionID
	}

	var records []Record
	scanner := bufio.NewScanner(f)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Bytes()
		if len(bytesTrim(line)) == 0 {
			continue
		}

		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, fmt.Errorf("corrupt trace record on line %d in %s: %w", lineNum, sessionFile, err)
		}
		records = append(records, rec)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan trace file %s: %w", sessionFile, err)
	}

	return records, nil
}

// ListSessions inspects the traces directory and summarizes all recorded sessions.
func (s *Store) ListSessions() ([]SessionSummary, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries, err := os.ReadDir(s.tracesDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read traces dir %s: %w", s.tracesDir, err)
	}

	var summaries []SessionSummary

	for _, entry := range entries {
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".jsonl") {
			continue
		}

		sessionID := strings.TrimSuffix(entry.Name(), ".jsonl")
		if !validSessionID(sessionID) {
			continue
		}
		filePath := filepath.Join(s.tracesDir, entry.Name())
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		sum, err := summarizeSessionFile(filePath, sessionID)
		if err == nil && sum.RecordCount > 0 {
			summaries = append(summaries, *sum)
		}
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].FirstTimestamp.After(summaries[j].FirstTimestamp)
	})

	return summaries, nil
}

func validSessionID(id string) bool {
	if len(id) == 0 || len(id) > 128 || id == "." || id == ".." {
		return false
	}
	for i, char := range id {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') {
			continue
		}
		if i > 0 && (char == '_' || char == '-' || char == '.') {
			continue
		}
		return false
	}
	return true
}

func summarizeSessionFile(path, sessionID string) (*SessionSummary, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sum := &SessionSummary{
		SessionID: sessionID,
		FilePath:  path,
	}

	modelMap := make(map[string]bool)
	scanner := bufio.NewScanner(f)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytesTrim(line)) == 0 {
			continue
		}

		var rec Record
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}

		sum.RecordCount++
		if sum.FirstTimestamp.IsZero() || rec.Timestamp.Before(sum.FirstTimestamp) {
			sum.FirstTimestamp = rec.Timestamp
		}
		if rec.Timestamp.After(sum.LastTimestamp) {
			sum.LastTimestamp = rec.Timestamp
		}

		if rec.Model != "" {
			modelMap[rec.Model] = true
		}
		sum.TotalPromptToks += rec.PromptTokens
		sum.TotalEvalToks += rec.EvalTokens
		sum.TotalDurationMs += rec.DurationMs
		if rec.Error != "" {
			sum.HasErrors = true
		}
	}

	for m := range modelMap {
		sum.Models = append(sum.Models, m)
	}
	sort.Strings(sum.Models)

	return sum, nil
}

func bytesTrim(b []byte) []byte {
	return []byte(strings.TrimSpace(string(b)))
}
