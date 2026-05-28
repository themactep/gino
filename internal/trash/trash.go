package trash

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Manager handles soft-delete operations via a trash directory.
// Instead of permanently deleting files, they are moved to a trash
// directory with metadata preserved for restoration.
type Manager struct {
	trashDir string
	mu       sync.Mutex
}

// Meta stores metadata about a trashed file.
type Meta struct {
	OriginalPath string    `json:"originalPath"`
	TrashedAt    time.Time `json:"trashedAt"`
	TrashName    string    `json:"trashName"`
	Size         int64     `json:"size,omitempty"`
	IsDir        bool      `json:"isDir"`
}

// NewManager creates a trash manager. The trash directory is created
// under the given workspace as .picobot/trash/.
func NewManager(workspace string) (*Manager, error) {
	trashDir := filepath.Join(workspace, ".picobot", "trash")
	if err := os.MkdirAll(trashDir, 0o755); err != nil {
		return nil, fmt.Errorf("trash: create trash dir: %w", err)
	}
	return &Manager{trashDir: trashDir}, nil
}

// TrashDir returns the path to the trash directory.
func (m *Manager) TrashDir() string {
	return m.trashDir
}

// Trash moves a file or directory to the trash directory.
// Returns the trash name (used for restoration).
func (m *Manager) Trash(path string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("trash: resolve path: %w", err)
	}

	// Check source exists
	info, err := os.Stat(absPath)
	if err != nil {
		return "", fmt.Errorf("trash: stat %s: %w", absPath, err)
	}

	// Generate unique trash name: timestamp_originalname
	timestamp := time.Now().Format("20060102-150405")
	baseName := filepath.Base(absPath)
	trashName := fmt.Sprintf("%s_%s", timestamp, baseName)

	// Handle collisions
	trashPath := filepath.Join(m.trashDir, trashName)
	counter := 0
	for {
		if _, err := os.Stat(trashPath); os.IsNotExist(err) {
			break
		}
		counter++
		trashName = fmt.Sprintf("%s_%d_%s", timestamp, counter, baseName)
		trashPath = filepath.Join(m.trashDir, trashName)
	}

	// Move the file/directory
	if err := os.Rename(absPath, trashPath); err != nil {
		return "", fmt.Errorf("trash: move to trash: %w", err)
	}

	// Save metadata
	meta := Meta{
		OriginalPath: absPath,
		TrashedAt:    time.Now(),
		TrashName:    trashName,
		Size:         info.Size(),
		IsDir:        info.IsDir(),
	}

	metaPath := trashPath + ".meta"
	metaData, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(metaPath, metaData, 0o644); err != nil {
		// Non-fatal: the file is trashed, just metadata is missing
		_ = os.Remove(metaPath)
	}

	return trashName, nil
}

// Restore moves a file or directory from trash back to its original location.
// If the original location is occupied, it returns an error.
func (m *Manager) Restore(trashName string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	trashPath := filepath.Join(m.trashDir, trashName)
	metaPath := trashPath + ".meta"

	// Load metadata
	var meta Meta
	metaData, err := os.ReadFile(metaPath)
	if err != nil {
		return "", fmt.Errorf("trash: no metadata for %s (cannot determine original path)", trashName)
	}
	if err := json.Unmarshal(metaData, &meta); err != nil {
		return "", fmt.Errorf("trash: corrupt metadata for %s: %w", trashName, err)
	}

	// Check if original location is free
	if _, err := os.Stat(meta.OriginalPath); err == nil {
		return "", fmt.Errorf("trash: original path %s already exists (move it first, then restore)", meta.OriginalPath)
	}

	// Create parent directories if needed
	parentDir := filepath.Dir(meta.OriginalPath)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return "", fmt.Errorf("trash: create parent dir: %w", err)
	}

	// Move back
	if err := os.Rename(trashPath, meta.OriginalPath); err != nil {
		return "", fmt.Errorf("trash: restore: %w", err)
	}

	// Clean up metadata
	_ = os.Remove(metaPath)

	return meta.OriginalPath, nil
}

// List returns all trashed items, most recent first.
func (m *Manager) List() ([]Meta, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.trashDir)
	if err != nil {
		return nil, fmt.Errorf("trash: read dir: %w", err)
	}

	var items []Meta
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".meta") {
			continue // skip metadata files
		}

		metaPath := filepath.Join(m.trashDir, e.Name()+".meta")
		var meta Meta
		if mData, err := os.ReadFile(metaPath); err == nil {
			if err := json.Unmarshal(mData, &meta); err == nil {
				// Ensure TrashName is set even if missing from file
				if meta.TrashName == "" {
					meta.TrashName = e.Name()
				}
			} else {
				meta = Meta{
					OriginalPath: "(unknown)",
					TrashName:    e.Name(),
				}
			}
		} else {
			meta = Meta{
				OriginalPath: "(unknown)",
				TrashName:    e.Name(),
			}
		}

		items = append(items, meta)
	}

	// Sort most recent first
	sort.Slice(items, func(i, j int) bool {
		return items[i].TrashedAt.After(items[j].TrashedAt)
	})

	return items, nil
}

// Purge permanently deletes a specific trashed item.
func (m *Manager) Purge(trashName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	trashPath := filepath.Join(m.trashDir, trashName)
	if _, err := os.Stat(trashPath); err != nil {
		return fmt.Errorf("trash: %s not found", trashName)
	}

	if err := os.RemoveAll(trashPath); err != nil {
		return fmt.Errorf("trash: purge: %w", err)
	}

	// Clean up metadata
	_ = os.Remove(trashPath + ".meta")

	return nil
}

// Empty permanently deletes all trashed items.
func (m *Manager) Empty() (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	entries, err := os.ReadDir(m.trashDir)
	if err != nil {
		return 0, fmt.Errorf("trash: read dir: %w", err)
	}

	count := 0
	for _, e := range entries {
		path := filepath.Join(m.trashDir, e.Name())
		if err := os.RemoveAll(path); err != nil {
			continue // skip failures
		}
		count++
	}

	return count, nil
}

// ResolveTrashName finds a trashed item by partial name match.
// Useful when the user provides just the original filename.
func (m *Manager) ResolveTrashName(partial string) (string, error) {
	items, err := m.List()
	if err != nil {
		return "", err
	}

	var matches []Meta
	for _, item := range items {
		if item.TrashName == partial {
			return item.TrashName, nil
		}
		// Match by original filename
		if strings.Contains(item.TrashName, partial) || strings.Contains(filepath.Base(item.OriginalPath), partial) {
			matches = append(matches, item)
		}
	}

	if len(matches) == 0 {
		return "", fmt.Errorf("trash: no item matching %q", partial)
	}
	if len(matches) == 1 {
		return matches[0].TrashName, nil
	}

	// Multiple matches — list them
	var b strings.Builder
	b.WriteString(fmt.Sprintf("Multiple items match %q:\n", partial))
	for _, m := range matches {
		b.WriteString(fmt.Sprintf("  %s ← %s\n", m.TrashName, m.OriginalPath))
	}
	return "", fmt.Errorf("%s", b.String())
}
