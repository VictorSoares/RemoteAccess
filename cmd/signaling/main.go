package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"

	"remoteaccess/internal/signaling"
)

func main() {
	defaultPort := 8080
	if portEnv := os.Getenv("PORT"); portEnv != "" {
		if p, err := strconv.Atoi(portEnv); err == nil {
			defaultPort = p
		}
	}

	port := flag.Int("port", defaultPort, "Porta do servidor de sinalizacao")
	flag.Parse()

	srv := signaling.NewServer()
	
	// WebSocket relay and signaling endpoint
	http.HandleFunc("/ws", srv.HandleWS)

	// API endpoints for cloud cluster management
	http.HandleFunc("/api/auth", srv.HandleAuth)
	http.HandleFunc("/api/stats", srv.HandleStats)
	http.HandleFunc("/api/history", srv.HandleHistory)
	http.HandleFunc("/api/block", srv.HandleBlockPeer)
	http.HandleFunc("/api/logs", srv.HandleLogs)
	http.HandleFunc("/api/kick", srv.HandleKick)
	http.HandleFunc("/api/wol", srv.HandleWoL)

	// Health check endpoint for Render/Cloud monitoring
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Render modern Web Dashboard on root path
	http.HandleFunc("/", srv.HandleDashboard)

	fmt.Printf("[Signaling Server] Iniciado na porta :%d\n", *port)
	fmt.Printf("[Signaling Server] Dashboard: http://localhost:%d\n", *port)
	fmt.Printf("[Signaling Server] Endpoint WebSocket: /ws\n")

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		log.Fatalf("Erro no servidor de sinalizacao: %v", err)
	}
}

