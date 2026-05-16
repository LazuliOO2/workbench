package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Configurações padrão do crawler.
const (
	defaultStartURL = "https://books.toscrape.com/catalogue/page-1.html"
	defaultOutput   = "books.ndjson"
	defaultState    = "crawler_state.json"
	maxPageBytes    = 10 << 20 // Limite de 10 MB por página HTML.
)

// Config guarda as opções usadas para executar o crawler.
type Config struct {
	StartURL       string
	OutputPath     string
	StatePath      string
	RequestTimeout time.Duration
	MaxRetries     int
	MaxPages       int
}

// Book representa um livro extraído da página.
type Book struct {
	Title       string `json:"title"`
	Price       string `json:"price"`
	Rating      string `json:"rating"`
	URL         string `json:"url,omitempty"`
	SourcePage  string `json:"source_page"`
	CollectedAt string `json:"collected_at"`
}

// CrawlerState representa o estado salvo em disco para permitir retomada.
type CrawlerState struct {
	StartURL       string          `json:"start_url"`
	NextURL        string          `json:"next_url"`
	ProcessedPages map[string]bool `json:"processed_pages"`
	Completed      bool            `json:"completed"`
	UpdatedAt      string          `json:"updated_at"`
}

// fetchResult encapsula o resultado de uma tentativa de baixar uma página.
type fetchResult struct {
	doc        *html.Node
	retryable  bool
	retryAfter time.Duration
	err        error
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("crawler: ")

	cfg := parseConfig()

	if err := validateConfig(cfg); err != nil {
		log.Fatal(err)
	}

	// Cria um contexto cancelável com Ctrl+C.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := crawl(ctx, cfg); err != nil {
		log.Fatal(err)
	}
}

// parseConfig lê argumentos da linha de comando.
func parseConfig() Config {
	cfg := Config{}

	flag.StringVar(&cfg.StartURL, "start-url", defaultStartURL, "absolute URL for the first page")
	flag.StringVar(&cfg.OutputPath, "output", defaultOutput, "NDJSON file where books are appended")
	flag.StringVar(&cfg.StatePath, "state", defaultState, "JSON checkpoint file used to resume interrupted crawls")
	flag.DurationVar(&cfg.RequestTimeout, "timeout", 15*time.Second, "timeout for each HTTP request")
	flag.IntVar(&cfg.MaxRetries, "retries", 4, "number of retries for transient HTTP/network errors")
	flag.IntVar(&cfg.MaxPages, "max-pages", 0, "maximum pages to crawl; 0 means no limit")

	flag.Parse()
	return cfg
}

// validateConfig valida os parâmetros recebidos.
func validateConfig(cfg Config) error {
	if cfg.RequestTimeout <= 0 {
		return errors.New("timeout must be greater than zero")
	}
	if cfg.MaxRetries < 0 {
		return errors.New("retries cannot be negative")
	}
	if cfg.MaxPages < 0 {
		return errors.New("max-pages cannot be negative")
	}

	parsed, err := url.Parse(cfg.StartURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return fmt.Errorf("start-url must be an absolute URL: %q", cfg.StartURL)
	}

	if strings.TrimSpace(cfg.OutputPath) == "" {
		return errors.New("output path cannot be empty")
	}
	if strings.TrimSpace(cfg.StatePath) == "" {
		return errors.New("state path cannot be empty")
	}

	return nil
}

