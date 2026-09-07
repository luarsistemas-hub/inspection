# Scripts

`local.sh` é o ponto de entrada Docker e `dev.sh` inicia processos no host. Ambos resolvem a raiz pelo próprio caminho e carregam `.env.inspection` sem executar conteúdo.

```sh
./scripts/local.sh init|up|infra|migrate|status|logs [serviço]|down
./scripts/dev.sh api|worker|scheduler|web
```

`verify.sh`, `smoke.sh`, `security-smoke.sh`, `load-smoke.sh` e `validate-compozy-tasks.sh` continuam disponíveis para CI e diagnóstico. Os scripts de startup não removem volumes nem criam binários no repositório.
