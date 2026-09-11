package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"remoteaccess/internal/config"
	"remoteaccess/internal/logger"
	"remoteaccess/internal/server"
)

const version = "1.2.0"

// openNativeAppWindow opens the application as a standalone desktop window (like AnyDesk) without browser UI
func openNativeAppWindow(url string) {
	if runtime.GOOS == "windows" {
		edgePaths := []string{
			os.Getenv("ProgramFiles(x86)") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("ProgramFiles") + `\Microsoft\Edge\Application\msedge.exe`,
			os.Getenv("LocalAppData") + `\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files (x86)\Microsoft\Edge\Application\msedge.exe`,
			`C:\Program Files\Microsoft\Edge\Application\msedge.exe`,
		}

		for _, edgePath := range edgePaths {
			if _, err := os.Stat(edgePath); err == nil {
				cmd := exec.Command(edgePath, fmt.Sprintf("--app=%s", url), "--window-size=960,720", "--app-id=RemoteAccessPortable")
				if err := cmd.Start(); err == nil {
					return
				}
			}
		}

		// Fallback: standard default browser
		_ = exec.Command("cmd", "/c", "start", url).Start()
		return
	}

	if runtime.GOOS == "darwin" {
		_ = exec.Command("open", url).Start()
	} else {
		_ = exec.Command("xdg-open", url).Start()
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
	// Initialize in-memory and file logging
	logger.InitLogger()

	portFlag := flag.Int("port", 8080, "Porta local para o painel de controle")
	noBrowser := flag.Bool("no-browser", false, "Modo silencioso de segundo plano (para inicializacao com Windows)")
	autostart := flag.Bool("autostart", false, "Ativar inicialização automática com o Windows")
	defaultPwd := flag.String("password", "", "Definir senha padrao")
	flag.Parse()

	cfg := config.LoadConfig()

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

	// Launch as Standalone Desktop Window (unless in silent autostart background mode)
	if !*noBrowser {
		go func() {
			time.Sleep(500 * time.Millisecond)
			openNativeAppWindow(url)
		}()
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(port); err != nil {
			log.Fatalf("Erro ao iniciar servidor: %v", err)
		}
	}()

	<-stop
	log.Println("[RemoteAccess] Encerrando com segurança...")
}
