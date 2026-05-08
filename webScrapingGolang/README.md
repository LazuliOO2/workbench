# 🕷️ WebScraping – Evasão de Detecção Anti-Bot com Go + Playwright

## 📦 Descrição

**WebScraping** é um projeto desenvolvido em **Go** utilizando **Playwright** para estudar técnicas modernas de automação web e evasão de detecção anti-bot.

O sistema navega até o site de testes de fingerprint:

https://bot.sannysoft.com/

e aplica algumas estratégias básicas de camuflagem para reduzir sinais clássicos de automação detectados por sites modernos.

Além disso, o projeto:

- captura screenshots do resultado da evasão;
- extrai o conteúdo textual da página;
- salva logs em arquivo `.txt`;
- demonstra conceitos importantes de:
  - fingerprinting;
  - `navigator.webdriver`;
  - User-Agent spoofing;
  - Browser Context isolation;
  - carregamento assíncrono de páginas.

O objetivo principal é **estudo técnico**, entendimento de como sites detectam automações e aprendizado de scraping moderno utilizando Go.

---

## 📁 Estrutura do Projeto

```bash
📂 WEBSCRAPING/
├── 📄 go.mod                  → Dependências do projeto Go
├── 📄 go.sum                  → Checksums das dependências
├── 📄 main.go                 → Código principal da automação
├── 📄 README.md               → Documentação do projeto
├── 📄 resultado_evasao.png    → Screenshot do teste anti-bot
└── 📄 resultado.txt           → Texto extraído da página
```

---

## 🛠️ Tecnologias Utilizadas

- **Go (Golang)** – Linguagem principal
- **Playwright-Go** – Automação do navegador
- **Chromium** – Navegador controlado pelo Playwright
- **JavaScript Injection** – Modificação de propriedades do navegador
- **HTTP / Browser Fingerprinting** – Técnicas de evasão

---

## ⚙️ Instalação

### 1️⃣ Clonar o repositório

```bash
git clone <URL_DO_REPOSITORIO>
cd WEBSCRAPING
```

---

### 2️⃣ Instalar dependências Go

```bash
go mod tidy
```

---

### 3️⃣ Instalar navegadores do Playwright

```bash
go run github.com/playwright-community/playwright-go/cmd/playwright install
```

---

## 🚀 Execução

Execute o projeto com:

```bash
go run main.go
```

---

## 🧠 O Que o Projeto Faz

### 🌐 Navegação Automatizada

O sistema abre um navegador Chromium automatizado utilizando Playwright.

---

### 🕵️ User-Agent Customizado

Bots normalmente possuem User-Agent suspeito.

O projeto substitui isso por um User-Agent semelhante ao Chrome real:

```go
Mozilla/5.0 (Windows NT 10.0; Win64; x64)
```

---

### 🚫 Remoção do `navigator.webdriver`

Muitos sites detectam bots verificando:

```javascript
navigator.webdriver
```

O projeto injeta JavaScript antes da página carregar para remover essa flag:

```javascript
const newProto = Object.getPrototypeOf(navigator);
delete newProto.webdriver;
Object.setPrototypeOf(navigator, newProto);
```

---

### 📸 Captura de Screenshot

Após carregar o site:

- o sistema tira um screenshot completo;
- salva como:

```bash
resultado_evasao.png
```

---

### 📄 Extração de Conteúdo

O texto completo da página é extraído automaticamente e salvo em:

```bash
resultado.txt
```

---

## 📊 Fluxo da Aplicação

```text
Inicia Playwright
        ↓
Abre Chromium
        ↓
Cria contexto isolado
        ↓
Define User-Agent
        ↓
Injeta Anti-Fingerprint
        ↓
Acessa bot.sannysoft.com
        ↓
Aguarda carregamento
        ↓
Captura screenshot
        ↓
Extrai conteúdo da página
        ↓
Salva arquivos locais
```

---

## 🔍 Conceitos Estudados

### 🧬 Fingerprinting

Sites modernos identificam navegadores através de:

- WebDriver
- WebGL
- Canvas
- User-Agent
- Plugins
- Permissões
- Idioma
- Resolução
- GPU
- Timezone
- Comportamento de navegação

---

### 🤖 Detecção de Bots

O projeto demonstra como automações básicas são detectadas e como algumas técnicas podem reduzir sinais óbvios.

---

### ⚡ Browser Context

Cada `context` do Playwright funciona como um navegador isolado:

- cookies separados;
- sessões independentes;
- localStorage isolado;
- configurações próprias.

---

## 📸 Exemplo de Resultado

Após a execução:

### Arquivos gerados:

```bash
resultado_evasao.png
resultado.txt
```

---

## 💡 Possíveis Melhorias Futuras

- Rotação automática de proxies
- Spoofing de WebGL/GPU
- Randomização de comportamento humano
- Simulação de movimento de mouse
- Delay inteligente entre ações
- Uso de stealth plugins
- Execução headless mais avançada
- Pool de navegadores concorrentes
- Exportação estruturada em JSON

---

## 📚 Aprendizados do Projeto

Este projeto explora conceitos importantes de:

- Web Scraping moderno
- Automação de navegadores
- Engenharia reversa de detecção
- Browser fingerprinting
- Playwright com Go
- Navegação assíncrona
- Extração automatizada de dados

---

## ⚠️ Aviso

Projeto desenvolvido com fins educacionais e de estudo técnico.

O uso de scraping deve sempre respeitar:

- Termos de uso dos sites;
- robots.txt;
- limites de requisição;
- legislação local;
- ética no uso de automação.

---

## 🤝 Contribuição

Contribuições são bem-vindas!

Sinta-se livre para abrir:

- Issues
- Pull Requests
- Sugestões de melhorias

---

## 📜 Licença

Este projeto está licenciado sob a licença MIT.
