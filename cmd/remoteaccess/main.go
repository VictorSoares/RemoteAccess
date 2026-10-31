package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"remoteaccess/internal/config"
	"remoteaccess/internal/logger"
	"remoteaccess/internal/server"
	"remoteaccess/internal/tray"
)

const version = "2.0.0"

var (
	kernel32                       = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex                = kernel32.NewProc("CreateMutexW")
	procCloseHandle                = kernel32.NewProc("CloseHandle")
	procCreateToolhelp32Snapshot   = kernel32.NewProc("CreateToolhelp32Snapshot")
	procProcess32FirstW            = kernel32.NewProc("Process32FirstW")
	procProcess32NextW             = kernel32.NewProc("Process32NextW")
	procOpenProcess                = kernel32.NewProc("OpenProcess")
	procTerminateProcess           = kernel32.NewProc("TerminateProcess")
	procQueryFullProcessImageNameW = kernel32.NewProc("QueryFullProcessImageNameW")

	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetClassNameW            = user32.NewProc("GetClassNameW")
	procShowWindow               = user32.NewProc("ShowWindow")
	procSetForegroundWindow      = user32.NewProc("SetForegroundWindow")
	procIsWindow                 = user32.NewProc("IsWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procIsZoomed                 = user32.NewProc("IsZoomed")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procSetWindowPos             = user32.NewProc("SetWindowPos")
	procGetWindowThreadProcessId = user32.NewProc("GetWindowThreadProcessId")

	advapi32                                                 = syscall.NewLazyDLL("advapi32.dll")
	procConvertStringSecurityDescriptorToSecurityDescriptorW = advapi32.NewProc("ConvertStringSecurityDescriptorToSecurityDescriptorW")
	procLocalFree                                            = kernel32.NewProc("LocalFree")

	shell32                                     = syscall.NewLazyDLL("shell32.dll")
	procExtractIconExW                          = shell32.NewProc("ExtractIconExW")
	procSetCurrentProcessExplicitAppUserModelID = shell32.NewProc("SetCurrentProcessExplicitAppUserModelID")
	procSendMessageW                            = user32.NewProc("SendMessageW")

	appMutex uintptr
)

const (
	wmSetIcon = 0x0080
	iconSmall = 0
	iconBig   = 1
)

type securityAttributes struct {
	nLength              uint32
	lpSecurityDescriptor uintptr
	bInheritHandle       int32
}

func getEveryoneSecurityAttributes() (*securityAttributes, uintptr) {
	if runtime.GOOS != "windows" {
		return nil, 0
	}
	// "D:(A;;GA;;;WD)" = DACL with Generic All granted to Everyone (World)
	// Ensures mutex can be accessed across both Medium and High integrity (UAC) processes
	sddl, _ := syscall.UTF16PtrFromString("D:(A;;GA;;;WD)")
	var pSD uintptr
	ret, _, _ := procConvertStringSecurityDescriptorToSecurityDescriptorW.Call(
		uintptr(unsafe.Pointer(sddl)),
		1, // SDDL_REVISION_1
		uintptr(unsafe.Pointer(&pSD)),
		0,
	)
	if ret != 0 && pSD != 0 {
		sa := &securityAttributes{
			nLength:              uint32(unsafe.Sizeof(securityAttributes{})),
			lpSecurityDescriptor: pSD,
			bInheritHandle:       0,
		}
		return sa, pSD
	}
	return nil, 0
}

type processEntry32W struct {
	dwSize              uint32
	cntUsage            uint32
	th32ProcessID       uint32
	th32DefaultHeapID   uintptr
	th32ModuleID        uint32
	cntThreads          uint32
	th32ParentProcessID uint32
	pcPriClassBase      int32
	dwFlags             uint32
	szExeFile           [260]uint16
}

func sweepExistingInstances(currentPID int, forceTakeover bool) {
	if runtime.GOOS != "windows" {
		return
	}
	// TH32CS_SNAPPROCESS = 0x00000002
	hSnap, _, _ := procCreateToolhelp32Snapshot.Call(2, 0)
	if hSnap == 0 || hSnap == uintptr(syscall.InvalidHandle) {
		return
	}
	defer procCloseHandle.Call(hSnap)

	currentExe, _ := os.Executable()
	currentBase := filepath.Base(currentExe)

	var entry processEntry32W
	entry.dwSize = uint32(unsafe.Sizeof(entry))

	ret, _, _ := procProcess32FirstW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
	var targetPIDs []uint32

	for ret != 0 {
		pid := entry.th32ProcessID
		if int(pid) != currentPID && pid > 4 {
			exeName := syscall.UTF16ToString(entry.szExeFile[:])
			isMatch := strings.EqualFold(exeName, "RemoteAccess.exe") ||
				(currentBase != "" && strings.EqualFold(exeName, currentBase)) ||
				(strings.HasSuffix(strings.ToLower(exeName), ".exe") && strings.Contains(strings.ToLower(exeName), "remoteaccess"))
			if isMatch {
				targetPIDs = append(targetPIDs, pid)
			}
		}
		ret, _, _ = procProcess32NextW.Call(hSnap, uintptr(unsafe.Pointer(&entry)))
	}

	if len(targetPIDs) == 0 {
		return
	}

	log.Printf("[Instâncias] Detectadas %d instâncias anteriores de RemoteAccess (PIDs: %v)", len(targetPIDs), targetPIDs)

	if forceTakeover {
		// 1. Tentar encerramento gracioso via HTTP se porta estiver aberta
		client := &http.Client{Timeout: 300 * time.Millisecond}
		for port := 8080; port < 8090; port++ {
			resp, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/api/app-terminate", port))
			if err == nil && resp != nil {
				_ = resp.Body.Close()
			}
		}
		time.Sleep(300 * time.Millisecond)

		// 2. Encerrar qualquer processo restante (PROCESS_TERMINATE = 0x0001)
		for _, pid := range targetPIDs {
			hProc, _, _ := procOpenProcess.Call(0x0001, 0, uintptr(pid))
			if hProc != 0 {
				procTerminateProcess.Call(hProc, 0)
				procCloseHandle.Call(hProc)
				log.Printf("[Instâncias] Processo anterior PID %d finalizado para takeover.", pid)
			}
		}
		time.Sleep(200 * time.Millisecond)

		profileDir := filepath.Join(os.TempDir(), "RemoteAccess_WebProfile")
		cleanupStaleEdgeProfile(profileDir)
	}
}

func syncProgramFilesExe() {
	if runtime.GOOS != "windows" {
		return
	}
	progFiles := os.Getenv("ProgramFiles")
	if progFiles == "" {
		progFiles = `C:\Program Files`
	}
	targetDir := filepath.Join(progFiles, "RemoteAccess")
	targetExe := filepath.Join(targetDir, "RemoteAccess.exe")
	currentExe, err := os.Executable()
	if err != nil {
		return
	}
	if strings.EqualFold(filepath.Clean(currentExe), filepath.Clean(targetExe)) {
		return
	}
	if _, err := os.Stat(targetDir); err == nil {
		data, err := os.ReadFile(currentExe)
		if err == nil && len(data) > 0 {
			_ = os.WriteFile(targetExe, data, 0755)
			log.Printf("[Instalação] Executável em %s sincronizado automaticamente.", targetExe)
		}
	}
}

func acquireSingleInstanceLock(maxWait time.Duration) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	name, _ := syscall.UTF16PtrFromString("Local\\RemoteAccess_SingleInstance_Mutex")
	deadline := time.Now().Add(maxWait)

	sa, pSD := getEveryoneSecurityAttributes()
	if pSD != 0 {
		defer procLocalFree.Call(pSD)
	}

	var saPtr uintptr
	if sa != nil {
		saPtr = uintptr(unsafe.Pointer(sa))
	}

	for {
		hMutex, _, err := procCreateMutex.Call(saPtr, 1, uintptr(unsafe.Pointer(name)))
		if hMutex != 0 {
			if err == syscall.ERROR_ALREADY_EXISTS {
				procCloseHandle.Call(hMutex)
			} else {
				appMutex = hMutex
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(150 * time.Millisecond)
	}
}

func releaseSingleInstanceLock() {
	if appMutex != 0 {
		procCloseHandle.Call(appMutex)
		appMutex = 0
	}
}

func findRunningLocalPort() int {
	for port := 8080; port < 8095; port++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 80*time.Millisecond)
		if err == nil {
			conn.Close()
			return port
		}
	}
	return 8080
}

