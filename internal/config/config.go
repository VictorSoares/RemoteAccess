package config

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// Priority 1: ProgramData (C:\ProgramData\RemoteAccess\config.json) - Machine-wide, shared seamlessly between Standard User and Admin
	progData := os.Getenv("ProgramData")
	if progData == "" {
		progData = `C:\ProgramData`
	}
	sharedDir := filepath.Join(progData, "RemoteAccess")
	sharedFile := filepath.Join(sharedDir, "config.json")

	// Priority 2: User AppData (%APPDATA%\RemoteAccess\config.json)
	appData := os.Getenv("APPDATA")
	var userFile string
	if appData != "" {
		userFile = filepath.Join(appData, "RemoteAccess", "config.json")
	}

	// If shared config already exists in ProgramData, use it!
	if _, err := os.Stat(sharedFile); err == nil {
		return sharedFile
	}

	// If user config exists in AppData, migrate it to ProgramData so Admin also sees it!
	if userFile != "" {
		if data, err := os.ReadFile(userFile); err == nil && len(data) > 0 {
			if err := os.MkdirAll(sharedDir, 0777); err == nil {
				if err := os.WriteFile(sharedFile, data, 0666); err == nil {
					return sharedFile
				}
			}
			return userFile
		}
	}

	// Create in ProgramData if possible
	if err := os.MkdirAll(sharedDir, 0777); err == nil {
		return sharedFile
	}

	// Fallback to AppData
	if appData != "" {
		dir := filepath.Join(appData, "RemoteAccess")
		_ = os.MkdirAll(dir, 0755)
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
			}
			if strings.TrimSpace(cm.Data.SignalingURL) == "" {
				cm.Data.SignalingURL = DefaultSignalingURL
			}
			_ = cm.Save()
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
	advapi32          = syscall.NewLazyDLL("advapi32.dll")
	shell32           = syscall.NewLazyDLL("shell32.dll")
	procRegOpenKeyEx  = advapi32.NewProc("RegOpenKeyExW")
	procRegSetValue   = advapi32.NewProc("RegSetValueExW")
	procRegDeleteVal  = advapi32.NewProc("RegDeleteValueW")
	procRegCloseKey   = advapi32.NewProc("RegCloseKey")
	procIsUserAnAdmin = shell32.NewProc("IsUserAnAdmin")
	procShellExecuteW = shell32.NewProc("ShellExecuteW")
)

const (
	hkeyCurrentUser   = 0x80000001
	hkeyLocalMachine  = 0x80000002
	keyAllAccess      = 0xF003F
	regSz             = 1
)

// IsAdmin returns true if current process is running with elevated administrator privileges
func IsAdmin() bool {
	if runtime.GOOS != "windows" {
		return false
	}
	if procIsUserAnAdmin.Find() == nil {
		ret, _, _ := procIsUserAnAdmin.Call()
		return ret != 0
	}
	return false
}

// IsInstalled returns true if running from Program Files
func IsInstalled() bool {
	exePath, err := os.Executable()
	if err != nil {
		return false
	}
	progFiles := os.Getenv("ProgramFiles")
	if progFiles != "" && strings.HasPrefix(strings.ToLower(exePath), strings.ToLower(progFiles)) {
		return true
	}
	return false
}

// ElevateSelf triggers UAC prompt to restart current executable as Administrator
func ElevateSelf(args string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	exePath, err := os.Executable()
	if err != nil {
		return err
	}
	verb, _ := syscall.UTF16PtrFromString("runas")
	file, _ := syscall.UTF16PtrFromString(exePath)
	params, _ := syscall.UTF16PtrFromString(args)

	ret, _, _ := procShellExecuteW.Call(
		0,
		uintptr(unsafe.Pointer(verb)),
		uintptr(unsafe.Pointer(file)),
		uintptr(unsafe.Pointer(params)),
		0,
		1, // SW_SHOWNORMAL
	)
	if ret <= 32 {
		return fmt.Errorf("solicitação de elevação cancelada ou falhou (código %d)", ret)
	}
	return nil
}

// InstallAsAdmin installs the application into Program Files and sets up elevated auto-start
func (cm *ConfigManager) InstallAsAdmin() error {
	if !IsAdmin() {
		return ElevateSelf("-install")
	}

	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	targetDir := filepath.Join(progFiles, "RemoteAccess")
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("falha ao criar diretório em Program Files: %w", err)
	}

	targetExe := filepath.Join(targetDir, "RemoteAccess.exe")
	currentExe, err := os.Executable()
	if err != nil {
		return err
	}

	if strings.ToLower(currentExe) != strings.ToLower(targetExe) {
		inputData, err := os.ReadFile(currentExe)
		if err != nil {
			return fmt.Errorf("falha ao ler executável atual: %w", err)
		}
		if err := os.WriteFile(targetExe, inputData, 0755); err != nil {
			return fmt.Errorf("falha ao copiar executável para %s: %w", targetExe, err)
		}
	}

	// Update executable path
	cm.executable = targetExe
	_ = cm.SetAutoStart(true)

	return nil
}

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

	// If admin, we can also register in HKLM for all users
	rootKey := uintptr(hkeyCurrentUser)
	if IsAdmin() {
		rootKey = uintptr(hkeyLocalMachine)
	}

	var hKey uintptr
	ret, _, _ := procRegOpenKeyEx.Call(
		rootKey,
		uintptr(unsafe.Pointer(subKey)),
		0,
		uintptr(keyAllAccess),
		uintptr(unsafe.Pointer(&hKey)),
	)
	if ret != 0 {
		// Fallback to HKCU if HKLM fails
		rootKey = uintptr(hkeyCurrentUser)
		ret, _, _ = procRegOpenKeyEx.Call(
			rootKey,
			uintptr(unsafe.Pointer(subKey)),
			0,
			uintptr(keyAllAccess),
			uintptr(unsafe.Pointer(&hKey)),
		)
		if ret != 0 {
			return fmt.Errorf("failed to open registry key: code %d", ret)
		}
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

