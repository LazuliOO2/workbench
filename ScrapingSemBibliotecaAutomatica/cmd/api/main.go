package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"lenovo-scraper/internal/api"
	"lenovo-scraper/internal/scraper"
)

// se não tiver variaveis de ambiente use a porta 8080
func main() {
	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}
	// cria um novo service
	scraperService := scraper.NewService()
	scrapeTimeout := durationFromEnv("SCRAPE_TIMEOUT", 30*time.Second)
	// cria a camada HTT
	handler := api.NewHandler(scraperService, api.WithScrapeTimeout(scrapeTimeout))
	//ServeMux é o roteador padrão do Go.Ele decide qual função responde cada rota.
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)
	// Ela registra:método: GET rota: /api/v1/notebooks/lenovo Quando alguém acessar:GET /api/v1/notebooks/lenovoo Go executa:handler.ListLenovoNotebooks
	mux.HandleFunc("GET /api/v1/notebooks/lenovo", handler.ListLenovoNotebooks)
	// Cria servidor HTTP customizado.
	server := &http.Server{
		Addr: addr, //Porta do servidor.
		//Provavelmente captura panic.Sem recover:panic -> servidor pode quebrar Com recover:panic -> responde 500 -> servidor continua vivo
		Handler:           api.RequestID(api.Recover(mux)), //Aqui entram os middlewares.Request>RequestID>Recover>mux
		ReadHeaderTimeout: 5 * time.Second,                 // Tempo máximo para ler headers.
		ReadTimeout:       15 * time.Second,                //Tempo máximo para ler request inteira.
		WriteTimeout:      scrapeTimeout + 5*time.Second,   //Tempo máximo para enviar resposta.
		IdleTimeout:       60 * time.Second,                //Quanto tempo conexão keep-alive fica aberta sem uso.
	}
	//Aqui o servidor roda em paralelo.Sem goroutine:server.ListenAndServe()bloquearia o programa.Com goroutine:servidor roda main continua executando
	go func() {
		log.Printf("server listening on %s", addr)
		//Quando faz shutdown gracioso:server.Shutdown()o ListenAndServe retorna:http.ErrServerClosedIsso NÃO é erro real.Então ele ignora esse caso.
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server failed: %v", err)
		}
	}()
	//Ele cria contexto que cancela quando o processo recebe:CTRL+C SIGTERM
	quit, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	//Libera recursos do signal handler.
	defer stop()
	//Programa fica parado aqui esperando sinal.
	<-quit.Done()
	//Cria timeout de shutdown.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	//Libera recursos do contexto.
	defer cancel()

	log.Println("shutting down server...")
	//para aceitar novas conexões espera requests atuais terminarem fecha servidor elegantemente Sem isso:processo morre instantaneamente e requests podem quebrar no meio.
	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server forced to shutdown: %v", err)
	}

	log.Println("server stopped gracefully")
}

func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration <= 0 {
		return fallback
	}

	return duration
}
