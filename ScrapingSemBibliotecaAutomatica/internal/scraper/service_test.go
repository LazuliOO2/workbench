package scraper

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/PuerkitoBio/goquery"

	"lenovo-scraper/internal/client"
)

// Testa o fluxo completo do scraper com paginação.
//
// Este teste valida se:
// - duas páginas são visitadas;
// - produtos inválidos são ignorados;
// - apenas produtos Lenovo são retornados;
// - os links relativos viram links absolutos;
// - os produtos são ordenados pelo menor preço;
// - existe delay entre uma página e outra.
func TestListLenovoNotebooksScrapesPaginatedResults(t *testing.T) {
	page1 := readFixture(t, "page1.html")
	page2 := readFixture(t, "page2.html")

	var mu sync.Mutex
	var requestTimes []time.Time

	// Servidor HTTP falso usado para simular o site real.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requestTimes = append(requestTimes, time.Now())
		mu.Unlock()

		w.Header().Set("Content-Type", "text/html; charset=utf-8")

		// Retorna a segunda fixture quando a URL contém ?page=2.
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write(page2)
			return
		}

		// Caso contrário, retorna a primeira página.
		_, _ = w.Write(page1)
	}))
	defer server.Close()

	pageDelay := 50 * time.Millisecond

	// Cria o serviço configurado para usar o servidor de teste.
	service := NewService(
		WithBaseURL(server.URL+"/laptops"),
		WithPageDelay(pageDelay),
		WithHTTPClient(client.NewHTTPClient(time.Second, client.WithMaxRetries(0))),
	)

	result, err := service.ScrapeLenovo(context.Background())
	if err != nil {
		t.Fatalf("expected scrape success, got error: %v", err)
	}

	if result.PagesVisited != 2 {
		t.Fatalf("expected 2 pages visited, got %d", result.PagesVisited)
	}
	if result.ErrorsSkipped != 1 {
		t.Fatalf("expected 1 skipped error, got %d", result.ErrorsSkipped)
	}
	if result.Total != 2 {
		t.Fatalf("expected 2 valid Lenovo products, got %d", result.Total)
	}
	if result.SourceURL != server.URL+"/laptops" {
		t.Fatalf("expected source URL %q, got %q", server.URL+"/laptops", result.SourceURL)
	}
	if _, err := time.Parse(time.RFC3339, result.ScrapedAt); err != nil {
		t.Fatalf("expected scraped_at to be RFC3339, got %q: %v", result.ScrapedAt, err)
	}
	if len(result.Data) != 2 {
		t.Fatalf("expected 2 products in data, got %d", len(result.Data))
	}

	for _, product := range result.Data {
		if !strings.Contains(strings.ToLower(product.Name), "lenovo") {
			t.Fatalf("expected only Lenovo products, got %q", product.Name)
		}
		if strings.Contains(product.Name, "Broken") {
			t.Fatalf("expected broken Lenovo product to be skipped, got %q", product.Name)
		}
		if !strings.HasPrefix(product.Link, server.URL+"/product/") {
			t.Fatalf("expected absolute product link using test server URL, got %q", product.Link)
		}
	}

	// Confere se os produtos foram ordenados por preço crescente.
	if result.Data[0].Name != "Lenovo IdeaPad 3" || result.Data[0].Price != 499.50 {
		t.Fatalf("expected cheapest product first, got %+v", result.Data[0])
	}
	if result.Data[1].Name != "Lenovo ThinkPad T14" || result.Data[1].Price != 899.99 {
		t.Fatalf("expected most expensive product second, got %+v", result.Data[1])
	}

	mu.Lock()
	times := append([]time.Time(nil), requestTimes...)
	mu.Unlock()

	if len(times) != 2 {
		t.Fatalf("expected 2 HTTP requests, got %d", len(times))
	}

	// Verifica se o delay configurado foi respeitado entre as páginas.
	if elapsed := times[1].Sub(times[0]); elapsed < pageDelay {
		t.Fatalf("expected delay of at least %s between pages, got %s", pageDelay, elapsed)
	}
}

// Testa se produtos Lenovo inválidos são ignorados corretamente.
//
// Casos testados:
// - preço inválido;
// - quantidade de reviews vazia;
// - link inválido;
// - descrição vazia.
func TestParseProductsSkipsInvalidLenovoCards(t *testing.T) {
	tests := []struct {
		name string
		html string
	}{
		{
			name: "invalid price",
			html: productCardHTML("Lenovo Broken Price", "$broken", "Invalid price card.", "5 reviews", "/product/broken-price"),
		},
		{
			name: "empty review",
			html: productCardHTML("Lenovo Empty Review", "$699.00", "Missing review count.", "", "/product/empty-review"),
		},
		{
			name: "broken link",
			html: productCardHTML("Lenovo Broken Link", "$699.00", "Invalid product link.", "7 reviews", "%"),
		},
		{
			name: "lenovo without description",
			html: productCardHTML("Lenovo Without Description", "$699.00", "", "7 reviews", "/product/no-description"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := documentFromHTML(t, tt.html)

			products, skipped := parseProducts(doc, "https://example.com/laptops")

			if len(products) != 0 {
				t.Fatalf("expected invalid product to be skipped, got %+v", products)
			}
			if skipped != 1 {
				t.Fatalf("expected 1 skipped error, got %d", skipped)
			}
		})
	}
}

