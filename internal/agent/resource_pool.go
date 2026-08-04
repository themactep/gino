package agent

import (
	"log"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	"github.com/wltechblog/gino/internal/agent/memory"
	"github.com/wltechblog/gino/internal/brain"
	"github.com/wltechblog/gino/internal/config"
	"github.com/wltechblog/gino/internal/providers"
)

// ResourcePool lazily creates and caches per-user brain and memory instances.
// In single-tenant mode (no userManager), the shared instances are always used.
type ResourcePool struct {
	mu       sync.RWMutex
	users    map[string]*userResources
	homeDir  string
	brainCfg *config.BrainConfig
	provider providers.LLMProvider

	// Shared defaults (single-tenant mode)
	sharedMem   *memory.MemoryStore
	sharedBrain *brain.Brain

	// Embedder factory — shared across all per-user brains
	embedder brain.EmbeddingProvider

	// Brain options template
	brainOpts brain.Options
}

type userResources struct {
	mem   *memory.MemoryStore
	brain *brain.Brain
}

// NewResourcePool creates a pool that will lazily create per-user resources.
func NewResourcePool(homeDir string, brainCfg *config.BrainConfig, provider providers.LLMProvider, sharedMem *memory.MemoryStore, sharedBrain *brain.Brain) *ResourcePool {
	pool := &ResourcePool{
		users:      make(map[string]*userResources),
		homeDir:    homeDir,
		brainCfg:   brainCfg,
		provider:   provider,
		sharedMem:  sharedMem,
		sharedBrain: sharedBrain,
	}

	// Capture the embedder configuration from the shared brain initialization
	// so per-user brains use the same embedding provider type.
	if brainCfg != nil && brainCfg.Enabled {
		pool.embedder, pool.brainOpts = buildEmbedder(brainCfg)
	}

	return pool
}

// Get returns the brain and memory for a given user ID.
// In single-tenant mode (no users registered), returns shared instances.
func (p *ResourcePool) Get(userID, workspacePath string) (*memory.MemoryStore, *brain.Brain) {
	// Single-tenant fast path
	if p.embedder == nil && p.sharedBrain == nil {
		return p.sharedMem, nil
	}

	// If no per-user brain configured, use shared memory (which may already
	// be workspace-scoped via the context-based workspace routing)
	if workspacePath == "" {
		return p.sharedMem, p.sharedBrain
	}

	p.mu.RLock()
	ur, ok := p.users[userID]
	p.mu.RUnlock()
	if ok {
		return ur.mem, ur.brain
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if ur, ok := p.users[userID]; ok {
		return ur.mem, ur.brain
	}

	// Create per-user memory store
	userMem := memory.NewMemoryStoreWithWorkspace(workspacePath, 100)

	// Create per-user brain DB
	var userBrain *brain.Brain
	if p.embedder != nil {
		dbPath := filepath.Join(workspacePath, "brain.db")
		b, err := brain.Init(dbPath, p.embedder, p.brainOpts)
		if err != nil {
			log.Printf("ResourcePool: failed to init brain for user %s at %s: %v", userID, dbPath, err)
		} else {
			userBrain = b
		}
	}

	ur = &userResources{mem: userMem, brain: userBrain}
	p.users[userID] = ur
	return userMem, userBrain
}

// CloseUser closes and removes resources for a user (called on eviction).
func (p *ResourcePool) CloseUser(userID string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	ur, ok := p.users[userID]
	if !ok {
		return
	}

	if ur.brain != nil {
		if err := ur.brain.Close(); err != nil {
			log.Printf("ResourcePool: error closing brain for user %s: %v", userID, err)
		}
	}

	delete(p.users, userID)
	log.Printf("ResourcePool: closed resources for user %s", userID)
}

// CloseAll closes all per-user resources (called on shutdown).
func (p *ResourcePool) CloseAll() {
	p.mu.Lock()
	defer p.mu.Unlock()

	for userID, ur := range p.users {
		if ur.brain != nil {
			if err := ur.brain.Close(); err != nil {
				log.Printf("ResourcePool: error closing brain for user %s: %v", userID, err)
			}
		}
		delete(p.users, userID)
	}
}

// buildEmbedder creates the embedding provider and brain options from config.
// This is extracted from initBrain so per-user brains reuse the same provider.
func buildEmbedder(cfg *config.BrainConfig) (brain.EmbeddingProvider, brain.Options) {
	var embedder brain.EmbeddingProvider

	ollamaURL := cfg.OllamaURL
	if ollamaURL == "" {
		ollamaURL = "http://localhost:11434"
	}

	// Try Ollama first
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(ollamaURL + "/api/tags")
	if err == nil && resp.StatusCode == 200 {
		resp.Body.Close()
		model := cfg.EmbeddingModel
		if model == "" {
			model = "nomic-embed-text"
		}
		embedder = brain.NewOllamaProvider(brain.OllamaConfig{
			BaseURL: ollamaURL,
			Model:   model,
		})
	} else if cfg.RemoteAPIBase != "" && cfg.RemoteAPIKey != "" {
		model := cfg.RemoteModel
		if model == "" {
			model = "text-embedding-3-small"
		}
		embedder = brain.NewRemoteAPIProvider(brain.RemoteAPIConfig{
			BaseURL: cfg.RemoteAPIBase,
			APIKey:  cfg.RemoteAPIKey,
			Model:   model,
		})
	}

	opts := brain.DefaultOptions()
	if cfg.EmbeddingModel != "" {
		opts.EmbeddingModel = cfg.EmbeddingModel
	}
	if cfg.EmbeddingDims > 0 {
		opts.EmbeddingDims = cfg.EmbeddingDims
	}

	return embedder, opts
}
