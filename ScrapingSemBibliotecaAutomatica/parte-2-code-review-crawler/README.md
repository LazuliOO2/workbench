# Code review do crawler

## Problemas encontrados

1. A URL inicial era `page-2.htm`, pulando a primeira pagina e usando uma extensao incorreta para o site. Em producao, a coleta poderia comecar incompleta ou parar em um 404.

2. O crawler nao verificava `StatusCode`. Paginas de erro como 429, 500 ou 503 poderiam ser parseadas como HTML valido, gerando resultado vazio, inconsistente ou uma parada prematura sem diagnostico claro.

3. Nao havia timeout nem retry. Em rede instavel ou site lento, o processo poderia ficar preso indefinidamente; em falhas temporarias, a coleta era encerrada mesmo quando uma nova tentativa resolveria.

4. O header `Accept-Encoding: zstd` era perigoso. O transporte HTTP padrao do Go nao descompacta zstd automaticamente; se o servidor respondesse com esse encoding, o parser receberia bytes comprimidos e a coleta falharia.

5. A ultima pagina era parseada duas vezes quando nao havia link `next`, duplicando livros no resultado.

6. Os resultados ficavam em uma variavel global e eram impressos apenas no final. Se o processo caisse no meio, todo o progresso era perdido; alem disso, uma coleta grande poderia consumir memoria desnecessaria.

7. Nao havia checkpoint. Ao reiniciar depois de uma queda, o processo comecaria do zero, com risco de duplicar itens ou nunca completar uma coleta longa.

8. `hasClass` usava `strings.Contains`, entao classes como `not-next` poderiam ser tratadas como `next`. Em producao, isso pode causar paginacao errada e dados incorretos.

9. A descoberta da proxima pagina dependia da posicao exata dos filhos no DOM. Uma pequena mudanca de HTML poderia quebrar a paginacao.

10. O corpo da resposta era lido sem limite. Uma resposta muito grande ou maliciosa poderia aumentar o uso de memoria do processo.

11. O projeto nao tinha `go.mod`, embora dependesse de `golang.org/x/net/html`. Em um ambiente limpo, o build poderia falhar por falta de modulo.

## Correcoes aplicadas

- A coleta agora comeca em `https://books.toscrape.com/catalogue/page-1.html` e resolve links relativos com `net/url`.
- Foi adicionado um `http.Client` com timeout configuravel.
- Erros transitorios de rede e HTTP 408, 429, 500, 502, 503 e 504 recebem retry com backoff exponencial. O header `Retry-After` e respeitado quando presente.
- Status HTTP nao-2xx que nao sao transitorios viram erro explicito.
- O header `Accept-Encoding: zstd` foi removido.
- A extracao de livros retorna uma lista local, sem estado global.
- A ultima pagina nao e mais parseada duas vezes.
- A verificacao de classes agora compara tokens completos.
- A paginacao procura um link dentro de `li.next`, sem depender de indices fixos no DOM.
- A resposta HTML e limitada a 10 MiB.
- A saida e gravada incrementalmente em `books.ndjson`, com `fsync` apos cada pagina.
- O progresso e salvo em `crawler_state.json` com escrita atomica via arquivo temporario e `rename`.
- Ao reiniciar, o crawler le a saida existente, evita duplicatas por URL do livro e continua a partir da proxima pagina salva.
- Se uma queda deixar a ultima linha do NDJSON parcial, o crawler trunca apenas essa linha incompleta antes de continuar.
- Foram adicionadas flags para facilitar operacao: `-start-url`, `-output`, `-state`, `-timeout`, `-retries` e `-max-pages`.

## Biblioteca utilizada

O crawler usa `golang.org/x/net/html`. Ela foi mantida porque e um parser HTML tolerante a markup real de paginas web, mais seguro e correto do que extrair dados de HTML com expressoes regulares. O `go.mod` foi adicionado para tornar a dependencia reprodutivel.

## Como executar

```sh
go run .
```

Para testar apenas uma pagina:

```sh
go run . -max-pages=1
```

Arquivos gerados em runtime:

- `books.ndjson`: um livro por linha em JSON.
- `crawler_state.json`: checkpoint usado para retomar a coleta.

## Melhorias futuras

- Implementar concorrencia com Goroutines e Channels para baixar varias paginas em paralelo, com limite de workers para nao sobrecarregar o site.
- Consultar e respeitar `robots.txt`, alem de adicionar um intervalo configuravel entre requisicoes para tornar o crawler mais educado.
- Adicionar testes com `httptest.Server` cobrindo retries, timeout, `Retry-After` e respostas HTTP reais sem depender da internet.
- Exportar metricas simples, como paginas processadas, livros coletados, retries e tempo medio por pagina.
