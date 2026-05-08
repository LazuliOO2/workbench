# 🧠 Sistema de Aprendizado por Reforço para Alocação de Portfólio  
### A2C → PPO → PPO + GAE • Pipeline completo de Treino/Teste
## ⚠️ Aviso Importante

Antes de executar qualquer parte do projeto, **todos os arquivos `.ipynb` devem ser convertidos para o formato `.py`**.

O sistema **não funcionará corretamente** enquanto houver notebooks sendo usados diretamente.

Para converter, utilize:

```sh
jupyter nbconvert --to script arquivo.ipynb
```


## 📦 Descrição

Este projeto implementa um **sistema completo de Aprendizado por Reforço (RL)** para otimização de portfólios financeiros.  
Ele segue uma arquitetura modular dividida em:

- Core de dados e lógica financeira  
- Ambiente de simulação estilo OpenAI Gym  
- Modelos Actor–Critic  
- Treinos A2C, PPO e PPO + GAE  
- Avaliação separada em dataset de teste  

O objetivo é permitir que o agente aprenda a:

- 📈 Ajustar pesos do portfólio  
- 📉 Minimizar drawdown  
- 🔁 Controlar turnover (custo de transação)  
- 🎯 Maximizar retorno logarítmico ajustado a risco  

Tudo isso utilizando dados históricos reais e interação ambiente–agente.

# 📊 Resultados Preliminares (Backtest)

> ⚠️ **Disclaimer:** Resultados simulados em backtest histórico (Out-of-Sample). 
> Não consideram custos de slippage (impacto no preço) nem taxas de corretagem reais. 
> O universo de ativos inclui criptomoedas, o que eleva a volatilidade e o retorno potencial.

## ✅ Performance do Modelo: PPO + GAE

O modelo final utilizou a arquitetura **PPO (Proximal Policy Optimization)** com **GAE (Generalized Advantage Estimation)** para estabilização do aprendizado.

**Cenário de Teste (Out-of-Sample):**
O modelo foi avaliado nos 20% finais da série temporal, dados que **nunca** foram vistos durante o treinamento.

### Métricas Principais
| Métrica | Valor | Descrição |
| :--- | :--- | :--- |
| **Duração do Teste** | 794 dias | Janela de avaliação temporal |
| **Retorno Logarítmico** | 0.8925 | Soma dos retornos logarítmicos puros |
| **Retorno Total** | **+144%** | Fator de crescimento de ~2.44x |
| **Reward Médio** | 0.0010 | Média diária da função de recompensa |

### Comparativo Visual de Crescimento
> *Exemplo hipotético de alocação baseada no fator de crescimento:*
> - **Capital Inicial:** R$ 1.000,00
> - **Capital Final:** ~R$ 2.440,00

### 🛡️ Controles de Integridade
Para garantir a confiabilidade técnica do experimento:
1. **Separação Rígida:** Dados de teste isolados temporalmente (split sequencial 80/20).
2. **Prevenção de Data Leakage:** O ambiente `PortfolioEnv` foi auditado para garantir que o estado em `t` utiliza apenas dados de fechamento até `t-1`.
3. **Universo de Ativos:** O portfólio é composto por ativos mistos (B3, ETFs Americanos, Cripto).

---

# 📁 Estrutura do Projeto

```
📂 RL-PORTFOLIO
├── 📄 teste.py           → Núcleo de dados e estatísticas (Core)
├── 📄 rl_env.py          → Ambiente e redes neurais (Simulador)
├── 📄 train_a2c.py       → Semana 1 • A2C para debug e validação
├── 📄 train_ppo.py       → Semana 2 e 3 • PPO e PPO+GAE (PPO+GEN)
├── 📄 app.py             → Sandbox / utilidades / carregamento de dados
├── 📄 README.md          → Documentação principal
└── 📄 requirements.txt   → Dependências
```

---

# 🧩 Visão Geral do Fluxo

Abaixo estão os locais destinados às **três imagens** explicando o fluxo completo do projeto.

---

## 🖼️ 1️⃣ Diagrama Geral do Pipeline (Treino + Teste)


