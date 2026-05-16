package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"lenovo-scraper/internal/domain"
)

// fakeScraper simula o comportamento de um scraper real durante os testes.
// Ele permite controlar o resultado, erro, panic e também verificar se o contexto
// recebido possui deadline.
type fakeScraper struct {
	result      domain.ScrapeResult
	err         error
	panicValue  any
	hasDeadline bool
	deadline    time.Time
	calls       atomic.Int32
}

// ScrapeLenovo implementa o método esperado pelo Handler.
// Ele registra a chamada, captura o deadline do contexto e retorna o resultado
// ou erro configurado no fakeScraper.
func (f *fakeScraper) ScrapeLenovo(ctx context.Context) (domain.ScrapeResult, error) {
	f.calls.Add(1)
	f.deadline, f.hasDeadline = ctx.Deadline()

	if f.panicValue != nil {
		panic(f.panicValue)
	}

	return f.result, f.err
}

// Testa se o endpoint retorna corretamente o resultado do scraper.
func TestListLenovoNotebooksReturnsScrapeResult(t *testing.T) {
	scraper := &fakeScraper{
		result: domain.ScrapeResult{
			SourceURL:     "https://example.com/laptops",
			ScrapedAt:     "2026-05-16T12:34:56Z",
			Total:         1,
			PagesVisited:  1,
			ErrorsSkipped: 0,
			DurationMS:    10,
			Data: []domain.Product{
				{
					Name:        "Lenovo ThinkPad T14",
					Price:       899.99,
					Description: "Business notebook",
					Rating:      4,
					Reviews:     12,
					Link:        "https://example.com/product",
				},
			},
		},
	}

	recorder := performHandlerRequest(t, scraper)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	if got := recorder.Header().Get(cacheHeader); got != cacheMiss {
		t.Fatalf("expected X-Cache %q, got %q", cacheMiss, got)
	}

	if !scraper.hasDeadline {
		t.Fatal("expected scraper context to have a deadline")
	}

	remaining := time.Until(scraper.deadline)
	if remaining <= 0 || remaining > 30*time.Second {
		t.Fatalf("expected deadline up to 30s, got %s", remaining)
	}

	var got domain.ScrapeResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got.Total != 1 || got.Data[0].Name != "Lenovo ThinkPad T14" {
		t.Fatalf("unexpected scrape result: %+v", got)
	}
	if got.SourceURL != "https://example.com/laptops" || got.ScrapedAt != "2026-05-16T12:34:56Z" {
		t.Fatalf("unexpected trace fields: %+v", got)
	}
}

// Testa se a segunda chamada usa cache e não executa o scraper novamente.
func TestListLenovoNotebooksReturnsCacheHitWithoutScrapingAgain(t *testing.T) {
	scraper := &fakeScraper{
		result: domain.ScrapeResult{
			Total: 1,
			Data: []domain.Product{
				{Name: "Lenovo IdeaPad 3", Price: 499.50},
			},
		},
	}

	handler := NewHandler(scraper)

	first := performRequestToHandler(t, handler)
	if first.Code != http.StatusOK {
		t.Fatalf("expected first status 200, got %d", first.Code)
	}

	if got := first.Header().Get(cacheHeader); got != cacheMiss {
		t.Fatalf("expected first X-Cache %q, got %q", cacheMiss, got)
	}

	second := performRequestToHandler(t, handler)
	if second.Code != http.StatusOK {
		t.Fatalf("expected second status 200, got %d", second.Code)
	}

	if got := second.Header().Get(cacheHeader); got != cacheHit {
		t.Fatalf("expected second X-Cache %q, got %q", cacheHit, got)
	}

	if got := scraper.calls.Load(); got != 1 {
		t.Fatalf("expected scraper to be called once, got %d", got)
	}
}

