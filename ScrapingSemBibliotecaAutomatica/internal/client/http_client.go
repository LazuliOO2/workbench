package client

import (
	"context"
	stdtls "crypto/tls"
	"crypto/x509"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

const (
	// Número padrão de tentativas adicionais.
	// Exemplo: 3 retries = 4 tentativas totais.
	defaultMaxRetries = 3

	// Delay inicial usado no backoff exponencial.
	defaultBaseDelay = 250 * time.Millisecond

	// Delay máximo permitido no backoff.
	defaultMaxDelay = 2 * time.Second

	tlsFingerprintDisabled = ""
	tlsFingerprintChrome   = "chrome"
)

// HTTPClient encapsula um http.Client com:
//
//   - Retry automático
//   - Backoff exponencial com jitter
//   - Respeito ao contexto (context.Context)
//   - Tratamento de Retry-After
//   - Reaproveitamento eficiente de conexões
type HTTPClient struct {
	client *http.Client

	maxRetries       int
	baseDelay        time.Duration
	maxDelay         time.Duration
	retryableMethods map[string]struct{}

	tlsFingerprintProfile string
	tlsRootCAs            *x509.CertPool
}

// Option representa uma opção funcional usada para configurar o cliente.
type Option func(*HTTPClient)

// WithMaxRetries define a quantidade máxima de retries.
//
// Valores negativos são ignorados.
func WithMaxRetries(maxRetries int) Option {
	return func(c *HTTPClient) {
		if maxRetries >= 0 {
			c.maxRetries = maxRetries
		}
	}
}

// WithBackoff configura os delays do backoff exponencial.
//
// Valores negativos são ignorados.
func WithBackoff(baseDelay, maxDelay time.Duration) Option {
	return func(c *HTTPClient) {
		if baseDelay >= 0 {
			c.baseDelay = baseDelay
		}

		if maxDelay >= 0 {
			c.maxDelay = maxDelay
		}
	}
}

// WithAdditionalRetryMethods adiciona metodos nao-idempotentes a politica de retry.
//
// Use para POST apenas quando a operacao for segura para replay, por exemplo
// endpoints com chave de idempotencia ou contratos de upsert. Requests com body
// ainda precisam disponibilizar GetBody para permitir reenvio seguro do payload.
func WithAdditionalRetryMethods(methods ...string) Option {
	return func(c *HTTPClient) {
		if c.retryableMethods == nil {
			c.retryableMethods = defaultRetryableMethods()
		}

		for _, method := range methods {
			normalized := normalizeHTTPMethod(method)
			if normalized == "" {
				continue
			}

			c.retryableMethods[normalized] = struct{}{}
		}
	}
}

// WithBrowserTLSFingerprint enables or disables a Chrome-like TLS ClientHello.
//
// This changes only the TLS handshake used by HTTPS requests. Retries,
// backoff, headers and context cancellation stay in the regular Do path.
func WithBrowserTLSFingerprint(enabled bool) Option {
	return func(c *HTTPClient) {
		if enabled {
			c.tlsFingerprintProfile = tlsFingerprintChrome
			return
		}

		c.tlsFingerprintProfile = tlsFingerprintDisabled
	}
}

// WithTLSFingerprint configures the TLS ClientHello profile.
//
// Supported values:
//   - "chrome" or "chrome-auto"
//   - "", "off", "disabled", "false", "standard" or "go" to disable it
//
// Unknown values fall back to the standard Go transport.
func WithTLSFingerprint(profile string) Option {
	return func(c *HTTPClient) {
		normalized := normalizeTLSFingerprintProfile(profile)
		if normalized == tlsFingerprintDisabled {
			c.tlsFingerprintProfile = tlsFingerprintDisabled
			return
		}

		if _, ok := clientHelloIDForProfile(normalized); !ok {
			c.tlsFingerprintProfile = tlsFingerprintDisabled
			return
		}

		c.tlsFingerprintProfile = normalized
	}
}

// WithTLSRootCAs configures trusted root CAs for HTTPS requests.
//
// It is useful for tests and private environments without disabling
// certificate verification.
func WithTLSRootCAs(rootCAs *x509.CertPool) Option {
	return func(c *HTTPClient) {
		if rootCAs != nil {
			c.tlsRootCAs = rootCAs
		}
	}
}

// NewHTTPClient cria um novo cliente HTTP configurado com:
//
//   - Pool de conexões reutilizáveis
//   - Timeouts seguros
//   - Retry automático
//   - Backoff exponencial com jitter
//
// O timeout informado é aplicado ao http.Client inteiro.
func NewHTTPClient(timeout time.Duration, opts ...Option) *HTTPClient {
	c := &HTTPClient{
		maxRetries:       defaultMaxRetries,
		baseDelay:        defaultBaseDelay,
		maxDelay:         defaultMaxDelay,
		retryableMethods: defaultRetryableMethods(),
	}

	// Aplica opções customizadas.
	for _, opt := range opts {
		opt(c)
	}

	c.client = &http.Client{
		Timeout:   timeout,
		Transport: c.transport(),
	}

	return c
}

func (c *HTTPClient) transport() *http.Transport {
	dialer := &net.Dialer{
		Timeout:   5 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ForceAttemptHTTP2:     true,
		DialContext:           dialer.DialContext,
	}

	helloID, ok := clientHelloIDForProfile(c.tlsFingerprintProfile)
	if ok {
		transport.ForceAttemptHTTP2 = false
		transport.DialTLSContext = uTLSDialTLSContext(dialer, helloID, c.tlsRootCAs)
		return transport
	}

	if c.tlsRootCAs != nil {
		transport.TLSClientConfig = &stdtls.Config{
			MinVersion: stdtls.VersionTLS12,
			RootCAs:    c.tlsRootCAs,
		}
	}

	return transport
}

func uTLSDialTLSContext(
	dialer *net.Dialer,
	helloID utls.ClientHelloID,
	rootCAs *x509.CertPool,
) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		conn, err := dialer.DialContext(ctx, network, addr)
		if err != nil {
			return nil, err
		}

		tlsConn := utls.UClient(conn, &utls.Config{
			MinVersion: utls.VersionTLS12,
			RootCAs:    rootCAs,
			ServerName: serverNameFromAddr(addr),
		}, helloID)

		// Alguns fingerprints Chrome do uTLS anunciam "h2" via ALPN por padrão.
		// Como este transporte usa HTTP/1.1, sobrescrevemos o ALPN para evitar
		// negociar HTTP/2 e receber frames binários em uma conexão HTTP/1.x.
		if err := tlsConn.BuildHandshakeState(); err != nil {
			_ = conn.Close()
			return nil, err
		}

		for _, ext := range tlsConn.Extensions {
			if alpn, ok := ext.(*utls.ALPNExtension); ok {
				alpn.AlpnProtocols = []string{"http/1.1"} // Força HTTP/1.1
				break
			}
		}

		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = conn.Close()
			return nil, err
		}

		return tlsConn, nil
	}
}
func serverNameFromAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return strings.Trim(addr, "[]")
	}

	return strings.Trim(host, "[]")
}

func normalizeTLSFingerprintProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "off", "disabled", "false", "standard", "go":
		return tlsFingerprintDisabled
	case "chrome", "chrome-auto":
		return tlsFingerprintChrome
	default:
		return strings.ToLower(strings.TrimSpace(profile))
	}
}

func clientHelloIDForProfile(profile string) (utls.ClientHelloID, bool) {
	switch profile {
	case tlsFingerprintChrome:
		return utls.HelloChrome_Auto, true
	default:
		return utls.ClientHelloID{}, false
	}
}

// Do executa uma requisição HTTP com suporte a:
//
//   - Retry automático
//   - Backoff exponencial
//   - Retry-After
//   - Cancelamento via contexto
//
// O retry acontece apenas quando:
//
//   - O método HTTP é idempotente
//   - O body pode ser recriado
//   - O status ou erro é considerado retryable
//
// Metodos nao-idempotentes so entram no retry quando liberados por opcao.
func (c *HTTPClient) Do(req *http.Request) (*http.Response, error) {
	// Verifica se o contexto já foi cancelado.
	if err := req.Context().Err(); err != nil {
		return nil, err
	}

	// Total de tentativas:
	// primeira execução + retries.
	attempts := c.maxRetries + 1

	for attempt := 0; attempt < attempts; attempt++ {
		// Clona a request para reutilizar body em retries.
		attemptReq, err := cloneRequest(req, attempt)
		if err != nil {
			return nil, err
		}

		// Define headers similares aos de navegador.
		setBrowserHeaders(attemptReq)

		resp, err := c.client.Do(attemptReq)
		if err != nil {
			// Se o contexto foi cancelado, retorna imediatamente.
			if ctxErr := req.Context().Err(); ctxErr != nil {
				return nil, ctxErr
			}

			// Última tentativa ou request não retryable.
			if attempt == attempts-1 || !c.canRetryRequest(req) {
				return nil, err
			}

			// Aguarda antes do retry.
			if err := c.waitBeforeRetry(
				req.Context(),
				c.backoffDelay(attempt),
			); err != nil {
				return nil, err
			}

			continue
		}

		// Se não deve retryar ou chegou na última tentativa,
		// retorna a resposta imediatamente.
		if !c.shouldRetryResponse(req, resp) || attempt == attempts-1 {
			return resp, nil
		}

		// Prioriza Retry-After quando disponível.
		delay := retryAfterDelay(resp)

		// Caso não exista Retry-After,
		// utiliza backoff exponencial.
		if delay <= 0 {
			delay = c.backoffDelay(attempt)
		}

		// Drena e fecha o body para reutilizar conexão.
		drainAndClose(resp.Body)

		// Aguarda antes do próximo retry.
		if err := c.waitBeforeRetry(req.Context(), delay); err != nil {
			return nil, err
		}
	}

	// Fallback improvável.
	return nil, context.Canceled
}