// crawl executa o processo principal:
// carrega estado, baixa páginas, extrai livros e salva o progresso.
func crawl(ctx context.Context, cfg Config) error {
	client := &http.Client{Timeout: cfg.RequestTimeout}
	startURL := normalizeURL(cfg.StartURL)

	// Carrega livros já salvos para evitar duplicados.
	seenBooks, err := loadSeenBooks(cfg.OutputPath)
	if err != nil {
		return err
	}

	// Carrega ou cria estado do crawler.
	state, err := loadState(cfg.StatePath, startURL)
	if err != nil {
		return err
	}

	if state.Completed {
		log.Printf("crawl already completed; output is in %s", cfg.OutputPath)
		return nil
	}

	nextURL := state.NextURL
	pagesCrawled := 0
	totalWritten := 0

	for nextURL != "" {
		if cfg.MaxPages > 0 && pagesCrawled >= cfg.MaxPages {
			log.Printf("page limit reached; next run will resume at %s", nextURL)
			break
		}

		// Protege contra loops de paginação.
		if state.ProcessedPages[nextURL] {
			return fmt.Errorf("detected a pagination loop at %s", nextURL)
		}

		log.Printf("collecting %s", nextURL)

		doc, err := fetchPage(ctx, client, nextURL, cfg.MaxRetries)
		if err != nil {
			return fmt.Errorf("fetch %s: %w", nextURL, err)
		}

		collectedAt := time.Now().UTC().Format(time.RFC3339)

		books := parseBooks(doc, nextURL, collectedAt)

		written, err := appendBooks(cfg.OutputPath, books, seenBooks)
		if err != nil {
			return fmt.Errorf("write output: %w", err)
		}

		// Atualiza estado após processar a página.
		state.ProcessedPages[nextURL] = true
		state.NextURL = getNextPage(doc, nextURL)
		state.Completed = state.NextURL == ""
		state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)

		if err := saveState(cfg.StatePath, state); err != nil {
			return fmt.Errorf("save state: %w", err)
		}

		pagesCrawled++
		totalWritten += written

		log.Printf("page done: %d books found, %d new written", len(books), written)

		nextURL = state.NextURL
	}

	if state.Completed {
		log.Printf("crawl completed; %d new books written to %s", totalWritten, cfg.OutputPath)
	} else {
		log.Printf("crawl paused; %d pages crawled and %d new books written", pagesCrawled, totalWritten)
	}

	return nil
}

// fetchPage tenta baixar e parsear uma página HTML, com retentativas.
func fetchPage(ctx context.Context, client *http.Client, rawURL string, retries int) (*html.Node, error) {
	var lastErr error

	for attempt := 0; attempt <= retries; attempt++ {
		result := fetchPageOnce(ctx, client, rawURL)

		if result.err == nil {
			return result.doc, nil
		}

		lastErr = result.err

		if !result.retryable || attempt == retries {
			break
		}

		delay := result.retryAfter
		if delay <= 0 {
			delay = retryDelay(attempt)
		}

		log.Printf("retryable error for %s: %v; retrying in %s", rawURL, result.err, delay)

		if err := sleepContext(ctx, delay); err != nil {
			return nil, err
		}
	}

	return nil, lastErr
}

// fetchPageOnce faz uma única requisição HTTP e transforma o HTML em árvore DOM.
func fetchPageOnce(ctx context.Context, client *http.Client, rawURL string) fetchResult {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return fetchResult{err: err}
	}

	req.Header.Set("User-Agent", "code-review-crawler/1.0 (+https://books.toscrape.com)")
	req.Header.Set("Accept", "text/html,application/xhtml+xml")

	resp, err := client.Do(req)
	if err != nil {
		return fetchResult{retryable: true, err: err}
	}
	defer resp.Body.Close()

	if isRetryableStatus(resp.StatusCode) {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

		return fetchResult{
			retryable:  true,
			retryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			err:        fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))

		return fetchResult{
			err: fmt.Errorf("HTTP %d %s", resp.StatusCode, http.StatusText(resp.StatusCode)),
		}
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBytes+1))
	if err != nil {
		return fetchResult{retryable: true, err: fmt.Errorf("read body: %w", err)}
	}

	if len(body) > maxPageBytes {
		return fetchResult{err: fmt.Errorf("page is larger than %d bytes", maxPageBytes)}
	}

	doc, err := html.Parse(bytes.NewReader(body))
	if err != nil {
		return fetchResult{err: fmt.Errorf("parse HTML: %w", err)}
	}

	return fetchResult{doc: doc}
}

// isRetryableStatus indica se o status HTTP permite nova tentativa.
func isRetryableStatus(status int) bool {
	switch status {
	case http.StatusRequestTimeout,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

// parseRetryAfter interpreta o cabeçalho Retry-After.
func parseRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)

	if value == "" {
		return 0
	}

	if seconds, err := time.ParseDuration(value + "s"); err == nil && seconds > 0 {
		return seconds
	}

	if when, err := http.ParseTime(value); err == nil {
		if delay := time.Until(when); delay > 0 {
			return delay
		}
	}

	return 0
}

