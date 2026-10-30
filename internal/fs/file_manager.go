package fs

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"syscall"

	"remoteaccess/internal/protocol"
)

// ListDrives returns the available storage drive letters on Windows (e.g. ["C:\\", "D:\\"])
func ListDrives() []string {
	if runtime.GOOS != "windows" {
		return []string{"/"}
	}

	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	getLogicalDrives := kernel32.NewProc("GetLogicalDrives")

	bitmask, _, _ := getLogicalDrives.Call()
	if bitmask == 0 {
		return []string{"C:\\"}
	}

	var drives []string
	for i := 0; i < 26; i++ {
		if (bitmask & (1 << i)) != 0 {
			driveLetter := fmt.Sprintf("%c:\\", 'A'+i)
			drives = append(drives, driveLetter)
		}
	}

	if len(drives) == 0 {
		return []string{"C:\\"}
	}
	return drives
}

// GetDefaultRoot returns a sensible starting directory (Desktop, User Profile, or C:\)
func GetDefaultRoot() string {
	home, err := os.UserHomeDir()
	if err == nil && home != "" {
		return home
	}
	drives := ListDrives()
	if len(drives) > 0 {
		return drives[0]
	}
	return "C:\\"
}

// ListDirectory reads files and directories at the specified path
func ListDirectory(targetPath string) (string, []protocol.FSItem, error) {
	cleanPath := strings.TrimSpace(targetPath)
	if cleanPath == "" {
		cleanPath = GetDefaultRoot()
	}

	// Resolve absolute path
	absPath, err := filepath.Abs(cleanPath)
	if err != nil {
		absPath = cleanPath
	}

	entries, err := os.ReadDir(absPath)
	if err != nil {
		return absPath, nil, fmt.Errorf("não foi possível abrir pasta: %v", err)
	}

	var items []protocol.FSItem
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}

		item := protocol.FSItem{
			Name:    entry.Name(),
			IsDir:   entry.IsDir(),
			Size:    info.Size(),
			ModTime: info.ModTime().Unix(),
		}
		items = append(items, item)
	}

	// Sort directories first, then alphabetical by name
	sort.Slice(items, func(i, j int) bool {
		if items[i].IsDir != items[j].IsDir {
			return items[i].IsDir // Dirs first
		}
		return strings.ToLower(items[i].Name) < strings.ToLower(items[j].Name)
	})

	return absPath, items, nil
}

// DeleteItem removes a file or empty folder
func DeleteItem(targetPath string) error {
	cleanPath := filepath.Clean(strings.TrimSpace(targetPath))
	if cleanPath == "" || cleanPath == "." || cleanPath == "/" || len(cleanPath) <= 3 {
		return fmt.Errorf("caminho protegido contra exclusão: %s", cleanPath)
	}
	return os.RemoveAll(cleanPath)
}

// CreateDirectory creates a new folder
func CreateDirectory(parentPath, newFolderName string) (string, error) {
	cleanParent := filepath.Clean(strings.TrimSpace(parentPath))
	cleanName := filepath.Base(strings.TrimSpace(newFolderName))
	if cleanName == "" || cleanName == "." || cleanName == "/" {
		return "", fmt.Errorf("nome de pasta inválido")
	}

	destPath := filepath.Join(cleanParent, cleanName)
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return "", err
	}
	return destPath, nil
}
