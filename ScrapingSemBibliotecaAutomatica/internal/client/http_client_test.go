package client

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

//
// TestDoRetriesUntilSuccess
//
// Valida se o client realiza retries automaticamente até receber
// uma resposta de sucesso.
//
// Fluxo esperado:
//   - 1ª tentativa -> 500
//   - 2ª tentativa -> 500
//   - 3ª tentativa -> 200 OK
//
// Também garante que os headers padrão foram enviados.
//

func TestDoRetriesUntilSuccess(t *testing.T) {
	var attempts atomic.Int32

	var mu sync.Mutex
	var receivedHeaders []http.Header

	// Servidor fake para simular falhas temporárias.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = append(receivedHeaders, r.Header.Clone())
		mu.Unlock()

		// As duas primeiras tentativas retornam erro.
		if attempts.Add(1) <= 2 {
			http.Error(w, "temporary error", http.StatusInternalServerError)
			return
		}

		// Terceira tentativa retorna sucesso.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(3),
		WithBackoff(time.Millisecond, time.Millisecond),
	)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	defer resp.Body.Close()

	// Valida status final.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	// Deve ter tentado exatamente 3 vezes.
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}

	if string(body) != "ok" {
		t.Fatalf("expected body ok, got %q", body)
	}

	// Verifica se os headers padrão foram enviados.
	mu.Lock()
	defer mu.Unlock()

	if len(receivedHeaders) == 0 {
		t.Fatal("expected at least one request")
	}

	firstHeaders := receivedHeaders[0]

	if got := firstHeaders.Get("User-Agent"); got == "" {
		t.Fatal("expected User-Agent header")
	}

	if got := firstHeaders.Get("Accept"); got == "" {
		t.Fatal("expected Accept header")
	}

	if got := firstHeaders.Get("Accept-Language"); got == "" {
		t.Fatal("expected Accept-Language header")
	}
}

//
// TestDoReturnsLastResponseWhenServerAlwaysFails
//
// Valida se o client retorna a última resposta HTTP recebida
// mesmo após esgotar todas as tentativas.
//
// Fluxo esperado:
//   - Todas as tentativas retornam 500
//   - Nenhum erro de execução deve ser retornado
//   - A última response deve ser entregue ao caller
//

func TestDoReturnsLastResponseWhenServerAlwaysFails(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "still failing", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(2),
		WithBackoff(time.Millisecond, time.Millisecond),
	)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected final HTTP response, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	// 1 tentativa inicial + 2 retries = 3
	if got := attempts.Load(); got != 3 {
		t.Fatalf("expected 3 attempts, got %d", got)
	}
}

//
// TestDoClosesRetriedResponseBodies
//
// Garante que o body das respostas descartadas durante retry
// seja corretamente fechado.
//
// Isso evita:
//   - vazamento de conexões
//   - esgotamento do pool HTTP
//   - goroutines presas aguardando leitura
//

func TestDoClosesRetriedResponseBodies(t *testing.T) {
	var attempts atomic.Int32
	firstBodyClosed := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempt := attempts.Add(1)

		// Primeira tentativa falha.
		if attempt == 1 {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(http.StatusInternalServerError)

			if flusher, ok := w.(http.Flusher); ok {
				_, _ = w.Write([]byte("temporary error"))
				flusher.Flush()
			}

			// Aguarda o fechamento do contexto da request.
			select {
			case <-r.Context().Done():
				close(firstBodyClosed)

			case <-time.After(time.Second):
			}

			return
		}

		// Segunda tentativa retorna sucesso.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(1),
		WithBackoff(time.Millisecond, time.Millisecond),
	)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	defer resp.Body.Close()

	// Verifica se o body da tentativa anterior foi fechado.
	select {
	case <-firstBodyClosed:

	case <-time.After(time.Second):
		t.Fatal("expected body from retried response to be closed")
	}

	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

// TestDoDoesNotRetryPostByDefault documenta a decisao conservadora:
// POST nao retrya automaticamente porque nao e idempotente.
func TestDoDoesNotRetryPostByDefault(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "temporary error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(3),
		WithBackoff(time.Millisecond, time.Millisecond),
	)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		server.URL,
		bytes.NewReader([]byte(`{"hello":"world"}`)),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected final HTTP response, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d", resp.StatusCode)
	}

	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected POST to avoid retry by default, got %d attempts", got)
	}
}

// POST nao retrya por padrao porque nao e idempotente. Este teste opta
// explicitamente pelo retry para representar endpoints seguros para replay,
// como operacoes protegidas por chave de idempotencia.
func TestDoRetriesPostWithReusableBody(t *testing.T) {
	var attempts atomic.Int32

	var mu sync.Mutex
	var receivedBodies []string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusInternalServerError)
			return
		}

		mu.Lock()
		receivedBodies = append(receivedBodies, string(body))
		mu.Unlock()

		// Primeira tentativa falha.
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary error", http.StatusInternalServerError)
			return
		}

		// Segunda tentativa retorna sucesso.
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(1),
		WithBackoff(time.Millisecond, time.Millisecond),
		WithAdditionalRetryMethods(http.MethodPost),
	)

	reqBody := []byte(`{"hello":"world"}`)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		server.URL,
		bytes.NewReader(reqBody),
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected success, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(receivedBodies) != 2 {
		t.Fatalf("expected 2 request bodies, got %d", len(receivedBodies))
	}

	// Ambas as tentativas devem receber exatamente o mesmo payload.
	for i, body := range receivedBodies {
		if body != string(reqBody) {
			t.Fatalf(
				"expected body %q on attempt %d, got %q",
				reqBody,
				i+1,
				body,
			)
		}
	}
}

