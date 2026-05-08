package main

import (
	"fmt"
	"log"
	"os"

	"github.com/playwright-community/playwright-go"
)

func main() {
	//Executa playwright.Run()Recebe DOIS valores de retornoGuarda em:pw err seria o mesmo que um try catch de python := é simbola da variavel
	pw, err := playwright.Run()
	// se erro não for vazio %v é um placeholder.
	if err != nil {
		log.Fatalf("Erro ao iniciar Playwright: %v", err)
	}

	// 1. LÓGICA DE LANÇAMENTO: O modo 'Headless' (sem janela) é o que mais denuncia bots.
	// Para testes, usamos false. Em produção, usamos true + técnicas extras.
	browser, err := pw.Chromium.Launch(playwright.BrowserTypeLaunchOptions{
		Headless: playwright.Bool(false),
	})
	if err != nil {
		log.Fatalf("Erro ao abrir navegador: %v", err)
	}

	// 2. LÓGICA DE CONTEXTO: Aqui definimos o 'User-Agent'.
	// Um bot comum se identifica como "HeadlessChrome". Nós fingimos ser um Chrome real.
	context, err := browser.NewContext(playwright.BrowserNewContextOptions{
		UserAgent: playwright.String("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"),
	})
	if err != nil {
		log.Fatalf("Erro ao criar contexto: %v", err)
	}

	// 3. LÓGICA DE INJEÇÃO (Anti-Fingerprint):
	// O site executa um JavaScript chamado 'navigator.webdriver' que retorna 'true' em automações.
	// Nós injetamos um script que roda ANTES de qualquer outro para "mentir" esse valor.
	// AddInitScript adiciona um script que será executado automaticamente em todas as páginas daquele contexto do navegador, antes do site carregar.
	// O navigate guarda informação idioma userAgent plataforma e também navigator.webdriver Então pegamos ele const newProto = Object.getPrototypeOf(navigator);
	// delete newProto.webdriver; Isso tentamos apagar o campo:navigator.webdriver e depois
	// Object.setPrototypeOf(navigator, newProto); Define novamente o prototype alterado no objeto navigator.
	err = context.AddInitScript(playwright.Script{
		Content: playwright.String(`
		const newProto = Object.getPrototypeOf(navigator);
		delete newProto.webdriver;
		Object.setPrototypeOf(navigator, newProto);
	`),
	})
	if err != nil {
		log.Fatal(err)
	}
	//O context é um BrowserContext.Pensa nele como um “perfil temporário do navegador”:cookies separadoslocalStorage separadosessões separadasconfigurações próprias
	// É como abrir um mini-Chrome isolado dentro do Chrome principal
	page, err := context.NewPage()
	if err != nil {
		log.Fatal(err)
	}

	// 4. EXECUÇÃO: Navegando para o desafio
	fmt.Println("Navegando e aguardando testes...")
	if _, err = page.Goto("https://bot.sannysoft.com/"); err != nil {
		log.Fatal(err)
	}

	// page.WaitForLoadState(...)manda o Playwright esperar um certo estado de carregamento da página.
	if err := page.WaitForLoadState(playwright.PageWaitForLoadStateOptions{
		// State: playwright.LoadStateNetworkidle → espera até a rede ficar ociosa (network idle), ou seja:normalmente significa que não há requisições de rede acontecendo por um pequeno período.
		State: playwright.LoadStateNetworkidle,
	}); err != nil {
		log.Fatal(err)
	}

	// Tiramos o print para validar se ficou tudo verde
	path := "resultado_evasao.png"
	if _, err = page.Screenshot(playwright.PageScreenshotOptions{
		Path:     playwright.String(path),
		FullPage: playwright.Bool(true),
	}); err != nil {
		log.Fatal(err)
	}

	// ===== EXTRAÇÃO DOS DADOS =====

	// Pega o texto completo da página
	locator := page.Locator("body")

	bodyText, err := locator.TextContent()
	if err != nil {
		log.Fatal(err)
	}
	// Cria arquivo TXT
	file, err := os.Create("resultado.txt")
	if err != nil {
		log.Fatal(err)
	}
	defer file.Close()

	// Escreve no arquivo
	_, err = file.WriteString(bodyText)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("TXT salvo com sucesso!")

	fmt.Printf("Concluído! Verifique o arquivo %s para ver o resultado.\n", path)

	browser.Close()
	pw.Stop()
}