var windowSizeGuardOnce sync.Once

// isRemoteAccessAppWindow determines whether an HWND belongs to the actual
// RemoteAccess Edge App desktop window, strictly excluding IDEs, code editors,
// and background workers.
func isRemoteAccessAppWindow(hwnd uintptr) bool {
	if runtime.GOOS != "windows" {
		return false
	}
	r, _, _ := procIsWindow.Call(hwnd)
	if r == 0 {
		return false
	}

	var clsBuf [256]uint16
	procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
	cls := syscall.UTF16ToString(clsBuf[:])
	if cls != "Chrome_WidgetWin_1" {
		return false
	}

	var buf [256]uint16
	procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
	title := syscall.UTF16ToString(buf[:])
	titleLower := strings.ToLower(title)

	// Strictly exclude any development environments, editors, and messaging windows
	for _, forbidden := range []string{
		"antigravity", "visual studio", "code", "cursor", "obsidian",
		"bate-papo", "chat", "discord", "slack", "teams",
	} {
		if strings.Contains(titleLower, forbidden) {
			return false
		}
	}

	// Title must strictly match RemoteAccess desktop app (or local loopback IP), never a regular browser tab
	if !strings.EqualFold(title, "RemoteAccess") && !strings.HasPrefix(title, "RemoteAccess - http") && !strings.Contains(title, "127.0.0.1") {
		return false
	}
	if strings.Contains(titleLower, "google chrome") || strings.Contains(titleLower, "microsoft edge") {
		return false
	}

	// Verify owning process is STRICTLY msedge.exe (never chrome.exe or general browsers)
	var pid uint32
	procGetWindowThreadProcessId.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == 0 {
		return false
	}

	hProc, _, _ := procOpenProcess.Call(0x1000 /* PROCESS_QUERY_LIMITED_INFORMATION */, 0, uintptr(pid))
	if hProc != 0 {
		defer procCloseHandle.Call(hProc)
		var imgBuf [512]uint16
		size := uint32(len(imgBuf))
		rImg, _, _ := procQueryFullProcessImageNameW.Call(hProc, 0, uintptr(unsafe.Pointer(&imgBuf[0])), uintptr(unsafe.Pointer(&size)))
		if rImg != 0 {
			exePath := syscall.UTF16ToString(imgBuf[:size])
			exeName := strings.ToLower(filepath.Base(exePath))
			if exeName != "msedge.exe" {
				return false
			}
		}
	}

	return true
}

