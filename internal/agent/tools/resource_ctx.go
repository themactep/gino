package tools

import (
	"context"

	"github.com/wltechblog/gino/internal/agent/memory"
	"github.com/wltechblog/gino/internal/brain"
)

// resourceKey is the context key for per-user brain and memory instances.
type resourceKey struct{}

// Resources holds per-user brain and memory instances injected via context.
// When present, tools use these instead of the shared default instances.
type Resources struct {
	Memory *memory.MemoryStore
	Brain  *brain.Brain
}

// WithResources injects per-user brain and memory into the context.
// Tools that support per-user resolution will use these instead of defaults.
func WithResources(ctx context.Context, mem *memory.MemoryStore, br *brain.Brain) context.Context {
	return context.WithValue(ctx, resourceKey{}, &Resources{Memory: mem, Brain: br})
}

// ResourcesFromContext returns per-user resources if present, or nil.
func ResourcesFromContext(ctx context.Context) *Resources {
	r, _ := ctx.Value(resourceKey{}).(*Resources)
	return r
}

// resolveMemory returns the per-user memory store from context, or the fallback default.
func resolveMemory(ctx context.Context, fallback *memory.MemoryStore) *memory.MemoryStore {
	if r := ResourcesFromContext(ctx); r != nil && r.Memory != nil {
		return r.Memory
	}
	return fallback
}

// resolveBrain returns the per-user brain from context, or the fallback default.
func resolveBrain(ctx context.Context, fallback *brain.Brain) *brain.Brain {
	if r := ResourcesFromContext(ctx); r != nil && r.Brain != nil {
		return r.Brain
	}
	return fallback
}