![DiagramaCompleto](https://raw.githubusercontent.com/LazuliOO2/Python/main/portfolioDeAtivos/src/completodiagrama.svg)

---

## 🖼️ 2️⃣ Fluxo do A2C (Semana 1 – Debug)

![A2C](https://raw.githubusercontent.com/LazuliOO2/Python/main/portfolioDeAtivos/src/a2c.png)

---

## 🖼️ 3️⃣ Fluxo do Ambiente PortfolioEnv

![PortfolioEnv](https://raw.githubusercontent.com/LazuliOO2/Python/main/portfolioDeAtivos/src/portfolio.svg)

---

# 🛠️ Tecnologias Utilizadas

- Python  
- NumPy & Pandas  
- PyTorch  
- Yahoo Finance API (yfinance)  
- Reinforcement Learning (A2C / PPO / PPO+GAE)  
- Modelos Actor–Critic  
- Normalização e segurança numérica (tratamento de NaNs e infinitos)

---

# ⚙️ Instalação

```sh
git clone <URL_DO_REPOSITORIO>
cd RL-PORTFOLIO
pip install -r requirements.txt
```

---

# 🔍 Explicação dos Arquivos Principais

---

# 🧩 `teste.py` — O “Core”: Dados, estatísticas e pré-processamento

Este arquivo realiza:

### ✔ Download e atualização dos dados históricos (Yahoo Finance)  
- ETFs  
- Ações  
- Ouro  
- Renda Fixa  
- Criptomoedas  

### ✔ Cálculo das features financeiras  
- Retornos diários (`ret`)  
- Volatilidade anualizada (`vol`)  
- Drawdown (`dd`)  
- Clusters setoriais  
- Classificação do regime de mercado  
  - bull, bear, alta_vol, baixa_vol, neutro  

### ✔ Split do dataset em treino / teste  
- 80% treino  
- 20% teste  

### ✔ Construção do vetor de estado  
Função: **`discretizar_estado_financeiro()`**  
Contém:  
- Pesos  
- Retorno médio  
- Volatilidade média  
- Drawdown  
- Cluster do ativo  

### ✔ Função de recompensa  
Função: **`calcular_recompensa_portfolio()`**  
Combina:  
- log-retorno  
- penalidade de drawdown  
- penalidade de turnover  

### ✔ Função de ação  
Função: **`aplicar_acao_portfolio()`**  
Transforma uma ação discreta em um novo vetor de pesos normalizado com limites.

---

# 🕹 `rl_env.py` — O Simulador (Ambiente + Modelos Actor/Critic)

Aqui fica o ambiente estilo **OpenAI Gym**.

## ✔ `PortfolioEnv`
Implementa:

- `reset()`  
- `step()`  
- cálculo do valor da carteira  
- cálculo do drawdown **real**  
- normalização dos pesos  
- construção do estado  

## ✔ Redes neurais

- `PolicyMLP` — **Ator**  
- `ValueMLP` — **Crítico**  

Ambas usando MLP com inicialização Xavier e ReLU.

---

# 🧪 `train_a2c.py` — Semana 1 (A2C) — Debug / Prova de Conceito

Este script serve para garantir que **todo o pipeline funciona**:

- testagem do ambiente  
- validação dos estados  
- validação da política  
- debug de NaNs  
- execução passo a passo  
- atualização Actor–Critic padrão  

O objetivo desta semana é **validar toda a pipeline** antes de evoluir para PPO.

---

# 🎯 `app.py` — Sandbox / Testes manuais

Serve como ambiente para:

- testes rápidos  
- conferência de shapes  
- testes de recompensas  
- verificação de normalização  
- execução da política sem treino  

É o **laboratório** do projeto.

---

# 🚀 `train_ppo.py` — PPO (Semana 2) + PPO+GAE (Semana 3)

Este é o arquivo mais importante — o modelo final.

## 🔵 Semana 2 — PPO puro (sem GAE)
- clipping  
- entropia  
- minibatches  
- múltiplas épocas por rollout  

Primeira forma estável e efetiva de treinamento.

---

## 🟣 Semana 3 — PPO + GAE

Modelo de produção:

- GAE(lambda=0.95)  
- vantagem suave e estável  
- treino fixo em dataset de treino  
- avaliação exclusiva no dataset de teste  
- métricas:  
  - reward total  
  - log-ret puro  

---

# 🧠 Diferenças entre os modelos

## 1️⃣ A2C — Simples e frágil
✔ Fácil para debug  
✔ Rápido  
✘ Alta variância  
✘ Instável  

---

## 2️⃣ PPO — Padrão moderno estável
✔ Clipping evita explosões  
✔ Treina com minibatches  
✔ Melhora sobre A2C  
✘ Ainda sofre sem GAE  

---

## 3️⃣ PPO + GAE — Modelo final
✔ Suaviza vantagens  
✔ Melhor estabilidade  
✔ Melhor generalização  
✔ Melhor convergência  

---

# 🚀 Execução

## Semana 1 — A2C
```sh
python train_a2c.py
```

## Semana 2 — PPO
```sh
python train_ppo.py
```

## Semana 3 — PPO + GAE
Mesmo arquivo:
```sh
python train_ppo.py
```

---

# 📚 Funcionalidades

- Simulador realista  
- Penalidade de drawdown  
- Custo de transação  
- Ações discretas  
- Generalização  
- GAE  
- PPO estável  
- Seed fixa (reprodutibilidade)  

---

# 🤝 Contribuição

Melhorias são bem-vindas: novas recompensas, novos ambientes, novas redes neurais etc.

---

