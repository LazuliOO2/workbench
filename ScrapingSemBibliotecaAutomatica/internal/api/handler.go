package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"lenovo-scraper/internal/domain"
)

const (
	// Tempo máximo permitido para executar o scraping.
	defaultScrapeTimeout = 30 * time.Second

	// Tempo padrão que o resultado ficará salvo em cache.
	defaultCacheTTL = 5 * time.Minute

	// Cabeçalho usado para indicar se a resposta veio do cache.
	cacheHeader = "X-Cache"
	cacheHit    = "HIT"
	cacheMiss   = "MISS"

	envScrapeTimeout = "SCRAPE_TIMEOUT"
)

// LenovoScraper define o contrato necessário para executar o scraping
// dos notebooks Lenovo.
type LenovoScraper interface {
	ScrapeLenovo(ctx context.Context) (domain.ScrapeResult, error)
}

// Handler concentra as dependências e configurações usadas pelos endpoints.
type Handler struct {
	scraper       LenovoScraper
	cacheTTL      time.Duration
	scrapeTimeout time.Duration
	cache         lenovoCache
	group         singleflight.Group
	logger        *slog.Logger
}

// HandlerOption permite configurar o Handler usando o padrão de options.
type HandlerOption func(*Handler)

// WithCacheTTL configura o tempo de vida do cache.
// Valores negativos são ignorados.
func WithCacheTTL(ttl time.Duration) HandlerOption {
	return func(h *Handler) {
		if ttl >= 0 {
			h.cacheTTL = ttl
		}
	}
}

// WithScrapeTimeout configura o deadline maximo do scraping por requisicao.
// Valores menores ou iguais a zero sao ignorados.
func WithScrapeTimeout(timeout time.Duration) HandlerOption {
	return func(h *Handler) {
		if timeout > 0 {
			h.scrapeTimeout = timeout
		}
	}
}

// WithLogger configura o logger usado pelo Handler.
// Valores nil são ignorados.
func WithLogger(logger *slog.Logger) HandlerOption {
	return func(h *Handler) {
		if logger != nil {
			h.logger = logger
		}
	}
}

// lenovoCache armazena o último resultado do scraping em memória.
type lenovoCache struct {
	mu        sync.RWMutex
	result    domain.ScrapeResult
	expiresAt time.Time
	valid     bool
}

// NewHandler cria uma nova instância do Handler.
func NewHandler(scraper LenovoScraper, opts ...HandlerOption) *Handler {
	h := &Handler{
		scraper:       scraper,
		cacheTTL:      defaultCacheTTL,
		scrapeTimeout: durationFromEnv(envScrapeTimeout, defaultScrapeTimeout),
		logger:        slog.Default(),
	}

	for _, opt := range opts {
		opt(h)
	}

	return h
}

// ListLenovoNotebooks responde com a lista de notebooks Lenovo.
//
// Fluxo:
//  1. Verifica se existe resultado válido em cache.
//  2. Caso exista, responde imediatamente com X-Cache: HIT.
//  3. Caso contrário, executa o scraping com singleflight.
//  4. O singleflight evita múltiplos scrapings simultâneos para a mesma chave.
//  5. Ao final, salva o resultado no cache e retorna a resposta JSON.
func (h *Handler) ListLenovoNotebooks(w http.ResponseWriter, r *http.Request) {
	now := time.Now()

	if result, ok := h.cachedResult(now); ok {
		w.Header().Set(cacheHeader, cacheHit)
		writeJSON(w, http.StatusOK, result)
		return
	}

	w.Header().Set(cacheHeader, cacheMiss)

	value, err, _ := h.group.Do("lenovo-notebooks", func() (any, error) {
		if result, ok := h.cachedResult(time.Now()); ok {
			return result, nil
		}

		ctx, cancel := context.WithTimeout(r.Context(), h.scrapeTimeout)
		defer cancel()

		result, err := h.scraper.ScrapeLenovo(ctx)
		if err != nil {
			return domain.ScrapeResult{}, err
		}

		h.storeCacheResult(result, time.Now())
		return result, nil
	})

	if err != nil {
		h.writeScrapeError(w, err)
		return
	}

	result, ok := value.(domain.ScrapeResult)
	if !ok {
		h.logger.Error("invalid singleflight result type")
		writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
			Error: "internal server error",
		})
		return
	}

	writeJSON(w, http.StatusOK, result)
}

// Health reports process liveness without touching the external scraped site.
func (h *Handler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, domain.HealthResponse{
		Status: "ok",
	})
}

// cachedResult retorna o resultado salvo em cache caso ele ainda seja válido.
func (h *Handler) cachedResult(now time.Time) (domain.ScrapeResult, bool) {
	h.cache.mu.RLock()
	defer h.cache.mu.RUnlock()

	if !h.cache.valid || !now.Before(h.cache.expiresAt) {
		return domain.ScrapeResult{}, false
	}

	return h.cache.result, true
}

// storeCacheResult salva o resultado do scraping no cache.
//
// Caso o TTL seja zero, o cache fica desativado.
func (h *Handler) storeCacheResult(result domain.ScrapeResult, now time.Time) {
	if h.cacheTTL == 0 {
		return
	}

	h.cache.mu.Lock()
	defer h.cache.mu.Unlock()

	h.cache.result = result
	h.cache.expiresAt = now.Add(h.cacheTTL)
	h.cache.valid = true
}

// writeScrapeError converte erros do scraping em respostas HTTP adequadas.
func (h *Handler) writeScrapeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		h.logger.Warn("scrape timeout", "error", err)
		writeJSON(w, http.StatusGatewayTimeout, domain.ErrorResponse{
			Error: "scrape timeout",
		})

	case errors.Is(err, context.Canceled):
		h.logger.Warn("request canceled", "error", err)
		writeJSON(w, http.StatusRequestTimeout, domain.ErrorResponse{
			Error: "request canceled",
		})

	default:
		h.logger.Error("failed to scrape Lenovo notebooks", "error", err)
		writeJSON(w, http.StatusBadGateway, domain.ErrorResponse{
			Error: "failed to scrape Lenovo notebooks",
		})
	}
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

// writeJSON escreve uma resposta HTTP no formato JSON.
func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		slog.Error("failed to write JSON response", "error", err)
	}
}
