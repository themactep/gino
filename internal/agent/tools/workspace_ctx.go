package tools

import "context"

// workspaceKey is the context key for the per-turn workspace path.
type workspaceKey struct{}

// WithWorkspace returns a context with the workspace path set.
// Tools that are workspace-aware (FilesystemTool, ExecTool) will use
// this path instead of their default configured workspace when present.
func WithWorkspace(ctx context.Context, ws string) context.Context {
	return context.WithValue(ctx, workspaceKey{}, ws)
}

// WorkspaceFromContext extracts the per-turn workspace path from the context.
// Returns empty string if not set (single-tenant mode).
func WorkspaceFromContext(ctx context.Context) string {
	if ws, ok := ctx.Value(workspaceKey{}).(string); ok {
		return ws
	}
	return ""
}
