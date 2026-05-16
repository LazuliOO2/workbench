# Lenovo Scraper

API RESTful em Go para coletar notebooks Lenovo do site de testes da Web Scraper, ordenar os produtos do menor para o maior preco e retornar o resultado em JSON.

Fonte dos dados:

```text
https://webscraper.io/test-sites/e-commerce/static/computers/laptops
```

O projeto usa scraping direto por HTTP. Ele nao utiliza Selenium, Puppeteer, Playwright ou automacao de navegador.

## Objetivo Parte 1 

O objetivo deste projeto e demonstrar uma implementacao limpa e testavel de scraping com Go, cobrindo:

- requisicoes HTTP diretas para paginas HTML;
- parsing de HTML com seletores CSS;
- filtro de produtos Lenovo;
- ordenacao por menor preco;
- exposicao dos dados em uma API RESTful;
- resiliencia no cliente HTTP com retry, backoff exponencial e jitter;
- rastreabilidade da coleta com `source_url` e `scraped_at`;
- prontidao operacional com endpoint de health check;
- testes automatizados cobrindo API, cliente HTTP e scraper.

## Como Rodar Localmente

Requisitos:

- Go 1.25+

Instale as dependencias e rode os testes:

```sh
go mod tidy
go test ./...
```

Inicie a API:

```sh
go run cmd/api/main.go
```

Por padrao, o servidor sobe em:

```text
http://localhost:8080
```

Para usar outra porta:

```sh
PORT=3000 go run cmd/api/main.go
```

No PowerShell:

```powershell
$env:PORT="3000"
go run cmd/api/main.go
```

## Endpoints

### Listar Notebooks Lenovo

```http
GET /api/v1/notebooks/lenovo
```

Exemplo:

```sh
curl http://localhost:8080/api/v1/notebooks/lenovo
```

O endpoint executa ou reutiliza o resultado do scraping, filtra notebooks Lenovo, ordena por preco crescente e retorna JSON.

### Health Check

```http
GET /health
```

Exemplo:

```sh
curl http://localhost:8080/health
```

Resposta:

```json
{
  "status": "ok"
}
```

Este endpoint nao dispara scraping e nao depende do site externo. Ele indica apenas que o processo HTTP esta vivo.

## Exemplo de Resposta JSON

```json
{
  "source_url": "https://webscraper.io/test-sites/e-commerce/static/computers/laptops",
  "scraped_at": "2026-05-16T12:34:56Z",
  "total": 2,
  "pages_visited": 2,
  "errors_skipped": 0,
  "duration_ms": 842,
  "data": [
    {
      "name": "Lenovo IdeaPad 3",
      "price": 499.5,
      "description": "Notebook description",
      "rating": 4,
      "reviews": 12,
      "link": "https://webscraper.io/test-sites/e-commerce/static/product/..."
    },
    {
      "name": "Lenovo ThinkPad T14",
      "price": 899.99,
      "description": "Notebook description",
      "rating": 5,
      "reviews": 21,
      "link": "https://webscraper.io/test-sites/e-commerce/static/product/..."
    }
  ]
}
```

Campos de rastreabilidade:

- `source_url`: URL inicial usada na coleta.
- `scraped_at`: instante da coleta em formato RFC3339.

Metricas da execucao:

- `total`: quantidade de produtos Lenovo validos retornados.
- `pages_visited`: quantidade de paginas visitadas.
- `errors_skipped`: quantidade de cards invalidos ignorados durante o parsing.
- `duration_ms`: duracao total do scraping em milissegundos.

## Decisoes Tecnicas

### Go

O projeto foi implementado em Go por sua simplicidade operacional, boa biblioteca padrao para HTTP, concorrencia nativa e facilidade para escrever testes rapidos e deterministas.

### `net/http`

A API e o cliente HTTP usam `net/http`, mantendo o projeto leve e sem dependencias de automacao de navegador. O scraping acontece por requisicoes HTTP diretas.

### `goquery`

O parsing do HTML e feito com `github.com/PuerkitoBio/goquery`, que permite navegar pelo documento usando seletores CSS. Isso deixa a extracao dos cards de produto simples e legivel.

### API RESTful

A camada HTTP expoe recursos claros:

- `GET /api/v1/notebooks/lenovo` para obter os notebooks Lenovo.
- `GET /health` para verificar se o servico esta vivo.

As respostas sao JSON e usam status HTTP apropriados para sucesso, timeout, erro externo e erro interno.

### Retry, Backoff Exponencial e Jitter

O pacote `internal/client` encapsula um cliente HTTP com retry automatico para falhas temporarias, como HTTP `429` e erros `5xx`.

O retry usa:

- backoff exponencial;
- jitter para reduzir rajadas simultaneas;
- suporte a `Retry-After`;
- respeito a `context.Context`;
- replay seguro apenas quando o metodo e o body permitem retry.

### Cache em Memoria

A camada de API mantem o ultimo resultado em cache por um periodo configuravel. Isso reduz chamadas repetidas ao site externo e melhora a latencia para requisicoes consecutivas.

O cache e local ao processo. Em producao com multiplas instancias, uma alternativa como Redis seria mais adequada.

### `singleflight`

O handler usa `golang.org/x/sync/singleflight` para evitar que varias requisicoes simultaneas disparem varios scrapings iguais ao mesmo tempo. Quando uma coleta ja esta em andamento, as demais requisicoes aguardam o mesmo resultado.

### `context.Context` e Timeouts

O projeto usa `context.Context` para propagar cancelamento e deadlines entre a API, o scraper e o cliente HTTP.