// Testa se a função de próxima página retorna string vazia
// quando o botão "Next" não existe no HTML.
func TestNextPageURLReturnsEmptyWhenNextButtonIsMissing(t *testing.T) {
	doc := documentFromHTML(t, `<html><body><div id="static-pagination"></div></body></html>`)

	next, err := nextPageURL(doc, "https://example.com/laptops")
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if next != "" {
		t.Fatalf("expected empty next page URL, got %q", next)
	}
}

// Testa se o scraper detecta loop de paginação.
//
// O HTML aponta a próxima página para a mesma URL atual.
// Isso deve gerar erro para evitar um loop infinito.
func TestScrapeLenovoReturnsErrorOnPaginationLoop(t *testing.T) {
	page := []byte(`<!doctype html>
<html>
<body>
	<div id="static-pagination">
		<a class="page-link next" href="/laptops">Next</a>
	</div>
</body>
</html>`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}))
	defer server.Close()

	service := NewService(
		WithBaseURL(server.URL+"/laptops"),
		WithPageDelay(0),
		WithHTTPClient(client.NewHTTPClient(time.Second, client.WithMaxRetries(0))),
	)

	result, err := service.ScrapeLenovo(context.Background())
	if err == nil {
		t.Fatal("expected pagination loop error")
	}
	if !strings.Contains(err.Error(), "pagination loop detected") {
		t.Fatalf("expected pagination loop error, got %v", err)
	}
	if result.PagesVisited != 1 {
		t.Fatalf("expected 1 visited page before loop detection, got %d", result.PagesVisited)
	}
}

// Testa se o scraper retorna erro quando o limite máximo
// de páginas configurado é ultrapassado.
func TestScrapeLenovoReturnsErrorWhenMaxPagesIsExceeded(t *testing.T) {
	page := []byte(`<!doctype html>
<html>
<body>
	<div id="static-pagination">
		<a class="page-link next" href="/laptops?page=2">Next</a>
	</div>
</body>
</html>`)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(page)
	}))
	defer server.Close()

	service := NewService(
		WithBaseURL(server.URL+"/laptops"),
		WithPageDelay(0),
		WithMaxPages(1),
		WithHTTPClient(client.NewHTTPClient(time.Second, client.WithMaxRetries(0))),
	)

	result, err := service.ScrapeLenovo(context.Background())
	if err == nil {
		t.Fatal("expected max pages error")
	}
	if !strings.Contains(err.Error(), "pagination limit exceeded") {
		t.Fatalf("expected pagination limit error, got %v", err)
	}
	if result.PagesVisited != 1 {
		t.Fatalf("expected 1 visited page before limit error, got %d", result.PagesVisited)
	}
}

// Testa se a identificação de produtos Lenovo usa os termos centralizados.
//
// A função deve reconhecer nomes da linha Lenovo mesmo quando
// a palavra "Lenovo" não aparece diretamente no nome do produto.
func TestIsLenovoProductUsesCentralizedTerms(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: "Lenovo ThinkPad T14", want: true},
		{name: "IdeaPad Slim 5", want: true},
		{name: "Legion 5 Pro", want: true},
		{name: "Yoga Book 9i", want: true},
		{name: "ThinkBook 14", want: true},
		{name: "LOQ 15IRH8", want: true},
		{name: "Acer Aspire", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isLenovoProduct(tt.name); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}

// Cria um documento goquery a partir de uma string HTML.
//
// Essa função auxiliar evita repetição nos testes.
func documentFromHTML(t *testing.T, html string) *goquery.Document {
	t.Helper()

	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatalf("failed to create HTML document: %v", err)
	}

	return doc
}

// Gera um HTML mínimo representando um card de produto.
//
// Usado nos testes para montar rapidamente cenários válidos
// e inválidos sem depender de arquivos externos.
func productCardHTML(name, price, description, reviews, href string) string {
	return fmt.Sprintf(`<!doctype html>
<html>
<body>
	<div class="card thumbnail">
		<div class="caption">
			<h4 class="price"><span>%s</span></h4>
			<h4><a href="%s" class="title">%s</a></h4>
			<p class="description">%s</p>
		</div>
		<div class="ratings">
			<p class="review-count">%s</p>
			<p><span class="ws-icon ws-icon-star"></span></p>
		</div>
	</div>
</body>
</html>`, price, href, name, description, reviews)
}

// Lê uma fixture HTML da pasta testdata.
//
// O caminho esperado é:
// ../../testdata/<nome-da-fixture>
func readFixture(t *testing.T, name string) []byte {
	t.Helper()

	path := filepath.Join("..", "..", "testdata", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}

	return data
}
