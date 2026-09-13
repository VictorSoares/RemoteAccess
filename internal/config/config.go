package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

type AppConfig struct {
	ID           string `json:"id"`
	Alias        string `json:"alias"`
	Password     string `json:"password"`
	Quality      int    `json:"quality"`
	FPS          int    `json:"fps"`
	AutoStart    bool   `json:"auto_start"`
	SignalingURL string `json:"signaling_url"`
	SaveLogFile  bool   `json:"save_log_file"`
}

type ConfigManager struct {
	mu         sync.RWMutex
	filePath   string
	Data       AppConfig
	executable string
}

func getConfigPath() string {
	// Priority 1: AppData (%APPDATA%\RemoteAccess\config.json) - keeps executable directory 100% clean
	appData := os.Getenv("APPDATA")
	if appData != "" {
		dir := filepath.Join(appData, "RemoteAccess")
		_ = os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "config.json")
	}

	// Priority 2: UserProfile
	userProfile := os.Getenv("USERPROFILE")
	if userProfile != "" {
		dir := filepath.Join(userProfile, ".remoteaccess")
		_ = os.MkdirAll(dir, 0755)
		return filepath.Join(dir, "config.json")
	}

	// Priority 3: Alongside executable
	exePath, err := os.Executable()
	if err == nil {
		dir := filepath.Dir(exePath)
		return filepath.Join(dir, "config.json")
	}

	return "config.json"
}

func generateRandomID() string {
	n, _ := rand.Int(rand.Reader, big.NewInt(900000))
	return fmt.Sprintf("%06d", n.Int64()+100000)
}

func generateRandomPassword() string {
	const charset = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(charset))))
		b[i] = charset[n.Int64()]
	}
	return string(b)
}

const DefaultSignalingURL = "https://remoteaccess-ltwx.onrender.com"

func LoadConfig() *ConfigManager {
	configPath := getConfigPath()
	exePath, _ := os.Executable()
	hostname, _ := os.Hostname()
	if hostname == "" {
		hostname = "Meu PC"
	}

	cm := &ConfigManager{
		filePath:   configPath,
		executable: exePath,
		Data: AppConfig{
			Alias:        hostname,
			Quality:      65,
			FPS:          30,
			SignalingURL: DefaultSignalingURL,
		},
	}

	data, err := os.ReadFile(configPath)
	if err == nil {
		if err := json.Unmarshal(data, &cm.Data); err == nil && cm.Data.ID != "" {
			if cm.Data.Alias == "" {
				cm.Data.Alias = hostname
				_ = cm.Save()
			}
			if cm.Data.SignalingURL == "" {
				cm.Data.SignalingURL = DefaultSignalingURL
				_ = cm.Save()
			}
			return cm
		}
	}

	// Generate new fixed ID and initial Password
	cm.Data.ID = generateRandomID()
	cm.Data.Alias = hostname
	cm.Data.Password = generateRandomPassword()
	cm.Data.SignalingURL = DefaultSignalingURL
	_ = cm.Save()
	return cm
}

func (cm *ConfigManager) Save() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	data, err := json.MarshalIndent(cm.Data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(cm.filePath, data, 0644)
}

func (cm *ConfigManager) SetPassword(newPwd string) error {
	cm.mu.Lock()
	cm.Data.Password = newPwd
	cm.mu.Unlock()
	return cm.Save()
}

func (cm *ConfigManager) SetAlias(alias string) error {
	cm.mu.Lock()
	cm.Data.Alias = alias
	cm.mu.Unlock()
	return cm.Save()
}

func (cm *ConfigManager) SetSignalingURL(url string) error {
	cm.mu.Lock()
	cm.Data.SignalingURL = url
	cm.mu.Unlock()
	return cm.Save()
}

func (cm *ConfigManager) SetConfig(quality, fps int) error {
	cm.mu.Lock()
	if quality > 0 {
		cm.Data.Quality = quality
	}
	if fps > 0 {
		cm.Data.FPS = fps
	}
	cm.mu.Unlock()
	return cm.Save()
}

func (cm *ConfigManager) SetSaveLogFile(enable bool) error {
	cm.mu.Lock()
	cm.Data.SaveLogFile = enable
	cm.mu.Unlock()
	return cm.Save()
}

// Windows Registry Auto-start management (HKCU - No Admin Needed)
var (
	advapi32         = syscall.NewLazyDLL("advapi32.dll")
	procRegOpenKeyEx = advapi32.NewProc("RegOpenKeyExW")
	procRegSetValue  = advapi32.NewProc("RegSetValueExW")
	procRegDeleteVal = advapi32.NewProc("RegDeleteValueW")
	procRegCloseKey  = advapi32.NewProc("RegCloseKey")
)

const (
	hkeyCurrentUser = 0x80000001
	keyAllAccess     = 0xF003F
	regSz            = 1
)

func (cm *ConfigManager) SetAutoStart(enable bool) error {
	if runtime.GOOS != "windows" {
		return nil
	}

	cm.mu.Lock()
	cm.Data.AutoStart = enable
	cm.mu.Unlock()
	_ = cm.Save()

	subKey, _ := syscall.UTF16PtrFromString(`Software\Microsoft\Windows\CurrentVersion\Run`)
	valueName, _ := syscall.UTF16PtrFromString("RemoteAccess")

	var hKey uintptr
	ret, _, _ := procRegOpenKeyEx.Call(
		uintptr(hkeyCurrentUser),
		uintptr(unsafe.Pointer(subKey)),
		0,
		uintptr(keyAllAccess),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 {
		return fmt.Errorf("failed to open registry key: code %d", ret)
	}
	defer procRegCloseKey.Call(hKey)

	if enable {
		// Run in background without automatically launching browser
		cmdLine := fmt.Sprintf("\"%s\" -no-browser", cm.executable)
		valBytes, _ := syscall.UTF16FromString(cmdLine)

		ret, _, _ = procRegSetValue.Call(
			hKey,
			uintptr(unsafe.Pointer(valueName)),
			0,
			uintptr(regSz),
			uintptr(unsafe.Pointer(&valBytes[0])),
			uintptr(len(valBytes)*2),
		)
		if ret != 0 {
			return fmt.Errorf("failed to set registry value: code %d", ret)
		}
	} else {
		procRegDeleteVal.Call(hKey, uintptr(unsafe.Pointer(valueName)))
	}

	return nil
}