// retryDelay calcula backoff exponencial com jitter.
func retryDelay(attempt int) time.Duration {
	delay := 500 * time.Millisecond

	for i := 0; i < attempt && delay < 8*time.Second; i++ {
		delay *= 2
	}

	if delay > 8*time.Second {
		delay = 8 * time.Second
	}

	jitter := time.Duration(rand.Int63n(int64(delay / 2)))
	return delay + jitter
}

// sleepContext espera respeitando cancelamento do contexto.
func sleepContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// parseBooks encontra todos os cards de livros na página.
func parseBooks(doc *html.Node, pageURL string, collectedAt string) []Book {
	var books []Book

	walk(doc, func(n *html.Node) {
		if n.Type != html.ElementNode || !hasClass(n, "product_pod") {
			return
		}

		book := extractBook(n, pageURL, collectedAt)
		if book.Title != "" {
			books = append(books, book)
		}
	})

	return books
}

// extractBook extrai título, preço, rating e URL de um card.
func extractBook(n *html.Node, pageURL string, collectedAt string) Book {
	book := Book{
		SourcePage:  pageURL,
		CollectedAt: collectedAt,
	}

	walk(n, func(child *html.Node) {
		if child.Type != html.ElementNode {
			return
		}

		switch child.Data {
		case "a":
			title := attr(child, "title")
			if title == "" {
				return
			}

			book.Title = title

			if href := attr(child, "href"); href != "" {
				book.URL = resolveURL(pageURL, href)
			}

		case "p":
			if hasClass(child, "price_color") {
				book.Price = textContent(child)
			}

			if hasClass(child, "star-rating") {
				book.Rating = ratingFromClasses(child)
			}
		}
	})

	return book
}

// getNextPage encontra o link da próxima página.
func getNextPage(doc *html.Node, currentURL string) string {
	var next string

	walk(doc, func(n *html.Node) {
		if next != "" || n.Type != html.ElementNode || n.Data != "li" || !hasClass(n, "next") {
			return
		}

		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "a" {
				next = resolveURL(currentURL, attr(c, "href"))
				return
			}
		}
	})

	return next
}

// walk percorre recursivamente a árvore HTML.
func walk(n *html.Node, visit func(*html.Node)) {
	if n == nil {
		return
	}

	visit(n)

	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, visit)
	}
}

// attr retorna o valor de um atributo HTML.
func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return strings.TrimSpace(a.Val)
		}
	}

	return ""
}

// hasClass verifica se um nó possui determinada classe CSS.
func hasClass(n *html.Node, class string) bool {
	for _, c := range classList(n) {
		if c == class {
			return true
		}
	}

	return false
}

// classList separa as classes CSS de um nó.
func classList(n *html.Node) []string {
	classes := attr(n, "class")

	if classes == "" {
		return nil
	}

	return strings.Fields(classes)
}

// ratingFromClasses extrai o rating a partir das classes CSS.
func ratingFromClasses(n *html.Node) string {
	for _, class := range classList(n) {
		if class != "star-rating" {
			return class
		}
	}

	return ""
}

// textContent extrai texto puro de um nó HTML.
func textContent(n *html.Node) string {
	var builder strings.Builder

	walk(n, func(child *html.Node) {
		if child.Type == html.TextNode {
			builder.WriteString(child.Data)
			builder.WriteByte(' ')
		}
	})

	return strings.Join(strings.Fields(builder.String()), " ")
}

// resolveURL transforma links relativos em URLs absolutas.
func resolveURL(baseURL string, href string) string {
	href = strings.TrimSpace(href)

	if href == "" {
		return ""
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return href
	}

	ref, err := url.Parse(href)
	if err != nil {
		return href
	}

	return normalizeURL(base.ResolveReference(ref).String())
}

// normalizeURL remove espaços e fragmentos da URL.
func normalizeURL(rawURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return strings.TrimSpace(rawURL)
	}

	parsed.Fragment = ""
	return parsed.String()
}

