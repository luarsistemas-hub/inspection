# Scripts

`local.sh` é o ponto de entrada Docker e `dev.sh` inicia processos no host. Ambos resolvem a raiz pelo próprio caminho e carregam `.env.inspection` sem executar conteúdo. O `seed` roda como job one-shot no Compose e inicia uma API containerizada temporária quando necessário.

`dev.sh all` inicia a API, o worker, o scheduler e os quatro frontends no host. Antes de iniciar os frontends, aguarda a API responder em `/readyz`; assim, o navegador não abre o onboarding sem o backend disponível. Se a API já estiver rodando com `dev.sh api`, `all` reutiliza essa API e inicia os demais processos. Use os comandos individuais quando quiser trabalhar somente em um processo.

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
