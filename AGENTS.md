# Guia do repositório

## Visão geral

Este repositório é um monorepo Go para o projeto `inspection`. Hoje ele contém
um único módulo (`module inspection`) e um único serviço executável, o Contract
Service. O workspace e o módulo usam Go 1.26.5.

O desenho adotado para serviços é Vertical Slice Architecture: cada operação de
negócio deve manter, na mesma feature, sua borda HTTP, fluxo de aplicação,
domínio, persistência e testes. Evite recriar camadas globais de controllers,
use cases ou repositories.

Estado atual relevante:

- `POST /create-contract` é a única operação de negócio implementada.
- PostgreSQL é a única dependência de infraestrutura usada pelo código.
- Dragonfly (compatível com Redis) e MinIO existem no Docker Compose, mas ainda
  não possuem integração com a aplicação.
- `photos` existe no request e no OpenAPI, porém `request.toInput` atualmente o
  descarta; não suponha que fotos são persistidas.
- Não há Makefile, task runner ou pipeline de CI versionado. Use os comandos Go
  diretamente a partir da raiz.

## Estrutura

```text
.
├── go.mod / go.work             # módulo e workspace Go da raiz
├── libs/
│   └── identity/                # IDs UUID compartilhados
├── services/
│   └── contract/
│       ├── cmd/contract-service/ # composition root e main
│       ├── docs/                 # especificação OpenAPI servida pela aplicação
│       └── internal/
│           ├── features/         # slices organizados por domínio/operação
│           └── platform/         # configuração e preocupações do processo
├── deploy/docker-compose.yml    # PostgreSQL, Dragonfly e MinIO locais
└── .compozy/                    # metadados, agentes e extensões do Compozy
```

Arquivos executáveis chamados `contract-service` são artefatos de build, não
fonte. Não os edite e não crie novos binários dentro do repositório; prefira
`go build ./...` ou uma saída explícita em `/tmp`.

## Arquitetura dos serviços

O exemplo canônico é
`services/contract/internal/features/contracts/create`:

- `setup.go` é a única API pública do slice. `Setup` recebe o router e as
  dependências externas, monta repository, use case e handler e registra a rota.
- `request.go` e `response.go` contêm somente DTOs e conversões da borda HTTP.
- `handler.go` traduz HTTP para o caso de uso e o resultado para HTTP. Não coloque
  regras de negócio no handler.
- `internal/usecase.go` coordena o fluxo e define input/output independentes de
  HTTP.
- `internal/entity.go` concentra entidade e invariantes de domínio.
- `internal/repository.go` define a porta mínima exigida pelo caso de uso e seu
  adapter GORM. A interface fica próxima do consumidor.
- `handler_test.go` testa o slice pela rota usando `httptest` e um repository
  stub, sem banco real.

Ao adicionar uma operação de contratos, crie um diretório irmão de `create`
(`contracts/<operacao>`), em vez de aumentar o slice existente ou criar pacotes
globais por camada. O composition root em `cmd/<servico>/main.go` deve apenas
montar dependências, middleware, rotas e ciclo de vida do processo.

Regras de dependência:

- Não importe `internal` de outro serviço.
- Mantenha detalhes HTTP fora do domínio e do use case.
- Propague `context.Context` do request até I/O; repositories GORM devem usar
  `WithContext`.
- Compartilhe código em `libs/` somente quando houver reutilização concreta e
  sem dependência de um serviço. Não extraia abstrações preventivamente.
- Use `libs/identity.NewID` e `libs/identity.ParseID` para UUIDs do projeto.
- Preserve compatibilidade com os nomes de coluna GORM existentes
  (`contractid`, `accountid`, `clientid`, `tenantid`, `productid`) ao evoluir o
  schema.

## Fluxo do Contract Service

`services/contract/cmd/contract-service/main.go`:

1. carrega `.env` da raiz;
2. conecta ao database de manutenção do PostgreSQL e cria `DB_NAME` se faltar;
3. abre o banco da aplicação com GORM;
4. configura Chi e seus middlewares;
5. chama `create.Setup`, que executa `AutoMigrate` e registra a rota;
6. publica Swagger em `/docs/` e inicia o servidor.

O usuário do banco precisa conseguir criar databases. Erros de validação do
caso de uso são marcados com `ValidationError` e viram HTTP 400; falhas internas
devem ser encapsuladas com `%w`, não expostas ao cliente, e viram HTTP 500.

## Ambiente local

Suba pelo menos o PostgreSQL:

```sh
docker compose -f deploy/docker-compose.yml up -d postgres
```

Para subir toda a infraestrutura disponível:

```sh
docker compose -f deploy/docker-compose.yml up -d
```

Crie `.env` na raiz. Mesmo com variáveis de ambiente, `config.Load(".")` chama
`ReadInConfig`, portanto o arquivo precisa existir no estado atual.

```dotenv
DB_DRIVER=postgres
DB_HOST=localhost
DB_PORT=5432
DB_USER=postgres
DB_PASSWORD=postgres
DB_NAME=contract
WEB_SERVER_PORT=8000
```

Esses valores correspondem ao PostgreSQL do Compose. Não versione `.env` nem
segredos. Execute o serviço a partir da raiz para que o caminho do `.env` seja
resolvido corretamente:

```sh
go run ./services/contract/cmd/contract-service
```

Com a porta acima, a API fica em `http://localhost:8000` e a interface Swagger
em `http://localhost:8000/docs/index.html`.

## Implementação e estilo

- Formate todo arquivo Go alterado com `gofmt`.
- Siga a organização padrão de imports do Go: standard library, imports do
  módulo `inspection`, depois dependências externas.
- Use lower camel case nos campos JSON (`accountId`, `contractId`).
- Faça validação de formato/conversão no caso de uso e invariantes na entidade.
- Mantenha respostas de erro externas estáveis e genéricas para falhas internas.
- Documente símbolos exportados e mantenha arquivos pequenos, com uma
  responsabilidade clara.
- Não faça refatorações amplas de código legado como efeito colateral de uma
  feature.

Os três artefatos em `services/contract/docs/` (`docs.go`, `swagger.json` e
`swagger.yaml`) descrevem a mesma API e não há comando de geração versionado.
Quando o contrato HTTP mudar, mantenha os três sincronizados e confirme que o
documento servido por `docs.go` reflete a implementação.

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
