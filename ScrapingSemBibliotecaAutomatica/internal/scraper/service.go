package scraper

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"

	"lenovo-scraper/internal/client"
	"lenovo-scraper/internal/domain"
)

const (
	// URL padrão do catálogo de notebooks usado pelo scraper.
	defaultLaptopsURL = "https://webscraper.io/test-sites/e-commerce/static/computers/laptops"

	// Timeout padrão para requisições HTTP.
	defaultTimeout = 15 * time.Second

	// Intervalo padrão entre uma página e outra.
	defaultPageDelay = 500 * time.Millisecond

	// Limite padrão de páginas para evitar loops infinitos na paginação.
	defaultMaxPages = 60

	// Variáveis de ambiente usadas para customizar o comportamento do scraper.
	envHTTPTimeout    = "SCRAPER_HTTP_TIMEOUT"
	envPageDelay      = "SCRAPER_PAGE_DELAY"
	envMaxPages       = "SCRAPER_MAX_PAGES"
	envTLSFingerprint = "SCRAPER_TLS_FINGERPRINT"
)

// Expressão regular usada para extrair números inteiros de textos,
// como "14 reviews".
var integerPattern = regexp.MustCompile(`\d+`)

// Termos centralizados para identificar produtos Lenovo.
// Isso facilita manutenção caso novas linhas de produtos apareçam no catálogo.
var lenovoProductTerms = []string{
	"lenovo",
	"thinkpad",
	"ideapad",
	"thinkbook",
	"legion",
	"yoga",
	"loq",
}

// HTTPClient define a interface mínima necessária para executar requisições HTTP.
// Isso permite injetar clientes falsos em testes.
type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

// Service representa o serviço principal de scraping.
type Service struct {
	httpClient HTTPClient
	baseURL    string
	pageDelay  time.Duration
	maxPages   int
}

// Option representa uma função de configuração do Service.
type Option func(*Service)

// WithHTTPClient permite substituir o cliente HTTP padrão.
func WithHTTPClient(httpClient HTTPClient) Option {
	return func(s *Service) {
		if httpClient != nil {
			s.httpClient = httpClient
		}
	}
}

// WithBaseURL permite alterar a URL inicial do scraper.
func WithBaseURL(baseURL string) Option {
	return func(s *Service) {
		if baseURL != "" {
			s.baseURL = baseURL
		}
	}
}

// WithPageDelay permite configurar o intervalo entre páginas.
func WithPageDelay(delay time.Duration) Option {
	return func(s *Service) {
		if delay >= 0 {
			s.pageDelay = delay
		}
	}
}

// WithMaxPages permite configurar o limite máximo de páginas visitadas.
func WithMaxPages(maxPages int) Option {
	return func(s *Service) {
		if maxPages >= 0 {
			s.maxPages = maxPages
		}
	}
}

// NewService cria uma nova instância do scraper com valores padrão,
// podendo ser customizada com opções funcionais.
func NewService(opts ...Option) *Service {
	service := &Service{
		httpClient: client.NewHTTPClient(
			durationFromEnv(envHTTPTimeout, defaultTimeout),
			client.WithTLSFingerprint(os.Getenv(envTLSFingerprint)),
		),
		baseURL:   defaultLaptopsURL,
		pageDelay: durationFromEnv(envPageDelay, defaultPageDelay),
		maxPages:  intFromEnv(envMaxPages, defaultMaxPages),
	}

	for _, opt := range opts {
		opt(service)
	}

	return service
}

// ListLenovoNotebooks é um alias público para ScrapeLenovo.
// Mantém uma API mais semântica para quem quer apenas listar notebooks Lenovo.
func (s *Service) ListLenovoNotebooks(ctx context.Context) (domain.ScrapeResult, error) {
	return s.ScrapeLenovo(ctx)
}

