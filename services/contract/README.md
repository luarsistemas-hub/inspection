# Contract service

The service is organized by vertical slices. A slice owns its HTTP handler,
request and response types, validation, business flow, persistence adapter and
tests.

```
internal/
  features/
    contracts/
      create/                         # POST /create-contract
        setup.go                       # única API pública da feature
        request.go                     # DTO HTTP de entrada
        response.go                    # DTOs HTTP de saída
        handler.go                     # tradução HTTP ↔ caso de uso
        handler_test.go
        internal/                      # Clean Architecture privada da feature
          usecase.go                   # orquestração da criação
          entity.go                    # entidade Contract e invariantes
          repository.go                # porta de persistência + adaptador GORM
  platform/
    config/        # process-level configuration
```

`cmd/contract-service/main.go` is the composition root: it creates the
database and HTTP router, then calls `create.Setup`. New contract operations
should be added beside `create`, rather than added to shared controller,
use-case or repository packages. A repository is only shared after a concrete
need emerges across multiple features.
