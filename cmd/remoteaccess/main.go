package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
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
					go func() {
						_ = cmd.Wait()
						log.Println("[Janela] Janela desktop fechada pelo usuário. Encerrando processo...")
						time.Sleep(100 * time.Millisecond)
						os.Exit(0)
					}()
					return
				}
			}
		}

		// Fallback: standard browser
		log.Println("[Janela] Edge não encontrado diretamente, abrindo navegador padrão...")
		_ = exec.Command("cmd", "/c", "start", url).Start()
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

	// Wait until HTTP server is actively accepting connections
	for i := 0; i < 50; i++ {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 50*time.Millisecond)
		if err == nil {
			conn.Close()
			break
		}
		time.Sleep(30 * time.Millisecond)
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
		go func() {
			time.Sleep(120 * time.Millisecond)
			runNativeAppWindow(url)
		}()
	}

	<-stop
	log.Println("[RemoteAccess] Notificando nuvem e encerrando com segurança...")
	srv.Shutdown()
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)
}