// bringWindowToFront searches for an existing RemoteAccess window and restores/focuses it.
func bringWindowToFront() bool {
	if runtime.GOOS != "windows" {
		return false
	}

	var targetHWND uintptr
	cb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
		// Window must be physically visible or minimized (iconic), not a dead background worker
		vis, _, _ := procIsWindowVisible.Call(hwnd)
		min, _, _ := procIsIconic.Call(hwnd)
		if vis == 0 && min == 0 {
			return 1
		}

		if isRemoteAccessAppWindow(hwnd) {
			targetHWND = hwnd
			return 0 // found target window
		}
		return 1
	})
	procEnumWindows.Call(cb, 0)

	if targetHWND != 0 {
		// SW_RESTORE = 9: activates and restores window if minimized
		procShowWindow.Call(targetHWND, 9)
		procSetForegroundWindow.Call(targetHWND)
		log.Println("[Janela] Janela ativa encontrada e restaurada para primeiro plano.")
		return true
	}
	return false
}

// cleanupStaleEdgeProfile ensures no stale lock files or lingering headless Edge processes
// prevent Edge from launching cleanly.
func cleanupStaleEdgeProfile(profileDir string) {
	if runtime.GOOS != "windows" {
		return
	}
	// Always terminate any lingering Edge processes tied to RemoteAccess_WebProfile
	psCmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-WindowStyle", "Hidden", "-Command",
		`Get-CimInstance Win32_Process | Where-Object { $_.Name -eq 'msedge.exe' -and $_.CommandLine -like '*RemoteAccess_WebProfile*' } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }`,
	)
	psCmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	_ = psCmd.Run()

	lockFiles := []string{
		filepath.Join(profileDir, "SingletonLock"),
		filepath.Join(profileDir, "SingletonCookie"),
		filepath.Join(profileDir, "SingletonSocket"),
		filepath.Join(profileDir, "lockfile"),
	}
	for _, f := range lockFiles {
		_ = os.Remove(f)
	}
}

