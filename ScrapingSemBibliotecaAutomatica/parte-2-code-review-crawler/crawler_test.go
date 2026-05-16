package main

import (
	"net/http"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

//
// ==============================
// TESTES DA FUNÇÃO parseBooks
// ==============================
//

// TestParseBooks valida se a função parseBooks:
//
// 1. Extrai corretamente:
//
//   - título
//
//   - preço
//
//   - rating
//
//   - URL absoluta
//
//     2. Ignora livros inválidos
//     (ex.: quando o atributo title está ausente)
//
// 3. Resolve URLs relativas corretamente
func TestParseBooks(t *testing.T) {

	// HTML fake usado como entrada do teste.
	// Simula parte da estrutura do site books.toscrape.com.
	doc := parseHTML(t, `
		<html>
			<body>

				<!-- Livro válido -->
				<article class="product_pod">
					<h3>
						<a href="a-light-in-the-attic_1000/index.html" title="A Light in the Attic">
							A Light in the Attic
						</a>
					</h3>

					<p class="price_color">&pound;51.77</p>
					<p class="star-rating Three"></p>
				</article>

				<!-- Livro válido com URL relativa usando ../ -->
				<article class="product_pod featured">
					<h3>
						<a href="../tipping-the-velvet_999/index.html" title="Tipping the Velvet">
							Tipping the Velvet
						</a>
					</h3>

					<p class="price_color">
						&pound;53.74
					</p>

					<p class="star-rating One"></p>
				</article>

				<!-- Livro inválido (sem atributo title) -->
				<article class="product_pod">
					<a href="missing-title.html">Missing title is ignored</a>

					<p class="price_color">&pound;10.00</p>
					<p class="star-rating Two"></p>
				</article>

			</body>
		</html>
	`)

	// Página de origem simulada.
	pageURL := "https://books.toscrape.com/catalogue/page-1.html"

	// Timestamp usado para verificar o campo CollectedAt.
	collectedAt := "2026-05-16T20:00:00Z"

	// Executa a função sendo testada.
	got := parseBooks(doc, pageURL, collectedAt)

	// Resultado esperado.
	want := []Book{
		{
			Title:       "A Light in the Attic",
			Price:       "\u00a351.77",
			Rating:      "Three",
			URL:         "https://books.toscrape.com/catalogue/a-light-in-the-attic_1000/index.html",
			SourcePage:  pageURL,
			CollectedAt: collectedAt,
		},
		{
			Title:       "Tipping the Velvet",
			Price:       "\u00a353.74",
			Rating:      "One",
			URL:         "https://books.toscrape.com/tipping-the-velvet_999/index.html",
			SourcePage:  pageURL,
			CollectedAt: collectedAt,
		},
	}

	// Compara resultado obtido com resultado esperado.
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("parseBooks() = %#v, want %#v", got, want)
	}
}

//
// ==================================
// TESTES DA FUNÇÃO getNextPage
// ==================================
//

// TestGetNextPage valida:
//
// 1. Se o link da próxima página é encontrado corretamente
// 2. Se URLs relativas são resolvidas corretamente
// 3. Se classes parciais NÃO são aceitas
// 4. Se retorna string vazia quando não existe próxima página
func TestGetNextPage(t *testing.T) {

	// Tabela de testes (table-driven tests).
	tests := []struct {
		name       string
		currentURL string
		html       string
		want       string
	}{
		{
			name:       "resolves relative next link",
			currentURL: "https://books.toscrape.com/catalogue/page-1.html",

			html: `
				<ul class="pager">
					<li class="current">Page 1 of 50</li>
					<li class="next">
						<a href="page-2.html">next</a>
					</li>
				</ul>
			`,

			want: "https://books.toscrape.com/catalogue/page-2.html",
		},

		{
			name:       "does not match partial class names",
			currentURL: "https://books.toscrape.com/catalogue/page-1.html",

			html: `
				<ul class="pager">
					<li class="not-next">
						<a href="page-2.html">next</a>
					</li>
				</ul>
			`,

			// Não deve encontrar link válido.
			want: "",
		},

		{
			name:       "returns empty string when there is no next link",
			currentURL: "https://books.toscrape.com/catalogue/page-50.html",

			html: `
				<ul class="pager">
					<li class="previous">
						<a href="page-49.html">previous</a>
					</li>
				</ul>
			`,

			want: "",
		},
	}

	// Executa todos os cenários da tabela.
	for _, tt := range tests {

		// Cada cenário vira um subteste.
		t.Run(tt.name, func(t *testing.T) {

			// Faz parse do HTML fake.
			doc := parseHTML(t, tt.html)

			// Executa função.
			got := getNextPage(doc, tt.currentURL)

			// Valida resultado.
			if got != tt.want {
				t.Fatalf("getNextPage() = %q, want %q", got, tt.want)
			}
		})
	}
}

//
// =========================================
// TESTES DA FUNÇÃO isRetryableStatus
// =========================================
//

// TestIsRetryableStatus valida se determinados
// códigos HTTP devem gerar retry automático.
//
// Exemplos:
//   - 500 → retry
//   - 503 → retry
//   - 404 → NÃO retry
func TestIsRetryableStatus(t *testing.T) {

	tests := []struct {
		status int
		want   bool
	}{
		// Status normais.
		{status: http.StatusOK, want: false},
		{status: http.StatusMovedPermanently, want: false},
		{status: http.StatusNotFound, want: false},

		// Status temporários / retryáveis.
		{status: http.StatusRequestTimeout, want: true},
		{status: http.StatusTooManyRequests, want: true},
		{status: http.StatusInternalServerError, want: true},
		{status: http.StatusBadGateway, want: true},
		{status: http.StatusServiceUnavailable, want: true},
		{status: http.StatusGatewayTimeout, want: true},
	}

	for _, tt := range tests {

		// Usa o nome do status HTTP como nome do subteste.
		t.Run(http.StatusText(tt.status), func(t *testing.T) {

			got := isRetryableStatus(tt.status)

			if got != tt.want {
				t.Fatalf(
					"isRetryableStatus(%d) = %t, want %t",
					tt.status,
					got,
					tt.want,
				)
			}
		})
	}
}

//
// =====================================
// FUNÇÃO AUXILIAR PARA TESTES HTML
// =====================================
//

// parseHTML transforma uma string HTML
// em um *html.Node para uso nos testes.
//
// Se ocorrer erro no parse,
// o teste falha imediatamente.
func parseHTML(t *testing.T, source string) *html.Node {
	t.Helper()

	// Faz parse do HTML fornecido.
	doc, err := html.Parse(strings.NewReader(source))
	if err != nil {
		t.Fatalf("parse test HTML: %v", err)
	}

	return doc
}