// ScrapeLenovo percorre as páginas do catálogo, coleta produtos Lenovo,
// ordena os resultados por preço e retorna um resumo da execução.
func (s *Service) ScrapeLenovo(ctx context.Context) (domain.ScrapeResult, error) {
	start := time.Now()

	var products []domain.Product
	pagesVisited := 0
	errorsSkipped := 0
	nextURL := s.baseURL
	visited := make(map[string]struct{})

	for nextURL != "" {
		if err := ctx.Err(); err != nil {
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), scrapeContextError(pagesVisited, err)
		}

		if _, ok := visited[nextURL]; ok {
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), fmt.Errorf("pagination loop detected at %s", nextURL)
		}

		if s.maxPages > 0 && pagesVisited >= s.maxPages {
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), fmt.Errorf("pagination limit exceeded after %d page(s); max pages is %d", pagesVisited, s.maxPages)
		}

		visited[nextURL] = struct{}{}

		doc, err := s.fetchDocument(ctx, nextURL)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), scrapeContextError(pagesVisited, ctxErr)
			}
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), err
		}

		pagesVisited++

		pageProducts, pageErrors := parseProducts(doc, nextURL)
		products = append(products, pageProducts...)
		errorsSkipped += pageErrors

		next, err := nextPageURL(doc, nextURL)
		if err != nil {
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), err
		}

		nextURL = next
		if nextURL == "" {
			break
		}

		if err := waitForNextPage(ctx, s.pageDelay); err != nil {
			return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), scrapeContextError(pagesVisited, err)
		}
	}

	sort.Slice(products, func(i, j int) bool {
		return products[i].Price < products[j].Price
	})

	return scrapeResult(products, pagesVisited, errorsSkipped, start, s.baseURL), nil
}

// fetchDocument executa a requisição HTTP de uma página e transforma o HTML
// em um documento goquery para consulta por seletores CSS.
func (s *Service) fetchDocument(ctx context.Context, pageURL string) (*goquery.Document, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pageURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch page %s: %w", pageURL, err)
	}

	if resp.Body == nil {
		return nil, fmt.Errorf("fetch page %s: empty response body", pageURL)
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		drainAndClose(resp.Body)
		return nil, fmt.Errorf("fetch page %s: unexpected status code %d", pageURL, resp.StatusCode)
	}

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	closeErr := resp.Body.Close()

	if err != nil {
		return nil, fmt.Errorf("parse page %s: %w", pageURL, err)
	}

	if closeErr != nil {
		return nil, fmt.Errorf("close response body for %s: %w", pageURL, closeErr)
	}

	return doc, nil
}

// parseProducts percorre todos os cards da página e retorna apenas produtos Lenovo.
// Cards incompletos ou inválidos são ignorados e contabilizados.
func parseProducts(doc *goquery.Document, pageURL string) ([]domain.Product, int) {
	products := make([]domain.Product, 0)
	errorsSkipped := 0

	doc.Find(".card.thumbnail").Each(func(_ int, card *goquery.Selection) {
		name := strings.TrimSpace(card.Find("a.title").First().Text())
		if name == "" {
			errorsSkipped++
			return
		}

		if !isLenovoProduct(name) {
			return
		}

		product, err := parseProduct(card, pageURL, name)
		if err != nil {
			errorsSkipped++
			return
		}

		products = append(products, product)
	})

	return products, errorsSkipped
}

// parseProduct extrai os dados de um único card de produto.
func parseProduct(card *goquery.Selection, pageURL, name string) (domain.Product, error) {
	price, err := parsePrice(card.Find(".price span").First().Text())
	if err != nil {
		return domain.Product{}, err
	}

	description := strings.TrimSpace(card.Find(".description").First().Text())
	if description == "" {
		return domain.Product{}, fmt.Errorf("empty description")
	}

	reviews, err := parseReviews(card.Find(".review-count").First().Text())
	if err != nil {
		return domain.Product{}, err
	}

	ratings := card.Find(".ratings").First()
	if ratings.Length() == 0 {
		return domain.Product{}, fmt.Errorf("empty rating")
	}

	link, err := absoluteProductLink(card, pageURL)
	if err != nil {
		return domain.Product{}, err
	}

	return domain.Product{
		Name:        name,
		Price:       price,
		Description: description,
		Rating:      ratings.Find(".ws-icon-star").Length(),
		Reviews:     reviews,
		Link:        link,
	}, nil
}

