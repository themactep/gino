package trash

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTrashAndRestore(t *testing.T) {
	// Create a temp workspace
	workspace := t.TempDir()

	// Create a test file
	testFile := filepath.Join(workspace, "testfile.txt")
	_ = os.WriteFile(testFile, []byte("hello world"), 0o644)

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Trash the file
	trashName, err := mgr.Trash(testFile)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// File should no longer exist
	if _, err := os.Stat(testFile); !os.IsNotExist(err) {
		t.Fatal("file should be gone after trashing")
	}

	// File should exist in trash
	trashPath := filepath.Join(mgr.TrashDir(), trashName)
	if _, err := os.Stat(trashPath); err != nil {
		t.Fatalf("file should exist in trash: %v", err)
	}

	// Metadata should exist
	metaPath := trashPath + ".meta"
	if _, err := os.Stat(metaPath); err != nil {
		t.Fatalf("metadata should exist: %v", err)
	}

	// List should show one item
	items, err := mgr.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].OriginalPath != testFile {
		t.Fatalf("expected original path %s, got %s", testFile, items[0].OriginalPath)
	}

	// Restore the file
	restoredPath, err := mgr.Restore(trashName)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restoredPath != testFile {
		t.Fatalf("expected restored path %s, got %s", testFile, restoredPath)
	}

	// File should be back
	data, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("file should be readable after restore: %v", err)
	}
	if string(data) != "hello world" {
		t.Fatalf("file content mismatch: got %q", string(data))
	}

	// Trash should be empty now
	items, err = mgr.List()
	if err != nil {
		t.Fatalf("List after restore: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("trash should be empty after restore, got %d items", len(items))
	}
}

func TestTrashDirectory(t *testing.T) {
	workspace := t.TempDir()

	// Create a test directory with files
	testDir := filepath.Join(workspace, "mydir")
	_ = os.MkdirAll(testDir, 0o755)
	_ = os.WriteFile(filepath.Join(testDir, "a.txt"), []byte("a"), 0o644)
	_ = os.WriteFile(filepath.Join(testDir, "b.txt"), []byte("b"), 0o644)

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	// Trash the directory
	trashName, err := mgr.Trash(testDir)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Directory should be gone
	if _, err := os.Stat(testDir); !os.IsNotExist(err) {
		t.Fatal("directory should be gone after trashing")
	}

	// Restore
	restoredPath, err := mgr.Restore(trashName)
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if restoredPath != testDir {
		t.Fatalf("expected %s, got %s", testDir, restoredPath)
	}

	// Files should be intact
	data, err := os.ReadFile(filepath.Join(testDir, "a.txt"))
	if err != nil || string(data) != "a" {
		t.Fatalf("file a.txt not restored correctly")
	}
}

func TestPurge(t *testing.T) {
	workspace := t.TempDir()

	testFile := filepath.Join(workspace, "goner.txt")
	_ = os.WriteFile(testFile, []byte("goodbye"), 0o644)

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	trashName, err := mgr.Trash(testFile)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Purge the trashed item
	if err := mgr.Purge(trashName); err != nil {
		t.Fatalf("Purge: %v", err)
	}

	// Should be permanently gone
	trashPath := filepath.Join(mgr.TrashDir(), trashName)
	if _, err := os.Stat(trashPath); !os.IsNotExist(err) {
		t.Fatal("file should be permanently gone after purge")
	}

	// Trash should be empty
	items, _ := mgr.List()
	if len(items) != 0 {
		t.Fatalf("trash should be empty after purge")
	}
}

func TestEmpty(t *testing.T) {
	workspace := t.TempDir()

	// Create and trash multiple files
	for i := 0; i < 5; i++ {
		name := filepath.Join(workspace, "file"+string(rune('A'+i))+".txt")
		_ = os.WriteFile(name, []byte("data"), 0o644)
	}

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	for i := 0; i < 5; i++ {
		name := filepath.Join(workspace, "file"+string(rune('A'+i))+".txt")
		if _, err := mgr.Trash(name); err != nil {
			t.Fatalf("Trash %d: %v", i, err)
		}
	}

	items, _ := mgr.List()
	if len(items) != 5 {
		t.Fatalf("expected 5 items, got %d", len(items))
	}

	count, err := mgr.Empty()
	if err != nil {
		t.Fatalf("Empty: %v", err)
	}
	if count != 10 { // 5 files + 5 meta files
		t.Logf("Empty removed %d items (includes meta files)", count)
	}

	items, _ = mgr.List()
	if len(items) != 0 {
		t.Fatalf("trash should be empty after Empty(), got %d items", len(items))
	}
}

func TestRestoreOccupiedPath(t *testing.T) {
	workspace := t.TempDir()

	testFile := filepath.Join(workspace, "occupied.txt")
	_ = os.WriteFile(testFile, []byte("original"), 0o644)

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	trashName, err := mgr.Trash(testFile)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Re-create the file at the original location
	_ = os.WriteFile(testFile, []byte("new content"), 0o644)

	// Restore should fail
	_, err = mgr.Restore(trashName)
	if err == nil {
		t.Fatal("Restore should fail when original path is occupied")
	}
}

func TestResolveTrashName(t *testing.T) {
	workspace := t.TempDir()

	testFile := filepath.Join(workspace, "findme.txt")
	_ = os.WriteFile(testFile, []byte("content"), 0o644)

	mgr, err := NewManager(workspace)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	trashName, err := mgr.Trash(testFile)
	if err != nil {
		t.Fatalf("Trash: %v", err)
	}

	// Resolve by partial name
	resolved, err := mgr.ResolveTrashName("findme")
	if err != nil {
		t.Fatalf("ResolveTrashName: %v", err)
	}
	if resolved != trashName {
		t.Fatalf("expected %s, got %s", trashName, resolved)
	}

	// Resolve by exact name
	resolved, err = mgr.ResolveTrashName(trashName)
	if err != nil {
		t.Fatalf("ResolveTrashName exact: %v", err)
	}
	if resolved != trashName {
		t.Fatalf("expected %s, got %s", trashName, resolved)
	}

	// Non-existent
	_, err = mgr.ResolveTrashName("nonexistent")
	if err == nil {
		t.Fatal("should fail for non-existent item")
	}
}
