package residency

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/hi-donwi/Agent-Brain/internal/backend"
	"github.com/hi-donwi/Agent-Brain/internal/catalog"
)

var (
	// ErrMemoryExceeded is returned when a model's footprint exceeds total configured memory budget.
	ErrMemoryExceeded = errors.New("model memory footprint exceeds total available budget")

	// ErrQueueTimeout is returned when a request waiting in the queue exceeds its timeout.
	ErrQueueTimeout = errors.New("request queue timed out waiting for concurrency slot")

	// ErrManagerClosed is returned when operations are attempted on a closed residency manager.
	ErrManagerClosed = errors.New("residency manager is closed")
	// ErrModelMutationInProgress prevents concurrent unload/delete mutations for one model.
	ErrModelMutationInProgress = errors.New("model mutation is already in progress")
	// ErrModelNotBlocked rejects residency accounting changes outside a mutation lease.
	ErrModelNotBlocked = errors.New("model is not held for mutation")
)

// ReleaseFunc releases a model's active request concurrency slot and updates usage timestamps.
type ReleaseFunc func()

// Config configures the residency and concurrency manager.
type Config struct {
	TotalBudget    uint64        // Memory budget in bytes.
	MaxConcurrency int           // Maximum concurrent active requests across all models (default 2).
	MaxPerModel    int           // Maximum concurrent requests per individual model (0 = unconstrained).
	QueueTimeout   time.Duration // Maximum wait time in request queue (default 30s).
	Catalog        *catalog.Catalog
	Adapter        backend.Adapter
}

// ResidentModel tracks a loaded model's memory footprint and execution state.
type ResidentModel struct {
	ID             string    `json:"id"`
	BackendTag     string    `json:"backend_tag"`
	MemoryBytes    uint64    `json:"memory_bytes"`
	ActiveRequests int       `json:"active_requests"`
	LastUsed       time.Time `json:"last_used"`
}

// Stats provides an observability snapshot of the manager state.
type Stats struct {
	TotalBudget     uint64          `json:"total_budget"`
	UsedMemory      uint64          `json:"used_memory"`
	AvailableMemory uint64          `json:"available_memory"`
	ActiveRequests  int             `json:"active_requests"`
	QueuedRequests  int             `json:"queued_requests"`
	MaxConcurrency  int             `json:"max_concurrency"`
	ResidentModels  []ResidentModel `json:"resident_models"`
}

// Manager governs multi-model memory allocation, LRU evictions, and request queueing.
type Manager struct {
	cfg            Config
	mu             sync.Mutex
	resident       map[string]*ResidentModel
	blockedModels  map[string]bool
	activeRequests int
	queuedRequests int
	usedMemory     uint64
	notifyCh       chan struct{}
	closed         bool
}

// New constructs a new residency and concurrency manager.
func New(cfg Config) *Manager {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 2
	}
	if cfg.QueueTimeout <= 0 {
		cfg.QueueTimeout = 30 * time.Second
	}
	if cfg.Catalog == nil {
		cfg.Catalog = catalog.Default()
	}
	if cfg.TotalBudget <= 0 {
		cfg.TotalBudget = 8 * 1024 * 1024 * 1024 // 8 GiB default budget
	}

	return &Manager{
		cfg:           cfg,
		resident:      make(map[string]*ResidentModel),
		blockedModels: make(map[string]bool),
		notifyCh:      make(chan struct{}),
	}
}

// ResolveModelMemory estimates memory required for a model tag at a specific context length.
func (m *Manager) ResolveModelMemory(tag string, numCtx int) uint64 {
	if m.cfg.Catalog == nil {
		return 1024 * 1024 * 1024
	}

	// 1. Direct catalog ID lookup
	if cm, ok := m.cfg.Catalog.GetModel(tag); ok {
		return cm.MemoryAtCtx(numCtx)
	}

	// 2. Lookup by backend tag across catalog models
	backendName := "ollama"
	if m.cfg.Adapter != nil {
		backendName = m.cfg.Adapter.Name()
	}
	for _, cm := range m.cfg.Catalog.Models {
		if btag, ok := cm.Backends[backendName]; ok && btag == tag {
			return cm.MemoryAtCtx(numCtx)
		}
	}

	// Unknown model default: 1 GiB
	return 1024 * 1024 * 1024
}

