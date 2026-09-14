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
	"syscall"
	"time"
	"unsafe"

	"remoteaccess/internal/config"
	"remoteaccess/internal/logger"
	"remoteaccess/internal/server"
	"remoteaccess/internal/tray"
)

const version = "1.2.0"

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32.NewProc("CreateMutexW")
	procCloseHandle = kernel32.NewProc("CloseHandle")
	appMutex        uintptr
)

func acquireSingleInstanceLock(maxWait time.Duration) bool {
	if runtime.GOOS != "windows" {
		return true
	}
	name, _ := syscall.UTF16PtrFromString("Local\\RemoteAccess_SingleInstance_Mutex")
	deadline := time.Now().Add(maxWait)
	for {
		hMutex, _, _ := procCreateMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
		if hMutex != 0 && syscall.GetLastError() != syscall.ERROR_ALREADY_EXISTS {
			appMutex = hMutex
			return true
		}
		if time.Now().After(deadline) {
			if hMutex != 0 {
				procCloseHandle.Call(hMutex)
			}
			return false
		}
		time.Sleep(150 * time.Millisecond)
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

// startWindowSizeGuard enforces a rigid native minimum window size (540x580)
// on the application window so the user physically cannot shrink it below the threshold.
func startWindowSizeGuard() {
	if runtime.GOOS != "windows" {
		return
	}
	user32 := syscall.NewLazyDLL("user32.dll")
	procGetWindowRect := user32.NewProc("GetWindowRect")
	procSetWindowPos := user32.NewProc("SetWindowPos")
	procGetWindowTextW := user32.NewProc("GetWindowTextW")
	procGetClassNameW := user32.NewProc("GetClassNameW")
	procEnumWindows := user32.NewProc("EnumWindows")
	procIsWindowVisible := user32.NewProc("IsWindowVisible")

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
		// Allocate callback ONCE outside the loop to avoid exhausting Go runtime callback table
		findCb := syscall.NewCallback(func(hwnd uintptr, lParam uintptr) uintptr {
			if !isVis(hwnd) {
				return 1
			}
			var clsBuf [256]uint16
			procGetClassNameW.Call(hwnd, uintptr(unsafe.Pointer(&clsBuf[0])), 256)
			cls := syscall.UTF16ToString(clsBuf[:])
			if cls != "Chrome_WidgetWin_1" {
				return 1
			}
			var buf [256]uint16
			procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), 256)
			title := syscall.UTF16ToString(buf[:])
			if strings.Contains(title, "RemoteAccess") || strings.Contains(title, "127.0.0.1") {
				targetHWND = hwnd
				return 0 // found, stop enumerating
			}
			return 1
		})

		for range ticker.C {
			if targetHWND == 0 || !isVis(targetHWND) {
				targetHWND = 0
				procEnumWindows.Call(findCb, 0)
			}

			if targetHWND != 0 {
				var r rect
				ret, _, _ := procGetWindowRect.Call(targetHWND, uintptr(unsafe.Pointer(&r)))
				if ret != 0 {
					w := r.right - r.left
					h := r.bottom - r.top
					needFix := false
					newW := w
					newH := h
					if w < 540 {
						newW = 540
						needFix = true
					}
					if h < 580 {
						newH = 580
						needFix = true
					}
					if needFix {
						// SWP_NOMOVE (0x0002) | SWP_NOZORDER (0x0004) | SWP_NOACTIVATE (0x0010)
						procSetWindowPos.Call(targetHWND, 0, 0, 0, uintptr(newW), uintptr(newH), 0x0002|0x0004|0x0010)
					}
				}
			}
		}
	}()
}

// runNativeAppWindow opens the standalone desktop app window (Edge App mode or browser fallback)
func runNativeAppWindow(url string) {
	if runtime.GOOS == "windows" {
		edgePaths := []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}

		profileDir := filepath.Join(os.TempDir(), "RemoteAccess_WebProfile")
		_ = os.MkdirAll(profileDir, 0755)

		for _, edgePath := range edgePaths {
			if _, err := os.Stat(edgePath); err == nil {
				cmd := exec.Command(edgePath,
					fmt.Sprintf("--app=%s", url),
					fmt.Sprintf("--user-data-dir=%s", profileDir),
					"--window-size=960,720",
					"--app-id=RemoteAccessPortable",
					"--no-first-run",
					"--no-default-browser-check",
				)
				if err := cmd.Start(); err == nil {
					log.Printf("[Janela] Janela desktop nativa iniciada via Edge (%s)", edgePath)
					startWindowSizeGuard()
					return
				}
			}
		}

		// Fallback: standard browser
		log.Println("[Janela] Edge não encontrado diretamente, abrindo navegador padrão...")
		_ = exec.Command("cmd", "/c", "start", url).Start()
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
	elevateFlag := flag.Bool("elevate", false, "Reiniciar como Administrador")
	defaultPwd := flag.String("password", "", "Definir senha padrao")
	flag.Parse()

	// 1. If running as installer
	if *installFlag {
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

	// 3. Acquire single-instance lock with retry for smooth transitions during elevation
	maxWait := 300 * time.Millisecond
	if *elevateFlag || config.IsAdmin() {
		maxWait = 3 * time.Second
	}

	if !acquireSingleInstanceLock(maxWait) {
		log.Println("[Aviso] Outra instância do RemoteAccess já está em execução.")
		if !*noBrowser {
			runningPort := findRunningLocalPort()
			runNativeAppWindow(fmt.Sprintf("http://127.0.0.1:%d", runningPort))
		}
		os.Exit(0)
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
		srv.Shutdown()
		_ = config.ElevateSelf("")
		os.Exit(0)
	}, func() {
		srv.Shutdown()
		time.Sleep(100 * time.Millisecond)
		os.Exit(0)
	})
	defer trayMgr.Remove()

	// Launch as Standalone Desktop Window (unless in silent autostart background mode)
	if !*noBrowser {
		runNativeAppWindow(url)
	}

	<-stop
	log.Println("[RemoteAccess] Notificando nuvem e encerrando com segurança...")
	srv.Shutdown()
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)
}