// Testa se o cache expira e uma nova chamada ao scraper é feita.
func TestListLenovoNotebooksReturnsCacheMissAfterExpiration(t *testing.T) {
	scraper := &fakeScraper{
		result: domain.ScrapeResult{
			Total: 1,
			Data: []domain.Product{
				{Name: "Lenovo ThinkPad T14", Price: 899.99},
			},
		},
	}

	handler := NewHandler(scraper, WithCacheTTL(time.Millisecond))

	first := performRequestToHandler(t, handler)
	if first.Code != http.StatusOK {
		t.Fatalf("expected first status 200, got %d", first.Code)
	}

	if got := first.Header().Get(cacheHeader); got != cacheMiss {
		t.Fatalf("expected first X-Cache %q, got %q", cacheMiss, got)
	}

	deadline := time.Now().Add(100 * time.Millisecond)

	for time.Now().Before(deadline) {
		second := performRequestToHandler(t, handler)

		if second.Code != http.StatusOK {
			t.Fatalf("expected second status 200, got %d", second.Code)
		}

		if second.Header().Get(cacheHeader) == cacheMiss {
			if got := scraper.calls.Load(); got != 2 {
				t.Fatalf("expected scraper to be called twice, got %d", got)
			}
			return
		}

		time.Sleep(5 * time.Millisecond)
	}

	t.Fatal("expected cache to expire")
}

// Testa se erro de timeout do scraper retorna HTTP 504.
func TestListLenovoNotebooksReturnsGatewayTimeoutOnTimeout(t *testing.T) {
	recorder := performHandlerRequest(t, &fakeScraper{err: context.DeadlineExceeded})

	if recorder.Code != http.StatusGatewayTimeout {
		t.Fatalf("expected status 504, got %d", recorder.Code)
	}

	assertErrorResponse(t, recorder, "scrape timeout")
}

// Testa se erro genérico do scraper retorna HTTP 502.
func TestListLenovoNotebooksReturnsBadGatewayOnScrapingError(t *testing.T) {
	recorder := performHandlerRequest(t, &fakeScraper{err: errors.New("upstream network failed")})

	if recorder.Code != http.StatusBadGateway {
		t.Fatalf("expected status 502, got %d", recorder.Code)
	}

	assertErrorResponse(t, recorder, "failed to scrape Lenovo notebooks")
}

// Testa se o middleware de recuperação captura panic e retorna HTTP 500.
func TestRecoverMiddlewareReturnsInternalServerErrorOnPanic(t *testing.T) {
	recorder := performHandlerRequest(t, &fakeScraper{panicValue: "boom"})

	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", recorder.Code)
	}

	assertErrorResponse(t, recorder, "internal server error")
}

func TestHealthReturnsOKWithoutScraping(t *testing.T) {
	scraper := &fakeScraper{}
	handler := NewHandler(scraper)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", handler.Health)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	recorder := httptest.NewRecorder()

	RequestID(Recover(mux)).ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}

	var got domain.HealthResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if got.Status != "ok" {
		t.Fatalf("expected status ok, got %q", got.Status)
	}

	if calls := scraper.calls.Load(); calls != 0 {
		t.Fatalf("expected health not to call scraper, got %d calls", calls)
	}
}

// Cria um Handler com o scraper fake e executa uma requisição de teste.
func performHandlerRequest(t *testing.T, scraper *fakeScraper) *httptest.ResponseRecorder {
	t.Helper()

	handler := NewHandler(scraper)
	return performRequestToHandler(t, handler)
}

// Monta uma rota HTTP de teste, cria uma requisição GET e executa o handler
// passando pelos middlewares de RequestID e RecoverMiddleware.
func performRequestToHandler(t *testing.T, handler *Handler) *httptest.ResponseRecorder {
	t.Helper()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/v1/notebooks/lenovo", handler.ListLenovoNotebooks)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notebooks/lenovo", nil)
	recorder := httptest.NewRecorder()

	RequestID(Recover(mux)).ServeHTTP(recorder, req)

	return recorder
}

// Decodifica a resposta de erro e valida se a mensagem retornada é a esperada.
func assertErrorResponse(t *testing.T, recorder *httptest.ResponseRecorder, want string) {
	t.Helper()

	var got domain.ErrorResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &got); err != nil {
		t.Fatalf("failed to decode error response: %v", err)
	}

	if got.Error != want {
		t.Fatalf("expected error %q, got %q", want, got.Error)
	}
}