// Acquire requests a concurrency slot and memory lease for modelTag.
// If another model must be evicted to free memory, it unloads idle models in LRU order.
// If concurrency or memory is saturated by active requests, Acquire queues the request.
func (m *Manager) Acquire(ctx context.Context, modelTag string, numCtx int) (ReleaseFunc, error) {
	memNeeded := m.ResolveModelMemory(modelTag, numCtx)

	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}

	if memNeeded > m.cfg.TotalBudget {
		m.mu.Unlock()
		return nil, ErrMemoryExceeded
	}

	for {
		if m.closed {
			m.mu.Unlock()
			return nil, ErrManagerClosed
		}

		canAcquire, needsEvict, evictList := m.evaluateAcquire(modelTag, memNeeded)
		if canAcquire {
			if needsEvict && len(evictList) > 0 {
				// Perform eviction of idle models
				m.mu.Unlock()
				for _, ev := range evictList {
					if m.cfg.Adapter != nil {
						_ = m.cfg.Adapter.Unload(ctx, ev.BackendTag)
					}
				}
				m.mu.Lock()
				for _, ev := range evictList {
					if rm, exists := m.resident[ev.BackendTag]; exists && rm.ActiveRequests == 0 {
						delete(m.resident, ev.BackendTag)
						if m.usedMemory >= rm.MemoryBytes {
							m.usedMemory -= rm.MemoryBytes
						} else {
							m.usedMemory = 0
						}
					}
				}
				// Re-check after eviction
				continue
			}

			// Acquire lease
			rm, exists := m.resident[modelTag]
			if !exists {
				rm = &ResidentModel{
					ID:          modelTag,
					BackendTag:  modelTag,
					MemoryBytes: memNeeded,
				}
				m.resident[modelTag] = rm
				m.usedMemory += memNeeded
			}
			rm.ActiveRequests++
			rm.LastUsed = time.Now()
			m.activeRequests++

			m.mu.Unlock()

			var once sync.Once
			release := func() {
				once.Do(func() {
					m.mu.Lock()
					defer m.mu.Unlock()
					if rm, ok := m.resident[modelTag]; ok {
						if rm.ActiveRequests > 0 {
							rm.ActiveRequests--
						}
						rm.LastUsed = time.Now()
					}
					if m.activeRequests > 0 {
						m.activeRequests--
					}
					if !m.closed {
						close(m.notifyCh)
						m.notifyCh = make(chan struct{})
					}
				})
			}
			return release, nil
		}

		// Wait in queue
		notify := m.notifyCh
		m.queuedRequests++
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			m.mu.Lock()
			m.queuedRequests--
			m.mu.Unlock()
			return nil, ctx.Err()

		case <-time.After(m.cfg.QueueTimeout):
			m.mu.Lock()
			m.queuedRequests--
			m.mu.Unlock()
			return nil, ErrQueueTimeout

		case <-notify:
			m.mu.Lock()
			m.queuedRequests--
		}
	}
}

// BlockModel prevents new requests from acquiring a model while a destructive
// operation runs and waits until current leases have drained. The returned release
// function must always be called to resume queued requests.
func (m *Manager) BlockModel(ctx context.Context, modelTag string) (ReleaseFunc, error) {
	if modelTag == "" {
		return nil, errors.New("model tag is required")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, ErrManagerClosed
	}
	if m.blockedModels[modelTag] {
		m.mu.Unlock()
		return nil, ErrModelMutationInProgress
	}
	m.blockedModels[modelTag] = true
	m.notifyWaiters()
	m.mu.Unlock()

	var once sync.Once
	releaseBlock := func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.blockedModels, modelTag)
			if !m.closed {
				m.notifyWaiters()
			}
			m.mu.Unlock()
		})
	}
	for {
		m.mu.Lock()
		if m.closed {
			m.mu.Unlock()
			releaseBlock()
			return nil, ErrManagerClosed
		}
		resident := m.resident[modelTag]
		if resident == nil || resident.ActiveRequests == 0 {
			m.mu.Unlock()
			return releaseBlock, nil
		}
		notify := m.notifyCh
		m.mu.Unlock()
		select {
		case <-ctx.Done():
			releaseBlock()
			return nil, ctx.Err()
		case <-notify:
		}
	}
}