// loadSeenBooks lê o arquivo NDJSON existente e monta um mapa de livros já salvos.
func loadSeenBooks(path string) (map[string]struct{}, error) {
	seen := make(map[string]struct{})

	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if errors.Is(err, os.ErrNotExist) {
		return seen, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open output: %w", err)
	}
	defer file.Close()

	reader := bufio.NewReader(file)

	var offset int64
	lineNumber := 0

	for {
		line, readErr := reader.ReadString('\n')

		if len(line) > 0 {
			lineStart := offset
			offset += int64(len(line))
			lineNumber++

			trimmed := strings.TrimSpace(line)
			if trimmed != "" {
				var book Book

				if err := json.Unmarshal([]byte(trimmed), &book); err != nil {
					// Se a última linha estiver incompleta, ela é truncada.
					if readErr == io.EOF {
						if truncateErr := file.Truncate(lineStart); truncateErr != nil {
							return nil, fmt.Errorf("repair partial output line: %w", truncateErr)
						}

						return seen, nil
					}

					return nil, fmt.Errorf("invalid JSON in %s at line %d: %w", path, lineNumber, err)
				}

				if key := bookKey(book); key != "" {
					seen[key] = struct{}{}
				}
			}
		}

		if readErr == io.EOF {
			break
		}

		if readErr != nil {
			return nil, fmt.Errorf("read output: %w", readErr)
		}
	}

	return seen, nil
}

// appendBooks adiciona novos livros ao arquivo NDJSON.
func appendBooks(path string, books []Book, seen map[string]struct{}) (int, error) {
	if len(books) == 0 {
		return 0, nil
	}

	if err := ensureParentDir(path); err != nil {
		return 0, err
	}

	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return 0, err
	}

	encoder := json.NewEncoder(file)
	written := 0

	for _, book := range books {
		key := bookKey(book)

		if key == "" {
			continue
		}

		if _, exists := seen[key]; exists {
			continue
		}

		if err := encoder.Encode(book); err != nil {
			_ = file.Close()
			return written, err
		}

		seen[key] = struct{}{}
		written++
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return written, err
	}

	if err := file.Close(); err != nil {
		return written, err
	}

	return written, nil
}

// bookKey cria uma chave única para evitar duplicação.
func bookKey(book Book) string {
	if book.URL != "" {
		return normalizeURL(book.URL)
	}

	title := strings.TrimSpace(strings.ToLower(book.Title))
	if title == "" {
		return ""
	}

	return title + "|" + strings.TrimSpace(book.Price)
}

// loadState carrega o checkpoint do crawler.
func loadState(path string, startURL string) (CrawlerState, error) {
	state := CrawlerState{
		StartURL:       startURL,
		NextURL:        startURL,
		ProcessedPages: make(map[string]bool),
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("read state: %w", err)
	}

	if len(bytes.TrimSpace(data)) == 0 {
		return state, nil
	}

	if err := json.Unmarshal(data, &state); err != nil {
		return state, fmt.Errorf("parse state: %w", err)
	}

	if state.StartURL != startURL {
		return state, fmt.Errorf("state file belongs to %s, not %s", state.StartURL, startURL)
	}

	if state.ProcessedPages == nil {
		state.ProcessedPages = make(map[string]bool)
	}

	if state.NextURL == "" && !state.Completed {
		state.NextURL = startURL
	}

	state.NextURL = normalizeURL(state.NextURL)

	return state, nil
}

// saveState salva o estado de forma segura usando arquivo temporário.
func saveState(path string, state CrawlerState) error {
	if err := ensureParentDir(path); err != nil {
		return err
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	data = append(data, '\n')

	tmpPath := path + ".tmp"

	file, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}

	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}

	if err := file.Close(); err != nil {
		return err
	}

	return os.Rename(tmpPath, path)
}

// ensureParentDir cria o diretório pai, caso ele ainda não exista.
func ensureParentDir(path string) error {
	dir := filepath.Dir(path)

	if dir == "." || dir == "" {
		return nil
	}

	return os.MkdirAll(dir, 0o755)
}
