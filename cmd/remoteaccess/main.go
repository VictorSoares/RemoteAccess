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
	"remoteaccess/internal/server"
)

const version = "1.1.0"

func openBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
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
	noBrowser := flag.Bool("no-browser", false, "Não abrir o navegador automaticamente (Modo background)")
	autostart := flag.Bool("autostart", false, "Ativar inicialização automática com o Windows")
	flag.Parse()

	cfg := config.LoadConfig()

	if *autostart {
		_ = cfg.SetAutoStart(true)
	}

	port := findAvailablePort(*portFlag)
	srv := server.NewLocalServer(cfg)

	url := fmt.Sprintf("http://localhost:%d", port)

	fmt.Println("================================================================")
	fmt.Printf("   ⚡ RemoteAccess Portable v%s - Acesso Remoto Bidirecional\n", version)
	fmt.Println("   Sem Instalacao | ID e Senha Fixos | Inicializacao com Windows")
	fmt.Println("================================================================")
	fmt.Printf("   [+] Seu ID Fixo       : %s\n", cfg.Data.ID)
	fmt.Printf("   [+] Sua Senha Fixa    : %s\n", cfg.Data.Password)
	fmt.Printf("   [+] Auto-start Windows: %v\n", cfg.Data.AutoStart)
	fmt.Printf("   [+] Painel de Controle: %s\n", url)
	fmt.Println("================================================================")
	fmt.Println("   Pressione Ctrl+C para encerrar o programa a qualquer momento.")
	fmt.Println()

	// Launch browser after server starts (unless in silent / background mode)
	if !*noBrowser {
		go func() {
			time.Sleep(600 * time.Millisecond)
			openBrowser(url)
		}()
	}

	// Handle graceful shutdown
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		if err := srv.Start(port); err != nil {
			log.Fatalf("Erro ao iniciar servidor: %v", err)
		}
	}()

	<-stop
	fmt.Println("\n[RemoteAccess] Encerrando com seguranca...")
}
