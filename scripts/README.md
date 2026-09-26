# Scripts

`local.sh` é o ponto de entrada Docker e `dev.sh` inicia processos no host. Ambos resolvem a raiz pelo próprio caminho e carregam `.env.inspection` sem executar conteúdo. O `seed` roda como job one-shot no Compose e inicia uma API containerizada temporária quando necessário.

`dev.sh all` inicia a API, o worker, o scheduler e os quatro frontends no host. Antes de iniciar os frontends, aguarda a API responder em `/readyz`; assim, o navegador não abre o onboarding sem o backend disponível. Se a API já estiver rodando com `dev.sh api`, `all` reutiliza essa API e inicia os demais processos. Use os comandos individuais quando quiser trabalhar somente em um processo.

```sh
./scripts/local.sh init|up|infra|migrate|status|logs [serviço]|down
./scripts/dev.sh api|worker|scheduler|admin|dashboard|capture|all
```

`verify.sh`, `smoke.sh`, `security-smoke.sh`, `load-smoke.sh` e `validate-compozy-tasks.sh` continuam disponíveis para CI e diagnóstico. `parity-gate.sh` sobe uma stack isolada, executa os testes autenticados, gera evidência redigida e valida o inventário legado antes de desmontar os volumes. Os scripts de startup não removem volumes nem criam binários no repositório.

Após substituir o prompt de análise em um ambiente local, aplique explicitamente o padrão novo com `docker compose -f deploy/docker-compose.yml run --rm inspection-prompt-seed --replace`. O seed sem `--replace` permanece idempotente e não sobrescreve edições administrativas.

Para comparar duas versões do prompt ou dois fluxos de imagem com um manifesto de casos rotulados, use `node scripts/analysis-eval.mjs --live --manifest scripts/analysis-eval.example.json --baseline prompt-a.txt --candidate prompt-b.txt --endpoint http://localhost:4000`. Cada caso deve informar `requirement`, `confidenceThreshold`, `expectedNoRelevantChange` e tags `scenarios`; as imagens usam `evidenceId`, `role` e data URL. Para comparar originais e comprimidas, informe `baselineImages` e `candidateImages` com as mesmas evidências nas duas configurações, dimensões `width`/`height` e, no derivado, dimensões originais `sourceWidth`/`sourceHeight`. O comando executa três repetições por configuração alternando a ordem, mede findings, `noRelevantChange`, bytes, tokens, cache, custo informado e latência mediana/p95. O resultado indica cenários obrigatórios ausentes e calcula o gate de qualidade: prompt/modelo iguais, perdas HIGH/CRITICAL, precisão, recall, `noRelevantChange` e redução de bytes. Use `--repetitions` para alterar o número de repetições e `--baseline-model`/`--candidate-model` para avaliar modelos sem alterar o padrão de produção. A flag `--live` é obrigatória para deixar explícito que haverá chamadas reais ao modelo.

O padrão `--cache-mode implicit` mede os acertos reportados pelo Gemini. `--cache-mode explicit` mede o prompt fixo pelo tokenizador do provedor, só marca cache se o prefixo atingir o mínimo documentado para o modelo, e envia `cache_control` com TTL de 600 segundos. Use `--cache-mode both` para comparar as duas estratégias na mesma amostra, incluindo o custo informado pelo gateway. Tarifas de leitura, gravação e armazenamento podem ser informadas com `--cache-read-usd-per-million-tokens`, `--cache-write-usd-per-million-tokens` e `--cache-storage-usd-per-million-token-seconds`; sem elas, o avaliador apresenta tokens e deixa a estimativa de cobrança indisponível.

O inventário pode ser revisado sem subir a stack:

```sh
node scripts/lib/legacy-inventory.mjs generate --legacy-root apps/web --output /tmp/legacy-inventory.json
node scripts/lib/legacy-inventory.mjs validate --input /tmp/legacy-inventory.json --baseline docs/legacy-inventory.json --fail-on-unclassified --fail-on-drift
```
