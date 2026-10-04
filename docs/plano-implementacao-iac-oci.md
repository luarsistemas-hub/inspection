# Plano de implementação da IaC e operação OCI

Este documento registra as decisões e o desenho implementados em `deploy/oci/`. O runbook operacional canônico, com comandos, entradas e recuperação, é [`deploy/oci/readme.md`](../deploy/oci/readme.md). O plano de referência original continua em [`plano-infra-oci-always-free.md`](plano-infra-oci-always-free.md); este registro atualiza as partes que eram propostas futuras.

## Escopo e decisões

- Tenancy OCI com compartment dedicado `inspection`.
- Perfil restrito a Ubuntu 24.04 ARM64 A1, 2 OCPUs, 12 GB, boot de 50 GB e volume persistente ext4 de 150 GB. Terraform falha se esses tamanhos forem alterados.
- Terraform 1.5.7 no Resource Manager; pilhas `foundation` e `runtime`, ZIPs distintos, lockfiles do provider, Plan revisado antes de cada Apply.
- GitHub Actions produz imagens ARM64 privadas no GHCR e artefatos imutáveis por commit/digest. DNS público e credenciais externas são configuração operacional manual.
- Compose OCI não contém MinIO, Mailpit, stubs, fixtures ou seeds de QA. LiteLLM e canais de SMS/WhatsApp ficam desativados; submissões que iniciariam análise ou canal desativado falham com `INTEGRATION_DISABLED` antes de persistência ou trabalho assíncrono.
- O bucket segue privado. URLs S3 assinadas para OCI recebem uma origem pública `storage.<domínio>`; Caddy encaminha ao único endpoint regional configurado, restaura Host/SNI OCI, restringe métodos/bucket e implementa CORS apenas para origens aprovadas, incluindo exposição de `ETag`.
- A VM é única e esta fase não tem backup. Não armazenar dados cuja perda seja inaceitável. Falta de capacidade A1 exige aguardar ou reavaliar o plano com orçamento aprovado; nunca há fallback pago automático.

## Responsabilidade das pilhas

| Pilha | Recursos |
|---|---|
| `foundation` | Compartment, Vault compartilhado, chave AES protegida por software, bucket Standard privado sem versionamento, usuário/grupo IAM de S3 limitado ao bucket, dynamic group da VM, políticas de acesso à chave/métricas, tópico e subscription de alertas, registro do endereço aprovado de Email Delivery. SPF/DKIM, aprovação do remetente e credenciais SMTP são concluídos fora do Terraform; nenhuma senha fica no estado. |
| `runtime` | VCN `10.20.0.0/16`, subnet pública `10.20.1.0/24`, Internet Gateway e rota de saída, security list sem entrada ampla, NSG com TCP 443 e SSH somente do CIDR administrativo, instância A1, boot/data volume, attachment, política Instance Principal restrita aos OCIDs de secrets declarados e alarmes de CPU/memória/disco/bucket/heartbeat. |

## Operação no host

`deploy/oci/ops.sh` é o ponto operacional para validar, empacotar, fazer preflight, bootstrap, sincronizar secrets, implantar e reverter releases por SHA, consultar status/logs, reiniciar serviços, executar smoke checks e parar containers sem remover dados. `host/` instala Docker/Compose e OCI CLI em versões controladas; valida o device e só formata volume comprovadamente vazio com consentimento explícito. O refresh de segredos usa Instance Principal, cria configurações de serviço protegidas e substitui arquivos atomicamente.

O Compose usa referências de imagem por digest, rede interna para dados, healthchecks, limites de memória e volume persistente para PostgreSQL 18, RabbitMQ, Dragonfly, banco Keycloak e estado ACME. O deploy serializa releases, efetua pull autenticado por tempo curto, executa migrador e inicialização idempotente de prompt antes dos serviços e termina com smoke. Não existe downgrade automático de banco.

## Verificação e limites da entrega

Validações locais cobrem Terraform sem backend, lockfiles, scripts, sintaxe e resolução do Compose, testes/lint/build Go existentes e configuração dos frontends. Testes locais exercitam as políticas desativadas e a assinatura S3 com origem pública. Apply, DNS, envio SMTP real, disponibilidade A1, smoke em tenancy e comprovação operacional após reboot só podem ser concluídos com os OCIDs, domínio, credenciais, quotas e acesso a uma tenancy real. Até essa execução, a implementação está preparada, mas o ambiente não está declarado implantado.
