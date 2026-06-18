package tools

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/wltechblog/gino/internal/trash"
)

// TrashListTool lists all items in the trash directory.
type TrashListTool struct {
	trash *trash.Manager
}

func NewTrashListTool(tm *trash.Manager) *TrashListTool {
	return &TrashListTool{trash: tm}
}

func (t *TrashListTool) Name() string        { return "trash_list" }
func (t *TrashListTool) Description() string { return "List all files in the trash (soft-deleted files that can be restored)" }

func (t *TrashListTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{},
	}
}

func (t *TrashListTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	items, err := t.trash.List()
	if err != nil {
		return "", err
	}

	if len(items) == 0 {
		return "trash is empty", nil
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Trash (%d items):\n", len(items)))
	b.WriteString("────────────────────────────────────────\n")
	for _, item := range items {
		icon := "📄"
		if item.IsDir {
			icon = "📁"
		}
		age := time.Since(item.TrashedAt).Truncate(time.Minute)
		b.WriteString(fmt.Sprintf("%s %s\n", icon, item.TrashName))
		b.WriteString(fmt.Sprintf("   from: %s\n", item.OriginalPath))
		b.WriteString(fmt.Sprintf("   trashed: %s ago\n", age))
		if item.Size > 0 {
			b.WriteString(fmt.Sprintf("   size: %d bytes\n", item.Size))
		}
	}
	b.WriteString("────────────────────────────────────────\n")
	b.WriteString("Use trash_restore to restore, trash_purge to permanently delete")

	return b.String(), nil
}

// TrashRestoreTool restores a file from trash to its original location.
type TrashRestoreTool struct {
	trash *trash.Manager
}

func NewTrashRestoreTool(tm *trash.Manager) *TrashRestoreTool {
	return &TrashRestoreTool{trash: tm}
}

func (t *TrashRestoreTool) Name() string        { return "trash_restore" }
func (t *TrashRestoreTool) Description() string { return "Restore a file from trash to its original location" }

func (t *TrashRestoreTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The trash item name (or partial original filename) to restore",
			},
		},
		"required": []string{"name"},
	}
}

func (t *TrashRestoreTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	nameRaw, ok := args["name"]
	if !ok {
		return "", fmt.Errorf("trash_restore: 'name' is required")
	}
	name, ok := nameRaw.(string)
	if !ok {
		return "", fmt.Errorf("trash_restore: 'name' must be a string")
	}

	// Try exact match first, then partial
	trashName := name
	restoredPath, err := t.trash.Restore(trashName)
	if err != nil {
		// Try partial match
		resolved, resolveErr := t.trash.ResolveTrashName(name)
		if resolveErr != nil {
			return "", fmt.Errorf("trash_restore: %s\nAlso tried partial match: %s", err, resolveErr)
		}
		trashName = resolved
		restoredPath, err = t.trash.Restore(trashName)
		if err != nil {
			return "", err
		}
	}

	return fmt.Sprintf("✅ restored: %s → %s", trashName, restoredPath), nil
}

// TrashPurgeTool permanently deletes a trashed item (or all items).
type TrashPurgeTool struct {
	trash *trash.Manager
}

func NewTrashPurgeTool(tm *trash.Manager) *TrashPurgeTool {
	return &TrashPurgeTool{trash: tm}
}

func (t *TrashPurgeTool) Name() string        { return "trash_purge" }
func (t *TrashPurgeTool) Description() string { return "Permanently delete trashed item(s). Use empty=true to empty all trash." }

func (t *TrashPurgeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name": map[string]interface{}{
				"type":        "string",
				"description": "The trash item name to permanently delete",
			},
			"empty": map[string]interface{}{
				"type":        "boolean",
				"description": "Set to true to permanently delete ALL trashed items",
			},
		},
	}
}

func (t *TrashPurgeTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	emptyRaw, _ := args["empty"]
	empty := false
	if v, ok := emptyRaw.(bool); ok {
		empty = v
	}

	if empty {
		count, err := t.trash.Empty()
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("🗑️ emptied trash: %d items permanently deleted", count), nil
	}

	nameRaw, ok := args["name"]
	if !ok {
		return "", fmt.Errorf("trash_purge: 'name' or 'empty=true' is required")
	}
	name, ok := nameRaw.(string)
	if !ok {
		return "", fmt.Errorf("trash_purge: 'name' must be a string")
	}

	// Try exact match first, then partial
	err := t.trash.Purge(name)
	if err != nil {
		resolved, resolveErr := t.trash.ResolveTrashName(name)
		if resolveErr != nil {
			return "", fmt.Errorf("trash_purge: %s\nAlso tried partial match: %s", err, resolveErr)
		}
		err = t.trash.Purge(resolved)
		if err != nil {
			return "", err
		}
		name = resolved
	}

	return fmt.Sprintf("🗑️ permanently deleted: %s", name), nil
}
