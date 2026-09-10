# Guia do repositório

## Visão geral

Este repositório é um monorepo para o produto `inspection`: API, worker, scheduler, migrador e frontend. O módulo Go usa 1.26.5 e a aplicação web usa Node 22.

O desenho adotado para serviços é Vertical Slice Architecture: cada operação de
negócio deve manter, na mesma feature, sua borda HTTP, fluxo de aplicação,
domínio, persistência e testes. Evite recriar camadas globais de controllers,
use cases ou repositories.

Estado atual relevante:

- A API é GraphQL em `services/inspection/schema.graphqls`.
- PostgreSQL, RabbitMQ, Dragonfly, MinIO, Keycloak, Mailpit, LiteLLM e Gotenberg são compostos em `deploy/docker-compose.yml`.
- O legado Contract Service foi removido do estado atual; não adicione código a ele.

## Estrutura

```text
.
├── go.mod / go.work             # módulo e workspace Go da raiz
├── libs/
│   └── identity/                # IDs UUID compartilhados
├── services/inspection/          # API, worker, scheduler, migrations e slices
├── apps/admin/                   # console administrativo
├── apps/dashboard/               # operações e experiência do cliente
├── apps/capture/                 # captura PWA link-scoped
├── internal/                     # artefatos GraphQL legados, sem import pelo serviço
├── deploy/docker-compose.yml    # PostgreSQL, Dragonfly e MinIO locais
└── .compozy/                    # metadados, agentes e extensões do Compozy
```

Não edite binários gerados nem crie executáveis dentro do repositório; prefira
`go build ./...` ou uma saída explícita em `/tmp`.

## Arquitetura dos serviços

O padrão de cada slice está em `services/inspection/internal/features`:

- `setup.go` é a única API pública do slice. `Setup` recebe o router e as
  dependências externas, monta repository, use case e handler e registra a rota.
- `setup.go` é a entrada pública e registra o comando/query no mediator.
- Tipos de input/output, invariantes e adapter ficam próximos da operação.
- Testes exercitam o setup com stubs e também cobrem adapters de plataforma.

Ao adicionar uma operação, crie um diretório irmão no domínio correspondente,
sem criar camadas globais de controllers, use cases ou repositories. O
composition root deve somente montar dependências e ciclo de vida.

Regras de dependência:

- Não importe `internal` de outro serviço.
- Mantenha detalhes HTTP fora do domínio e do use case.
- Propague `context.Context` do request até I/O; repositories GORM devem usar
  `WithContext`.
- Compartilhe código em `libs/` somente quando houver reutilização concreta e
  sem dependência de um serviço. Não extraia abstrações preventivamente.
- Use `libs/identity.NewID` e `libs/identity.ParseID` para UUIDs do projeto.
- Preserve nomes de tabela/coluna existentes ao evoluir migrations.

## Fluxo do Inspection

Os comandos em `services/inspection/cmd/`:

1. carregam ambiente validado;
2. abrem PostgreSQL com a role apropriada;
3. montam slices e adapters;
4. iniciam GraphQL, consumer/dispatcher ou scheduler;
5. encerram com shutdown limitado.

O migrador usa conexão privilegiada; API, scheduler e worker usam roles de
runtime distintas. Erros de validação viram `userErrors`; falhas internas não
expõem detalhes ao cliente.

## Ambiente local

O fluxo oficial é:

```sh
./scripts/local.sh init
./scripts/local.sh infra
```

Para subir toda a stack:

```sh
./scripts/local.sh up
```

`.env.inspection` é ignorado pelo Git. O Go recebe ambiente exportado pelos
scripts; não há leitura automática de arquivos `.env`.

## Implementação e estilo

- Formate todo arquivo Go alterado com `gofmt`.
- Siga a organização padrão de imports do Go: standard library, imports do
  módulo `inspection`, depois dependências externas.
- Use lower camel case nos campos GraphQL/JSON (`tenantId`, `businessUnitId`, `clientMutationId`).
- Faça validação de formato/conversão no caso de uso e invariantes na entidade.
- Mantenha respostas de erro externas estáveis e genéricas para falhas internas.
- Documente símbolos exportados e mantenha arquivos pequenos, com uma
  responsabilidade clara.
- Não faça refatorações amplas de código legado como efeito colateral de uma
  feature.

O schema GraphQL é a referência canônica. Arquivos `generated.go`, `models_gen.go` e `src/graphql/generated.ts` devem ser regenerados e verificados pela CI.

## Testes e verificação

Execute, a partir da raiz:

```sh
go test ./...
go vet ./...
go build ./...
```

Antes de concluir uma alteração Go, rode no mínimo `gofmt` nos arquivos tocados
e `go test ./...`. Para novos slices, cubra pelo menos caminho feliz, entrada
inválida, falha de dependência e validação das dependências de `Setup`. Prefira
stubs/fakes pequenos definidos no próprio teste; adicione teste de integração
somente quando o comportamento depender de PostgreSQL ou outro adapter real.

## Orientações para agentes

- Leia primeiro este arquivo, o `README.md` do serviço afetado e os slices
  vizinhos antes de propor uma estrutura nova.
- Trate `.compozy/memory/` como histórico auxiliar, não como fonte de verdade
  sobre o código atual.
- Não altere `.compozy/` salvo quando a tarefa for especificamente sobre o
  workflow do Compozy.
- `RTK.md` é uma referência opcional: carregue-o somente quando o usuário pedir
  explicitamente para usar RTK.
- Preserve mudanças existentes do usuário e mantenha o escopo da tarefa.