Tambem ha timeouts configuraveis para:

- tempo maximo da requisicao de scraping;
- timeout do cliente HTTP;
- timeouts do servidor HTTP.

### Graceful Shutdown

O servidor em `cmd/api/main.go` escuta sinais do sistema (`SIGINT` e `SIGTERM`) e executa shutdown gracioso, permitindo que requisicoes em andamento terminem dentro de um prazo definido.

### Middlewares

A API inclui middlewares para:

- `Request ID`: cria ou reaproveita um `X-Request-ID` valido e o propaga no contexto.
- `Recover`: captura panics, registra o erro e retorna HTTP `500` sem derrubar o processo.

### Testes Automatizados

O projeto possui testes para:

- comportamento da API;
- cache e singleflight;
- health check;
- tratamento de erros;
- retry e backoff do cliente HTTP;
- suporte opcional a TLS fingerprint;
- parsing HTML;
- filtro Lenovo;
- paginacao;
- ordenacao por preco.

## Bonus Implementado: TLS Fingerprint com uTLS

O cliente HTTP suporta, de forma opcional, um fingerprint TLS estilo navegador usando:

```text
github.com/refraction-networking/utls
```

Para ativar:

```sh
SCRAPER_TLS_FINGERPRINT=chrome go run cmd/api/main.go
```

No PowerShell:

```powershell
$env:SCRAPER_TLS_FINGERPRINT="chrome"
go run cmd/api/main.go
```

Quando ativado, o transporte HTTPS usa um `ClientHello` proximo ao Chrome. Isso pode ser util em cenarios onde o servidor diferencia clientes por caracteristicas do handshake TLS.

Limite tecnico documentado:

- o modo padrao do Go pode negociar HTTP/2 quando o transporte suporta;
- o modo com uTLS restringe ALPN a `http/1.1`;
- essa decisao evita anunciar HTTP/2 em uma conexao uTLS customizada que o `net/http` nao manipula de forma confiavel sem complexidade adicional.

Configuracoes invalidas de `SCRAPER_TLS_FINGERPRINT` voltam para o comportamento seguro padrao do Go.

## Variaveis de Ambiente

| Variavel | Padrao | Descricao |
| --- | --- | --- |
| `PORT` | `8080` | Porta usada pelo servidor HTTP. |
| `SCRAPE_TIMEOUT` | `30s` | Deadline maximo para uma requisicao de scraping iniciada pela API. |
| `SCRAPER_HTTP_TIMEOUT` | `15s` | Timeout do cliente HTTP usado para buscar paginas. |
| `SCRAPER_PAGE_DELAY` | `500ms` | Intervalo entre paginas durante a paginacao. |
| `SCRAPER_MAX_PAGES` | `60` | Limite maximo de paginas visitadas. Use `0` para desativar o limite. |
| `SCRAPER_TLS_FINGERPRINT` | vazio | Perfil opcional de TLS fingerprint. Use `chrome` para ativar uTLS. |

## Estrutura do Projeto

```text
📂 lenovo-scraper
├── 📁 cmd/
│   └── 📁 api/
│       └── 📄 main.go                       → Inicializa o servidor HTTP, registra rotas e configura graceful shutdown
├── 📁 internal/
│   ├── 📁 api/                              → Camada HTTP da aplicacao
│   │   ├── 📄 handler.go                    → Endpoints, cache, singleflight e respostas JSON
│   │   ├── 📄 middleware.go                 → Middlewares de Request ID e Recover
│   │   └── 📄 handler_test.go               → Testes da camada de API
│   ├── 📁 client/                           → Cliente HTTP customizado
│   │   ├── 📄 http_client.go                → Retry, backoff, headers, timeouts e TLS fingerprint opcional
│   │   └── 📄 http_client_test.go           → Testes do cliente HTTP
│   ├── 📁 scraper/                          → Logica de scraping
│   │   ├── 📄 service.go                    → Busca paginas, faz parsing, paginacao, filtro Lenovo e ordenacao
│   │   └── 📄 service_test.go               → Testes do scraper e parsing
│   └── 📁 domain/                           → Estruturas de dominio e modelos de resposta
│       └── 📄 product.go                    → Produtos, resultado do scraping e respostas JSON
├── 📁 testdata/                             → Fixtures HTML usadas nos testes
├── 📄 Dockerfile                            → Definicao de imagem da aplicacao
├── 📄 go.mod                                → Dependencias do projeto
├── 📄 go.sum                                → Checksums das dependencias
└── 📄 README.md                             → Documentacao do projeto
```

## Como Rodar os Testes

```sh
go test ./...
```

Os testes nao usam automacao de navegador. Eles exercitam o comportamento da API, do scraper e do cliente HTTP com servidores de teste e fixtures locais.

## Observacoes

O scraper depende da estrutura HTML atual do site de teste. Mudancas nos seletores, cards de produto ou paginacao podem exigir ajustes em `internal/scraper/service.go`.

O projeto evita tecnicas agressivas de bypass e nao usa proxy. O objetivo e demonstrar scraping HTTP direto, resiliente e observavel, com uma arquitetura simples para avaliacao tecnica.

## Parte 2 — Revisão de Código

Local: `parte-2-code-review-crawler/`

Arquivo `crawler.go` corrigido a partir do crawler original enviado no desafio.  
Esta parte é independente da API da Parte 1 e não precisa ser executada junto com ela.

A análise dos problemas encontrados e as justificativas das correções estão no README específico da pasta.
