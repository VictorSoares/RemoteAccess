package config

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestRandomIDGeneration(t *testing.T) {
	for i := 0; i < 50; i++ {
		id := generateRandomID()
		if len(id) != 6 {
			t.Errorf("Expected 6-character ID, got %s (length %d)", id, len(id))
		}
		num, err := strconv.Atoi(id)
		if err != nil {
			t.Fatalf("ID %s is not numeric: %v", id, err)
		}
		if num < 100000 || num > 999999 {
			t.Errorf("ID %d out of expected range [100000, 999999]", num)
		}
	}
}

func TestRandomPasswordGeneration(t *testing.T) {
	for i := 0; i < 50; i++ {
		pwd := generateRandomPassword()
		if len(pwd) != 6 {
			t.Errorf("Expected 6-character password, got %s (length %d)", pwd, len(pwd))
		}
	}
}

func TestConfigSaveAndLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "ra_config_test_*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	testFilePath := filepath.Join(tmpDir, "config.json")
	cm := &ConfigManager{
		filePath: testFilePath,
		Data: AppConfig{
			ID:           "999888",
			Alias:        "Test Machine",
			Password:     "TEST12",
			Quality:      70,
			FPS:          30,
			SignalingURL: DefaultSignalingURL,
		},
	}

	if err := cm.Save(); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	if err := cm.SetPassword("NEW_PASS_123"); err != nil {
		t.Fatalf("Failed to set password: %v", err)
	}
	if cm.Data.Password != "NEW_PASS_123" {
		t.Errorf("Expected password NEW_PASS_123, got %s", cm.Data.Password)
	}

	if err := cm.SetAlias("Renamed Host"); err != nil {
		t.Fatalf("Failed to set alias: %v", err)
	}
	if cm.Data.Alias != "Renamed Host" {
		t.Errorf("Expected alias 'Renamed Host', got %s", cm.Data.Alias)
	}

	if err := cm.SetConfig(85, 60); err != nil {
		t.Fatalf("Failed to set config: %v", err)
	}
	if cm.Data.Quality != 85 || cm.Data.FPS != 60 {
		t.Errorf("Expected Q:85, FPS:60, got Q:%d, FPS:%d", cm.Data.Quality, cm.Data.FPS)
	}
}