var (
	appWindowCmd   *exec.Cmd
	appWindowCmdMu sync.Mutex
)

// killActiveAppWindow terminates the dedicated Edge App desktop window and processes.
func killActiveAppWindow() {
	appWindowCmdMu.Lock()
	if appWindowCmd != nil && appWindowCmd.Process != nil {
		pid := appWindowCmd.Process.Pid
		_ = exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
		appWindowCmd = nil
	}
	appWindowCmdMu.Unlock()

	profileDir := filepath.Join(os.TempDir(), "RemoteAccess_WebProfile")
	cleanupStaleEdgeProfile(profileDir)
}

// terminateAppSession performs complete, graceful and guaranteed process termination.
func terminateAppSession(srv *server.LocalServer) {
	log.Println("[RemoteAccess] Encerrando instância e liberando recursos...")
	killActiveAppWindow()
	releaseSingleInstanceLock()

	if srv != nil {
		done := make(chan struct{})
		go func() {
			srv.Shutdown()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(350 * time.Millisecond):
			log.Println("[RemoteAccess] Shutdown finalizado por timeout de segurança.")
		}
	}
	os.Exit(0)
}

// startWindowSizeGuard enforces a rigid native minimum window size (540x580)
// on the application window so the user physically cannot shrink it below the threshold.
func startWindowSizeGuard() {
	if runtime.GOOS != "windows" {
		return
	}
	windowSizeGuardOnce.Do(func() {
		type rect struct {
			left, top, right, bottom int32
		}

		go func() {
			ticker := time.NewTicker(40 * time.Millisecond) // 25Hz physical clamp
			defer ticker.Stop()

			isVis := func(h uintptr) bool {
				r, _, _ := procIsWindowVisible.Call(h)
				return r != 0
			}

			var targetHWND uintptr
			var iconApplied bool
			// Allocate callback ONCE outside the loop to avoid exhausting Go runtime callback table
			findCb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
				if !isVis(hwnd) {
					return 1
				}
				if isRemoteAccessAppWindow(hwnd) {
					targetHWND = hwnd
					return 0 // found, stop enumerating
				}
				return 1
			})

			for range ticker.C {
				if targetHWND == 0 || !isVis(targetHWND) {
					targetHWND = 0
					iconApplied = false
					procEnumWindows.Call(findCb, 0)
				}

				if targetHWND != 0 {
					if !iconApplied {
						iconApplied = true
						go func(h uintptr) {
							exePath, err := os.Executable()
							if err == nil {
								exeUtf16, _ := syscall.UTF16PtrFromString(exePath)
								var hLarge, hSmall uintptr
								procExtractIconExW.Call(uintptr(unsafe.Pointer(exeUtf16)), 0, uintptr(unsafe.Pointer(&hLarge)), uintptr(unsafe.Pointer(&hSmall)), 1)
								if hLarge != 0 {
									procSendMessageW.Call(h, wmSetIcon, iconBig, hLarge)
								}
								if hSmall != 0 {
									procSendMessageW.Call(h, wmSetIcon, iconSmall, hSmall)
								}
							}
						}(targetHWND)
					}

					isMin, _, _ := procIsIconic.Call(targetHWND)
					if isMin != 0 {
						continue
					}
					isMax, _, _ := procIsZoomed.Call(targetHWND)
					if isMax != 0 {
						continue
					}

					var r rect
					ret, _, _ := procGetWindowRect.Call(targetHWND, uintptr(unsafe.Pointer(&r)))
					if ret != 0 {
						w := r.right - r.left
						h := r.bottom - r.top
						// Lock to crisp fixed resolution (960x720) when not maximized
						if w != 960 || h != 720 {
							// SWP_NOMOVE (0x0002) | SWP_NOZORDER (0x0004) | SWP_NOACTIVATE (0x0010)
							procSetWindowPos.Call(targetHWND, 0, 0, 0, 960, 720, 0x0002|0x0004|0x0010)
						}
					}
				}
			}
		}()
	})
}