//
// TestDoStopsRetriesWhenContextIsCanceled
//
// Garante que o retry seja interrompido imediatamente
// quando o contexto for cancelado.
//
// Fluxo esperado:
//   - Primeira tentativa falha
//   - Context é cancelado
//   - Nenhum retry adicional acontece
//   - Erro retornado deve ser context.Canceled
//

func TestDoStopsRetriesWhenContextIsCanceled(t *testing.T) {
	var attempts atomic.Int32

	var once sync.Once
	firstAttemptDone := make(chan struct{})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer once.Do(func() {
			close(firstAttemptDone)
		})

		attempts.Add(1)
		http.Error(w, "temporary error", http.StatusInternalServerError)
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(5),
		WithBackoff(500*time.Millisecond, 500*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	done := make(chan error, 1)

	go func() {
		resp, err := client.Do(req)

		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}

		done <- err
	}()

	// Aguarda primeira tentativa.
	select {
	case <-firstAttemptDone:
		cancel()

	case <-time.After(time.Second):
		t.Fatal("first attempt did not happen")
	}

	// Deve encerrar imediatamente após cancelamento.
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}

	case <-time.After(time.Second):
		t.Fatal("request did not stop after context cancellation")
	}

	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt before cancellation, got %d", got)
	}
}

//
// TestDoDoesNotRetryNonRetriableStatus
//
// Garante que códigos HTTP não-retentáveis
// (como 400 Bad Request) não gerem retries.
//

func TestDoDoesNotRetryNonRetriableStatus(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(3),
		WithBackoff(time.Millisecond, time.Millisecond),
	)

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodGet,
		server.URL,
		nil,
	)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected response, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", resp.StatusCode)
	}

	// Não deve haver retry para erro 400.
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt, got %d", got)
	}
}

func TestDefaultClientCanRequestHTTPS(t *testing.T) {
	server, rootCAs := newTrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(0),
		WithTLSRootCAs(rootCAs),
	)

	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.client.Transport)
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatal("expected default HTTPS client to use configured root CAs")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected HTTPS request to succeed, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestClientWithBrowserTLSFingerprintCanRequestHTTPS(t *testing.T) {
	server, rootCAs := newTrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(0),
		WithTLSRootCAs(rootCAs),
		WithBrowserTLSFingerprint(true),
	)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected HTTPS request with TLS fingerprint to succeed, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func TestTLSFingerprintDoesNotBreakRetries(t *testing.T) {
	var attempts atomic.Int32

	server, rootCAs := newTrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			http.Error(w, "temporary error", http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(1),
		WithBackoff(time.Millisecond, time.Millisecond),
		WithTLSRootCAs(rootCAs),
		WithBrowserTLSFingerprint(true),
	)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected retry to succeed, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestInvalidTLSFingerprintFallsBackToSafeDefault(t *testing.T) {
	server, rootCAs := newTrustedTLSServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer server.Close()

	client := NewHTTPClient(
		time.Second,
		WithMaxRetries(0),
		WithTLSRootCAs(rootCAs),
		WithTLSFingerprint("not-a-browser-profile"),
	)

	if client.tlsFingerprintProfile != tlsFingerprintDisabled {
		t.Fatalf("expected invalid TLS fingerprint to be disabled, got %q", client.tlsFingerprintProfile)
	}

	transport, ok := client.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", client.client.Transport)
	}

	if transport.DialTLSContext != nil {
		t.Fatal("expected invalid TLS fingerprint to use the standard TLS transport")
	}

	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected HTTPS request with fallback transport to succeed, got error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}
}

func newTrustedTLSServer(t *testing.T, handler http.Handler) (*httptest.Server, *x509.CertPool) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	now := time.Now()
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Lenovo Scraper Test CA"},
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caDER, err := x509.CreateCertificate(
		rand.Reader,
		caTemplate,
		caTemplate,
		&caKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}

	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("failed to parse CA certificate: %v", err)
	}

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    now.Add(-time.Minute),
		NotAfter:     now.Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
	}

	serverDER, err := x509.CreateCertificate(
		rand.Reader,
		serverTemplate,
		caCert,
		&serverKey.PublicKey,
		caKey,
	)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}

	serverCert, err := x509.ParseCertificate(serverDER)
	if err != nil {
		t.Fatalf("failed to parse server certificate: %v", err)
	}

	certificate := tls.Certificate{
		Certificate: [][]byte{serverDER, caDER},
		PrivateKey:  serverKey,
	}

	server := httptest.NewUnstartedServer(handler)
	if listener, err := net.Listen("tcp", "[::1]:0"); err == nil {
		_ = server.Listener.Close()
		server.Listener = listener
	}

	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{certificate},
		MinVersion:   tls.VersionTLS12,
	}
	server.StartTLS()

	conn, err := tls.Dial("tcp", server.Listener.Addr().String(), &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
	})
	if err != nil {
		t.Fatalf("failed to inspect test server certificate: %v", err)
	}
	defer conn.Close()

	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(caCert)

	// Some developer machines intercept local TLS. Trust the observed
	// loopback certificate in tests without disabling certificate checks.
	state := conn.ConnectionState()
	if got := state.PeerCertificates[0].Issuer.CommonName; got != caTemplate.Subject.CommonName {
		for _, cert := range state.PeerCertificates {
			rootCAs.AddCert(cert)
		}
	}

	if _, err := serverCert.Verify(x509.VerifyOptions{
		DNSName: "127.0.0.1",
		Roots:   rootCAs,
	}); err != nil {
		t.Fatalf("test server certificate is not trusted by its CA: %v", err)
	}

	return server, rootCAs
}
