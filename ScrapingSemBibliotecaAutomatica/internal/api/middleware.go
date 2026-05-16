package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"lenovo-scraper/internal/domain"
)

// contextKey define um tipo privado para evitar colisões
// de chaves no contexto da aplicação.
type contextKey string

const (
	// requestIDKey é a chave usada para armazenar
	// o request ID dentro do contexto da requisição.
	requestIDKey contextKey = "request_id"

	// requestIDHeader é o header HTTP utilizado
	// para transportar o identificador da requisição.
	requestIDHeader = "X-Request-ID"
)

// RequestID é um middleware responsável por:
//
//  1. Ler o header X-Request-ID da requisição.
//  2. Validar o identificador recebido.
//  3. Gerar um novo ID caso ele seja inválido.
//  4. Armazenar o ID no contexto da requisição.
//  5. Retornar o ID no header da resposta.
//
// Isso facilita rastreamento, debugging e correlação
// de logs entre serviços distribuídos.
func RequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := strings.TrimSpace(r.Header.Get(requestIDHeader))

		// Gera um novo ID caso o recebido seja inválido.
		if !validRequestID(requestID) {
			requestID = newRequestID()
		}

		// Armazena o request ID no contexto.
		ctx := context.WithValue(r.Context(), requestIDKey, requestID)

		// Retorna o ID no header da resposta.
		w.Header().Set(requestIDHeader, requestID)

		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// Recover é um middleware responsável por capturar panics
// durante o processamento da requisição.
//
// Quando um panic ocorre:
//
//  1. O stack trace é registrado no logger.
//  2. Informações da requisição são incluídas no log.
//  3. Uma resposta HTTP 500 é enviada ao cliente.
//
// Isso evita que a aplicação seja encerrada por erros inesperados.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				requestID, _ := RequestIDFromContext(r.Context())

				slog.ErrorContext(
					r.Context(),
					"panic recovered",
					"request_id", requestID,
					"error", recovered,
					"method", r.Method,
					"path", r.URL.Path,
					"stack", string(debug.Stack()),
				)

				writeJSON(w, http.StatusInternalServerError, domain.ErrorResponse{
					Error: "internal server error",
				})
			}
		}()

		next.ServeHTTP(w, r)
	})
}

// RequestIDFromContext recupera o request ID armazenado
// no contexto da requisição.
//
// Retorna:
//   - string: o request ID.
//   - bool: indica se o valor foi encontrado.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	requestID, ok := ctx.Value(requestIDKey).(string)
	return requestID, ok
}

// newRequestID gera um novo identificador único para requisições.
//
// O ID é criado usando 16 bytes aleatórios codificados em hexadecimal.
// Caso a geração criptográfica falhe, um fallback baseado em timestamp
// é utilizado.
func newRequestID() string {
	var b [16]byte

	if _, err := rand.Read(b[:]); err == nil {
		return hex.EncodeToString(b[:])
	}

	return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
}

// validRequestID valida se um request ID:
//
//   - Não é vazio.
//
//   - Possui no máximo 128 caracteres.
//
//   - Contém apenas caracteres seguros:
//
//     a-z A-Z 0-9 - _ .
//
// Isso evita problemas de segurança e logs malformados.
func validRequestID(id string) bool {
	if len(id) == 0 || len(id) > 128 {
		return false
	}

	for _, r := range id {
		if !(r >= 'a' && r <= 'z') &&
			!(r >= 'A' && r <= 'Z') &&
			!(r >= '0' && r <= '9') &&
			r != '-' &&
			r != '_' &&
			r != '.' {
			return false
		}
	}

	return true
}