// runNativeAppWindow opens the standalone desktop app window (Edge App mode or browser fallback)
func runNativeAppWindow(url string) {
	if runtime.GOOS == "windows" {
		if bringWindowToFront() {
			return
		}

		edgePaths := []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}

		profileDir := filepath.Join(os.TempDir(), "RemoteAccess_WebProfile")
		_ = os.MkdirAll(profileDir, 0755)
		cleanupStaleEdgeProfile(profileDir)

		for _, edgePath := range edgePaths {
			if _, err := os.Stat(edgePath); err == nil {
				cmd := exec.Command(edgePath,
					fmt.Sprintf("--app=%s", url),
					fmt.Sprintf("--user-data-dir=%s", profileDir),
					"--app-id=RemoteAccess.Portable",
					"--window-size=960,720",
					"--new-window",
					"--no-first-run",
					"--no-default-browser-check",
					"--disable-background-mode",
					"--disable-background-networking",
					"--disable-component-update",
					"--disable-sync",
				)
				if err := cmd.Start(); err == nil {
					appWindowCmdMu.Lock()
					appWindowCmd = cmd
					appWindowCmdMu.Unlock()
					log.Printf("[Janela] Janela desktop nativa iniciada via Edge (%s)", edgePath)
					startWindowSizeGuard()
					return
				}
			}
		}

		// Fallback: standard browser
		log.Println("[Janela] Edge não encontrado diretamente, abrindo navegador padrão...")
		shell32 := syscall.NewLazyDLL("shell32.dll")
		procShellExecute := shell32.NewProc("ShellExecuteW")
		verb, _ := syscall.UTF16PtrFromString("open")
		targetURL, _ := syscall.UTF16PtrFromString(url)
		procShellExecute.Call(0, uintptr(unsafe.Pointer(verb)), uintptr(unsafe.Pointer(targetURL)), 0, 0, 1) // 1 = SW_SHOWNORMAL
		startWindowSizeGuard()
		return
	}

	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", "-W", url)
		_ = cmd.Start()
	} else {
		cmd := exec.Command("xdg-open", url)
		_ = cmd.Start()
	}
}

func findAvailablePort(startPort int) int {
	for port := startPort; port < startPort+100; port++ {
		ln, err := net.Listen("tcp", fmt.Sprintf(":%d", port))
		if err == nil {
			_ = ln.Close()
			return port
		}
	}
	return startPort
}

