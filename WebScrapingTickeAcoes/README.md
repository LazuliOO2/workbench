# Projeto: Coleta de Tickers com Selenium

## Descrição

Este projeto utiliza a biblioteca Selenium para coletar os tickers das ações da B3 e das criptomoedas a partir do site InfoMoney. A coleta é realizada através de dois códigos separados:

Coleta de tickers das ações da B3: Extrai os tickers diretamente do site InfoMoney.

Coleta de tickers das criptomoedas: Inclui interação com um botão no site InfoMoney para exibir todos os tickers disponíveis.

# Tecnologias Utilizadas

### Python

### Selenium

### WebDriver (por exemplo, ChromeDriver)

# Estrutura do Projeto

O projeto é composto pelos seguintes arquivos principais:

acoes.py: Contém o código para a coleta dos tickers das ações da B3.

criptomoedas.py: Contém o código para a coleta dos tickers das criptomoedas, incluindo a lógica para clicar no botão "Exibir mais" para carregar todos os tickers.

# Requisitos

Certifique-se de ter os seguintes itens instalados e configurados:

### Python (versão 3.6 ou superior)

### Biblioteca Selenium:

pip install selenium

### WebDriver
correspondente ao seu navegador (ex.: ChromeDriver para Google Chrome).

### Navegador Compatível (ex.: Google Chrome).

## Como Executar

Clone o repositório para o seu ambiente local:

git clone <url-do-repositorio>
cd <nome-da-pasta>

Instale as dependências necessárias:

pip install -r requirements.txt

Execute o arquivo para coletar os tickers das ações da B3:

python acoes.py

Execute o arquivo para coletar os tickers das criptomoedas:

python criptomoedas.py

# Funcionalidades

## Coleta de Tickers das Ações da B3

Extrai automaticamente a lista de tickers exibida no site InfoMoney.

## Coleta de Tickers das Criptomoedas

Clica no botão "Exibir mais" para carregar todos os tickers antes de realizar a coleta.

# Observações

Verifique se o WebDriver está atualizado para evitar problemas de compatibilidade com o navegador.

Caso o site InfoMoney altere sua estrutura ou elementos, será necessário ajustar os seletores utilizados no código.

# Licença

Este projeto está licenciado sob a MIT License.