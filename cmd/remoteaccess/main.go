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
)

const version = "1.2.0"

var (
	kernel32        = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutex = kernel32.NewProc("CreateMutexW")
)

func acquireSingleInstanceLock() uintptr {
	if runtime.GOOS != "windows" {
		return 0
	}
	name, _ := syscall.UTF16PtrFromString("Global\\RemoteAccess_SingleInstance_Mutex")
	hMutex, _, _ := procCreateMutex.Call(0, 1, uintptr(unsafe.Pointer(name)))
	if hMutex != 0 && syscall.GetLastError() == syscall.ERROR_ALREADY_EXISTS {
		log.Println("[Aviso] Outra instância do RemoteAccess já está em execução.")
		os.Exit(0)
	}
	return hMutex
}

// runNativeAppWindow opens the standalone desktop app window and invokes onWindowClose when closed
func runNativeAppWindow(url string, onWindowClose func()) {
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
					_ = cmd.Wait()
					if onWindowClose != nil {
						onWindowClose()
					}
					return
				}
			}
		}

		// Fallback: standard browser
		_ = exec.Command("cmd", "/c", "start", url).Start()
		return
	}

	if runtime.GOOS == "darwin" {
		cmd := exec.Command("open", "-W", url)
		_ = cmd.Run()
		if onWindowClose != nil {
			onWindowClose()
		}
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
	_ = acquireSingleInstanceLock()

	cfg := config.LoadConfig()

	// Initialize in-memory (and optional file) logging
	logger.InitLogger(cfg.Data.SaveLogFile)

	portFlag := flag.Int("port", 8080, "Porta local para o painel de controle")
	noBrowser := flag.Bool("no-browser", false, "Modo silencioso de segundo plano (para inicializacao com Windows)")
	autostart := flag.Bool("autostart", false, "Ativar inicialização automática com o Windows")
	defaultPwd := flag.String("password", "", "Definir senha padrao")
	flag.Parse()

	if *defaultPwd != "" {
		_ = cfg.SetPassword(*defaultPwd)
	}

	if *autostart {
		_ = cfg.SetAutoStart(true)
	}

	port := findAvailablePort(*portFlag)
	srv := server.NewLocalServer(cfg)

	url := fmt.Sprintf("http://localhost:%d", port)

	log.Printf("[Início] RemoteAccess v%s iniciado na porta :%d", version, port)
	log.Printf("[Início] Seu ID: %s | AutoStart: %v", cfg.Data.ID, cfg.Data.AutoStart)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	// Launch as Standalone Desktop Window (unless in silent autostart background mode)
	if !*noBrowser {
		go func() {
			time.Sleep(300 * time.Millisecond)
			runNativeAppWindow(url, func() {
				log.Println("[Janela] Janela desktop fechada pelo usuário. Encerrando processo...")
				stop <- syscall.SIGTERM
			})
		}()
	}

	go func() {
		if err := srv.Start(port); err != nil {
			log.Fatalf("Erro ao iniciar servidor: %v", err)
		}
	}()

	<-stop
	log.Println("[RemoteAccess] Notificando nuvem e encerrando com segurança...")
	srv.Shutdown()
	time.Sleep(200 * time.Millisecond)
	os.Exit(0)
}