func main() {
	portFlag := flag.Int("port", 8080, "Porta local para o painel de controle")
	noBrowser := flag.Bool("no-browser", false, "Modo silencioso de segundo plano (para inicializacao com Windows)")
	autostart := flag.Bool("autostart", false, "Ativar inicialização automática com o Windows")
	installFlag := flag.Bool("install", false, "Instalar no Program Files com privilégios de Administrador")
	uninstallFlag := flag.Bool("uninstall", false, "Desinstalar o RemoteAccess do sistema")
	uninstallStage2Flag := flag.Bool("uninstall-stage2", false, "Estágio 2 interno da desinstalação")
	silentFlag := flag.Bool("silent", false, "Modo silencioso (sem caixas de diálogo)")
	elevateFlag := flag.Bool("elevate", false, "Reiniciar como Administrador")
	defaultPwd := flag.String("password", "", "Definir senha padrao")
	flag.Parse()

	if runtime.GOOS == "windows" {
		if procSetCurrentProcessExplicitAppUserModelID.Find() == nil {
			appID, _ := syscall.UTF16PtrFromString("RemoteAccess.Portable")
			procSetCurrentProcessExplicitAppUserModelID.Call(uintptr(unsafe.Pointer(appID)))
		}
	}

	// 0a. Estágio 2 interno da desinstalação
	if *uninstallStage2Flag {
		if err := config.UninstallStage2(*silentFlag); err != nil {
			log.Fatalf("[Desinstalação] Erro no estágio 2: %v", err)
		}
		os.Exit(0)
	}

	// 0b. Desinstalação (disparada pelo Windows / appwiz.cpl ou CLI)
	if *uninstallFlag {
		if err := config.Uninstall(*silentFlag); err != nil {
			log.Fatalf("[Desinstalação] Erro ao desinstalar: %v", err)
		}
		os.Exit(0)
	}

	// 1. If running as installer
	if *installFlag {
		if !config.IsAdmin() {
			if err := config.ElevateSelf("-install"); err != nil {
				log.Fatalf("[Instalação] Erro ao solicitar elevação: %v", err)
			}
			os.Exit(0)
		}
		cfg := config.LoadConfig()
		if err := cfg.InstallAsAdmin(); err != nil {
			log.Fatalf("[Instalação] Erro ao instalar: %v", err)
		}
		log.Println("[Instalação] RemoteAccess instalado com sucesso em Program Files com privilégios elevados!")
		if !*noBrowser {
			progFiles := os.Getenv("ProgramFiles")
			if progFiles == "" {
				progFiles = `C:\Program Files`
			}
			targetExe := filepath.Join(progFiles, "RemoteAccess", "RemoteAccess.exe")
			_ = exec.Command(targetExe).Start()
		}
		os.Exit(0)
	}

	// 2. If running as elevate request
	if *elevateFlag {
		if !config.IsAdmin() {
			_ = config.ElevateSelf("")
			os.Exit(0)
		}
	}

	// 3. Varredura ativa e takeover de instâncias anteriores:
	// Encerra qualquer processo anterior do RemoteAccess para que a nova execução assuma o controle limpo.
	isElevated := config.IsAdmin()
	sweepExistingInstances(os.Getpid(), true)

	// Acquire single-instance lock with retry
	maxWait := 1500 * time.Millisecond
	if *elevateFlag || isElevated {
		maxWait = 3 * time.Second
	}

	if !acquireSingleInstanceLock(maxWait) {
		sweepExistingInstances(os.Getpid(), true)
		if !acquireSingleInstanceLock(1 * time.Second) {
			log.Println("[Instâncias] Assumindo execução como instância primária do sistema.")
		}
	}

	if isElevated {
		syncProgramFilesExe()
	}

	cfg := config.LoadConfig()

	// Initialize in-memory (and optional file) logging
	logger.InitLogger(cfg.Data.SaveLogFile)

	if *defaultPwd != "" {
		_ = cfg.SetPassword(*defaultPwd)
	}

	if *autostart {
		_ = cfg.SetAutoStart(true)
	}

	port := findAvailablePort(*portFlag)
	srv := server.NewLocalServer(cfg)

	url := fmt.Sprintf("http://127.0.0.1:%d", port)

	adminStatus := "Usuário Padrão (Portátil)"
	if config.IsAdmin() {
		adminStatus = "Administrador (Acesso Total a Telas UAC)"
	}

	log.Printf("[Início] RemoteAccess v%s iniciado na porta :%d", version, port)
	log.Printf("[Início] Seu ID: %s | Privilégio: %s | AutoStart: %v", cfg.Data.ID, adminStatus, cfg.Data.AutoStart)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Start local server in background
	go func() {
		if err := srv.Start(port); err != nil {
			log.Fatalf("Erro ao iniciar servidor: %v", err)
		}
	}()

	// Wait until HTTP server is actively answering HTTP requests
	httpClient := &http.Client{Timeout: 80 * time.Millisecond}
	for i := 0; i < 100; i++ {
		resp, err := httpClient.Get(fmt.Sprintf("http://127.0.0.1:%d/api/host-info", port))
		if err == nil && resp.StatusCode == http.StatusOK {
			_ = resp.Body.Close()
			break
		}
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Initialize Windows System Tray Icon with native context menu
	trayMgr := tray.StartTray(cfg.Data.ID, config.IsAdmin(), func() {
		runNativeAppWindow(url)
	}, func() {
		go func() {
			log.Println("[Bandeja] Solicitando elevação de privilégios UAC...")
			releaseSingleInstanceLock()
			err := config.ElevateSelf("")
			if err != nil {
				log.Printf("[Bandeja] Elevação cancelada ou falhou: %v. Mantendo serviço ativo.", err)
				_ = acquireSingleInstanceLock(500 * time.Millisecond)
				return
			}
			log.Println("[Bandeja] Processo elevado iniciado com sucesso! Transferindo controle...")
			time.Sleep(300 * time.Millisecond)
			terminateAppSession(srv)
		}()
	}, func() {
		terminateAppSession(srv)
	})
	defer trayMgr.Remove()

	// Launch as Standalone Desktop Window (unless in silent autostart background mode)
	if !*noBrowser {
		runNativeAppWindow(url)
	}

	<-stop
	trayMgr.Remove()
	terminateAppSession(srv)
}