// ForgetBlockedModel removes a successfully unloaded/deleted model from residency
// accounting. Call only while holding the BlockModel lease for the same tag.
func (m *Manager) ForgetBlockedModel(modelTag string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.blockedModels[modelTag] {
		return ErrModelNotBlocked
	}
	resident := m.resident[modelTag]
	if resident == nil {
		return nil
	}
	if resident.ActiveRequests != 0 {
		return ErrModelMutationInProgress
	}
	delete(m.resident, modelTag)
	if m.usedMemory >= resident.MemoryBytes {
		m.usedMemory -= resident.MemoryBytes
	} else {
		m.usedMemory = 0
	}
	m.notifyWaiters()
	return nil
}

// evaluateAcquire inspects concurrency limits and memory availability under m.mu.
func (m *Manager) evaluateAcquire(modelTag string, memNeeded uint64) (bool, bool, []ResidentModel) {
	if m.blockedModels[modelTag] {
		return false, false, nil
	}
	// 1. Check global concurrency limit
	if m.activeRequests >= m.cfg.MaxConcurrency {
		return false, false, nil
	}

	// 2. Check per-model concurrency limit
	if rm, exists := m.resident[modelTag]; exists {
		if m.cfg.MaxPerModel > 0 && rm.ActiveRequests >= m.cfg.MaxPerModel {
			return false, false, nil
		}
		// Model is already resident; memory is already accounted for
		return true, false, nil
	}

	// 3. Model is not resident: check memory availability
	if m.usedMemory+memNeeded <= m.cfg.TotalBudget {
		return true, false, nil
	}

	// 4. Memory budget exceeded: check if evicting idle models satisfies budget
	var idleModels []ResidentModel
	var totalIdleMemory uint64
	for _, rm := range m.resident {
		if rm.ActiveRequests == 0 {
			idleModels = append(idleModels, *rm)
			totalIdleMemory += rm.MemoryBytes
		}
	}

	if m.usedMemory-totalIdleMemory+memNeeded > m.cfg.TotalBudget {
		// Even evicting all idle models is not enough; must wait for active models to finish
		return false, false, nil
	}

	// Sort idle models by LastUsed ascending (oldest first for LRU)
	sort.Slice(idleModels, func(i, j int) bool {
		return idleModels[i].LastUsed.Before(idleModels[j].LastUsed)
	})

	var evictList []ResidentModel
	currentUsed := m.usedMemory
	for _, im := range idleModels {
		if currentUsed+memNeeded <= m.cfg.TotalBudget {
			break
		}
		evictList = append(evictList, im)
		if currentUsed >= im.MemoryBytes {
			currentUsed -= im.MemoryBytes
		} else {
			currentUsed = 0
		}
	}

	return true, true, evictList
}

func (m *Manager) notifyWaiters() {
	close(m.notifyCh)
	m.notifyCh = make(chan struct{})
}

// Stats returns an instantaneous snapshot of residency and concurrency metrics.
func (m *Manager) Stats() Stats {
	m.mu.Lock()
	defer m.mu.Unlock()

	var residentList []ResidentModel
	for _, rm := range m.resident {
		residentList = append(residentList, *rm)
	}

	avail := uint64(0)
	if m.cfg.TotalBudget >= m.usedMemory {
		avail = m.cfg.TotalBudget - m.usedMemory
	}

	return Stats{
		TotalBudget:     m.cfg.TotalBudget,
		UsedMemory:      m.usedMemory,
		AvailableMemory: avail,
		ActiveRequests:  m.activeRequests,
		QueuedRequests:  m.queuedRequests,
		MaxConcurrency:  m.cfg.MaxConcurrency,
		ResidentModels:  residentList,
	}
}

// Close terminates the residency manager and releases waiting goroutines.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}
	m.closed = true
	close(m.notifyCh)
	return nil
}