// cloneRequest cria uma cópia segura da request.
//
// Para retries, o body precisa ser recriado via GetBody.
func cloneRequest(req *http.Request, attempt int) (*http.Request, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()

	// Requests sem body.
	if req.Body == nil || req.Body == http.NoBody {
		cloned.Body = req.Body
		return cloned, nil
	}

	// Primeira tentativa reutiliza body original.
	if attempt == 0 {
		cloned.Body = req.Body
		return cloned, nil
	}

	// Retries precisam recriar body.
	body, err := req.GetBody()
	if err != nil {
		return nil, err
	}

	cloned.Body = body

	return cloned, nil
}

// shouldRetryResponse determina se a resposta deve ser retryada.
func (c *HTTPClient) shouldRetryResponse(req *http.Request, resp *http.Response) bool {
	return c.canRetryRequest(req) && isRetryableStatus(resp.StatusCode)
}

// canRetryRequest verifica se a request pode ser repetida pela politica atual.
func (c *HTTPClient) canRetryRequest(req *http.Request) bool {
	return canRetryRequest(req) && c.isRetryableMethod(req.Method)
}

// canRetryRequest verifica se a request pode ser repetida.
//
// Requests com body precisam fornecer GetBody
// para permitir replay seguro.
func canRetryRequest(req *http.Request) bool {
	return req.Body == nil ||
		req.Body == http.NoBody ||
		req.GetBody != nil
}

func (c *HTTPClient) isRetryableMethod(method string) bool {
	_, ok := c.retryableMethods[normalizeHTTPMethod(method)]
	return ok
}

func defaultRetryableMethods() map[string]struct{} {
	return map[string]struct{}{
		http.MethodGet:     {},
		http.MethodHead:    {},
		http.MethodOptions: {},
		http.MethodTrace:   {},
	}
}

// isIdempotentMethod verifica se o método HTTP
// é considerado seguro para retry automático.
func isIdempotentMethod(method string) bool {
	switch normalizeHTTPMethod(method) {
	case http.MethodGet,
		http.MethodHead,
		http.MethodOptions,
		http.MethodTrace:
		return true

	default:
		return false
	}
}

func normalizeHTTPMethod(method string) string {
	return strings.ToUpper(strings.TrimSpace(method))
}

// isRetryableStatus define quais status HTTP
// permitem retry automático.
func isRetryableStatus(statusCode int) bool {
	return statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

// retryAfterDelay interpreta o header Retry-After.
//
// Suporta:
//
//   - segundos inteiros
//   - data HTTP RFC1123
func retryAfterDelay(resp *http.Response) time.Duration {
	value := resp.Header.Get("Retry-After")

	if value == "" {
		return 0
	}

	// Retry-After em segundos.
	if seconds, err := strconv.Atoi(value); err == nil {
		return time.Duration(seconds) * time.Second
	}

	// Retry-After em formato de data HTTP.
	if retryTime, err := http.ParseTime(value); err == nil {
		delay := time.Until(retryTime)

		if delay > 0 {
			return delay
		}
	}

	return 0
}

// waitBeforeRetry aguarda o delay respeitando cancelamento
// via contexto.
func (c *HTTPClient) waitBeforeRetry(
	ctx context.Context,
	delay time.Duration,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if delay <= 0 {
		return nil
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

// backoffDelay calcula o delay usando:
//
//	backoff exponencial + jitter aleatório
//
// Exemplo:
//
//	250ms
//	500ms
//	1s
//	2s
func (c *HTTPClient) backoffDelay(attempt int) time.Duration {
	delay := c.baseDelay

	// Crescimento exponencial.
	for i := 0; i < attempt; i++ {
		delay *= 2

		if c.maxDelay > 0 && delay > c.maxDelay {
			delay = c.maxDelay
			break
		}
	}

	if delay <= 0 {
		return 0
	}

	// Adiciona jitter aleatório para evitar thundering herd.
	jitter := rand.N(delay / 2)

	delay += jitter

	// Garante limite máximo.
	if c.maxDelay > 0 && delay > c.maxDelay {
		return c.maxDelay
	}

	return delay
}

// setBrowserHeaders adiciona headers comuns de navegador
// quando não estiverem definidos.
func setBrowserHeaders(req *http.Request) {
	setHeaderIfEmpty(
		req,
		"User-Agent",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36",
	)

	setHeaderIfEmpty(
		req,
		"Accept",
		"text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,*/*;q=0.8",
	)

	setHeaderIfEmpty(
		req,
		"Accept-Language",
		"pt-BR,pt;q=0.9,en-US;q=0.8,en;q=0.7",
	)
}

// setHeaderIfEmpty define um header apenas se ele
// ainda não existir.
func setHeaderIfEmpty(req *http.Request, key, value string) {
	if req.Header.Get(key) == "" {
		req.Header.Set(key, value)
	}
}

// drainAndClose consome completamente o body
// e fecha a conexão.
//
// Isso permite que o pool HTTP reutilize
// a conexão TCP corretamente.
func drainAndClose(body io.ReadCloser) {
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