// parsePrice limpa e converte o preço textual para float64.
func parsePrice(raw string) (float64, error) {
	cleaned := strings.TrimSpace(raw)
	cleaned = strings.TrimPrefix(cleaned, "$")
	cleaned = strings.ReplaceAll(cleaned, ",", "")

	if cleaned == "" {
		return 0, fmt.Errorf("empty price")
	}

	price, err := strconv.ParseFloat(cleaned, 64)
	if err != nil {
		return 0, fmt.Errorf("parse price %q: %w", raw, err)
	}

	return price, nil
}

// parseReviews extrai a primeira sequência numérica do texto de avaliações.
func parseReviews(raw string) (int, error) {
	match := integerPattern.FindString(raw)
	if match == "" {
		return 0, fmt.Errorf("parse reviews %q: missing integer", raw)
	}

	reviews, err := strconv.Atoi(match)
	if err != nil {
		return 0, fmt.Errorf("parse reviews %q: %w", raw, err)
	}

	return reviews, nil
}

// absoluteProductLink resolve o link relativo do produto usando a URL da página atual.
func absoluteProductLink(card *goquery.Selection, pageURL string) (string, error) {
	href, ok := card.Find("a.title").First().Attr("href")
	if !ok || strings.TrimSpace(href) == "" {
		return "", fmt.Errorf("empty product link")
	}

	return resolveURL(pageURL, href)
}

// nextPageURL identifica a próxima página da paginação.
// Retorna string vazia quando não existe próxima página.
func nextPageURL(doc *goquery.Document, pageURL string) (string, error) {
	next := doc.Find("#static-pagination a.page-link.next").First()
	if next.Length() == 0 {
		return "", nil
	}

	if next.Parent().HasClass("disabled") {
		return "", nil
	}

	href, ok := next.Attr("href")
	if !ok || strings.TrimSpace(href) == "" {
		return "", nil
	}

	return resolveURL(pageURL, href)
}

// resolveURL transforma uma URL relativa em absoluta com base em uma URL de origem.
func resolveURL(baseURL, rawURL string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse base URL %q: %w", baseURL, err)
	}

	ref, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return "", fmt.Errorf("parse URL %q: %w", rawURL, err)
	}

	return base.ResolveReference(ref).String(), nil
}

// isLenovoProduct verifica se o nome do produto contém algum termo Lenovo conhecido.
func isLenovoProduct(name string) bool {
	normalized := strings.ToLower(name)

	for _, term := range lenovoProductTerms {
		if strings.Contains(normalized, term) {
			return true
		}
	}

	return false
}

// durationFromEnv lê uma duração de uma variável de ambiente.
// Caso esteja vazia, inválida ou negativa, retorna o fallback.
func durationFromEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	duration, err := time.ParseDuration(value)
	if err != nil || duration < 0 {
		return fallback
	}

	return duration
}

// intFromEnv lê um inteiro de uma variável de ambiente.
// Caso esteja vazio, inválido ou negativo, retorna o fallback.
func intFromEnv(name string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}

	number, err := strconv.Atoi(value)
	if err != nil || number < 0 {
		return fallback
	}

	return number
}

// scrapeContextError melhora mensagens de erro relacionadas ao contexto,
// especialmente quando ocorre timeout.
func scrapeContextError(pagesVisited int, err error) error {
	if err == context.DeadlineExceeded {
		return fmt.Errorf("scrape timeout after %d page(s): %w", pagesVisited, err)
	}

	return err
}

// waitForNextPage aguarda o delay configurado entre páginas,
// respeitando cancelamento ou timeout do contexto.
func waitForNextPage(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// scrapeResult monta o resultado final do scraping,
// incluindo métricas da execução.
func scrapeResult(
	products []domain.Product,
	pagesVisited,
	errorsSkipped int,
	start time.Time,
	sourceURL string,
) domain.ScrapeResult {
	return domain.ScrapeResult{
		SourceURL:     sourceURL,
		ScrapedAt:     start.UTC().Format(time.RFC3339),
		Total:         len(products),
		PagesVisited:  pagesVisited,
		ErrorsSkipped: errorsSkipped,
		DurationMS:    time.Since(start).Milliseconds(),
		Data:          products,
	}
}

// drainAndClose consome e fecha o corpo da resposta HTTP.
// Isso ajuda no reaproveitamento de conexões pelo cliente HTTP.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
