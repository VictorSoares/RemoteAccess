package fs

import (
	"os"
	"path/filepath"
	"testing"
)

func TestListDrives(t *testing.T) {
	drives := ListDrives()
	if len(drives) == 0 {
		t.Fatalf("Expected at least one drive, got none")
	}
	t.Logf("Detected drives: %v", drives)
}

func TestListDirectoryAndOperations(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "ra_fs_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create test structure
	subDirName := "test_sub_folder"
	subDirPath, err := CreateDirectory(tempDir, subDirName)
	if err != nil {
		t.Fatalf("Failed to create sub dir: %v", err)
	}

	testFile := filepath.Join(tempDir, "sample.txt")
	if err := os.WriteFile(testFile, []byte("Hello RemoteAccess File Manager"), 0644); err != nil {
		t.Fatalf("Failed to write test file: %v", err)
	}

	absPath, items, err := ListDirectory(tempDir)
	if err != nil {
		t.Fatalf("ListDirectory failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(items))
	}

	// Verify dir is sorted first
	if !items[0].IsDir || items[0].Name != subDirName {
		t.Errorf("Expected first item to be directory %s, got %+v", subDirName, items[0])
	}
	if items[1].IsDir || items[1].Name != "sample.txt" {
		t.Errorf("Expected second item to be file sample.txt, got %+v", items[1])
	}

	// Test Delete
	if err := DeleteItem(subDirPath); err != nil {
		t.Fatalf("Failed to delete sub folder: %v", err)
	}

	_, itemsAfter, err := ListDirectory(absPath)
	if err != nil {
		t.Fatalf("ListDirectory after delete failed: %v", err)
	}
	if len(itemsAfter) != 1 {
		t.Errorf("Expected 1 item remaining after deletion, got %d", len(itemsAfter))
	}
}
