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
	http.HandleFunc("/ws", srv.HandleWS)

	// Health check endpoint for Render/Cloud monitoring
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, "<h2>⚡ RemoteAccess Signaling Server</h2><p>Status: <strong>Online</strong></p><p>WebSocket: <code>/ws</code></p>")
	})

	fmt.Printf("[Signaling Server] Iniciado na porta :%d\n", *port)
	fmt.Printf("[Signaling Server] Endpoint WebSocket: /ws\n")

	if err := http.ListenAndServe(fmt.Sprintf(":%d", *port), nil); err != nil {
		log.Fatalf("Erro no servidor de sinalizacao: %v", err)
	}
}
