# Scripts

`local.sh` é o ponto de entrada Docker e `dev.sh` inicia processos no host. Ambos resolvem a raiz pelo próprio caminho e carregam `.env.inspection` sem executar conteúdo. O `seed` roda como job one-shot no Compose e inicia uma API containerizada temporária quando necessário.

```sh
./scripts/local.sh init|up|infra|migrate|status|logs [serviço]|down
./scripts/dev.sh api|worker|scheduler|admin|dashboard|capture|all
```

`verify.sh`, `smoke.sh`, `security-smoke.sh`, `load-smoke.sh` e `validate-compozy-tasks.sh` continuam disponíveis para CI e diagnóstico. `parity-gate.sh` sobe uma stack isolada, executa os testes autenticados, gera evidência redigida e valida o inventário legado antes de desmontar os volumes. Os scripts de startup não removem volumes nem criam binários no repositório.

O inventário pode ser revisado sem subir a stack:

```sh
node scripts/lib/legacy-inventory.mjs generate --legacy-root apps/web --output /tmp/legacy-inventory.json
node scripts/lib/legacy-inventory.mjs validate --input /tmp/legacy-inventory.json --baseline docs/legacy-inventory.json --fail-on-unclassified --fail-on-drift
```
