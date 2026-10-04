# Inspection na OCI: manual de operação

Este é o ponto de entrada operacional. Use-o para provisionar, publicar versões, diagnosticar, recuperar e parar o ambiente.

## Arquitetura e limites

```text
Browser ── HTTPS/443 ── Caddy ── Admin, Dashboard, Capture, Onboarding, API, Keycloak
                             └── storage proxy ── OCI regional S3-compatible endpoint
Private Docker network: PostgreSQL, RabbitMQ, Dragonfly, API, worker, scheduler, Keycloak
Persistent OCI block volume: database, queues, cache, Keycloak database, certificates
OCI: Resource Manager, Vault, private Object Storage bucket, Email Delivery, Monitoring/Notifications
```

A VCN de runtime usa `10.20.0.0/16`; a subnet pública usa `10.20.1.0/24`. A VM expõe TCP 443 e TCP 22 restrito ao CIDR administrativo. Banco, broker, cache, API administrativa do Keycloak e portas de containers não têm listeners públicos. O hostname de storage encaminha somente o bucket configurado, com CORS limitado às quatro origens web e `ETag` exposto ao navegador.

A implantação usa uma VM sem failover. O desenho não inclui backup nem troca automática por recursos pagos. Armazene somente dados cuja perda seja aceitável. As quotas Always Free são compartilhadas pela tenancy, a disponibilidade depende da região principal e o nível Always Free não garante custo zero. Antes do primeiro provisionamento ou de qualquer redimensionamento, revise **Governance & Administration → Limits, Quotas and Usage**. Se faltar capacidade A1, aguarde ou reavalie o plano; não troque por uma shape paga sem orçamento aprovado.

## Pré-requisitos e configuração

Instale Terraform **1.5.7**, OCI Resource Manager, OCI CLI **3.94.1**, Docker Engine **29.8.2**, Buildx **0.37.1**, Compose **5.6.0**, containerd **2.3.6**, Git, `zip`, `ssh`, `python3` e `gh`. O bootstrap fixa e verifica essas versões de pacotes Ubuntu ARM64; revise `deploy/oci/host/install-host.sh` antes de alterar as versões. As definições de stack do Resource Manager usam Terraform 1.5.7. A OCI CLI da VM usa Instance Principals; a CLI do operador pode usar um perfil local com API key. Mantenha a chave SSH privada fora deste repositório.

Comandos `deploy/oci/ops.sh` são executados na estação do operador, a partir da raiz do repositório. Comandos `sudo` ou `docker compose`, após conexão SSH, são executados na VM. Workflow e variáveis do repositório são configurados no GitHub Actions/Settings; quotas, Plan/Apply das stacks, anexação do volume e alterações de DNS/e-mail são feitos no Console OCI. Nunca passe segredos como argumentos de comandos.

Copie [production.env.example](production.env.example) para `/etc/inspection/public.env` na VM, substitua todos os exemplos e proteja o arquivo com `root:root` e modo `0600`. Use a região principal, nomes DNS públicos, hostname compatível com OCI S3, bucket, namespace, OCID do compartment e subject inicial corretos. Os sete hostnames precisam resolver publicamente antes de o proxy solicitar certificados.

| Entrada | Segredo? | Onde definir |
|---|---|---|
| Tenancy, compartment, home region, AD and Ubuntu ARM64 image OCID | No | Resource Manager stack variables |
| Administrative IPv4 CIDR and SSH public key | No | Runtime stack variables |
| Domain, seven hostnames/origins, OCI S3 endpoint, namespace and bucket | No | `public.env`; matching public build variables in GitHub |
| OCIs for OCI Vault secrets | No | Root-owned `/etc/inspection/secrets.map`; never values in Terraform |
| Database, Keycloak, Turnstile, S3, SMTP, OTP, metrics, payload encryption and GHCR credentials | Yes | Individual OCI Vault secrets |
| Image references in `repository@sha256:digest` form | No | Release manifest in `deploy/oci/releases/<40-char-SHA>.env` |

Use a tabela abaixo como inventário completo de entradas. Não há valores padrão de produção para domínio, região, CIDRs, OCIDs, destinatários, segredos ou imagens publicadas; substitua todos os exemplos. As variáveis do Resource Manager ficam armazenadas na OCI; informe ali somente identificadores não secretos e dimensionamento.

| Variável | Obrigatória / padrão | Exemplo não secreto | Definida em |
|---|---|---|---|
| `tenancy_ocid`, `region` | Required; no default | `ocid1.tenancy...`, `us-ashburn-1` | Both Resource Manager stacks |
| `compartment_name`, `bucket_name` | `inspection`, `inspection-private` | `inspection-private` | Foundation stack |
| `alert_email` | Required; no default | `ops@example.com` | Foundation stack; confirm topic subscription |
| `email_sender_address` | Required; no default | `no-reply@example.com` | Foundation stack; confirm sender approval and domain SPF/DKIM |
| `compartment_ocid`, `availability_domain`, `image_ocid`, `namespace`, `alert_topic_ocid` | Required; no default | OCI output values | Runtime stack, from foundation/OCI Console |
| `admin_cidr`, `ssh_public_key` | Required; no default | `198.51.100.24/32` | Runtime stack |
| `instance_name` | `inspection-runtime` | `inspection-runtime` | Runtime stack |
| `vcn_cidr`, `subnet_cidr` | `10.20.0.0/16`, `10.20.1.0/24` | Same | Runtime stack; change only with reviewed network plan |
| `shape_ocpus`, `shape_memory_gb`, `boot_volume_gb`, `data_volume_gb` | Fixed at `2`, `12`, `50`, `150` | Same | Runtime stack; validation rejects different sizes |
| `INSPECTION_COMPARTMENT_OCID`, `OCI_REGION` | Required; no default | OCI runtime output, `us-ashburn-1` | `/etc/inspection/public.env` |
| `ADMIN_HOST`, `DASHBOARD_HOST`, `CAPTURE_HOST`, `ONBOARDING_HOST`, `API_HOST`, `AUTH_HOST`, `STORAGE_HOST` | Required; no default | `admin.example.com` ... `storage.example.com` | `/etc/inspection/public.env`; matching DNS A records |
| `ADMIN_ORIGIN`, `DASHBOARD_ORIGIN`, `CAPTURE_ORIGIN`, `ONBOARDING_ORIGIN`, `API_ORIGIN`, `AUTH_ORIGIN`, `STORAGE_ORIGIN` | Required; no default | `https://admin.example.com` | `/etc/inspection/public.env`; GitHub variables below |
| `OIDC_ISSUER`, `OIDC_JWKS_URL` | Required; no default | `https://auth.example.com/realms/inspection` | `/etc/inspection/public.env` |
| `INSPECTION_ALLOWED_ORIGINS` | Required; no default | comma-separated four app origins | `/etc/inspection/public.env` |
| `OCI_S3_HOST`, `OCI_S3_ENDPOINT`, `OCI_S3_BUCKET`, `INSPECTION_NAMESPACE` | Required; no default | `namespace.compat.objectstorage.us-ashburn-1.oraclecloud.com` | `/etc/inspection/public.env` |
| `SUPER_ADMIN_SUBJECT`, `TURNSTILE_SITE_KEY`, `SMTP_ADDRESS`, `SMTP_FROM`, `RABBITMQ_USER` | Required; no default | approved user UUID, public site key, regional SMTP endpoint | `/etc/inspection/public.env` |
| GitHub `OCI_ADMIN_ORIGIN`, `OCI_DASHBOARD_ORIGIN`, `OCI_CAPTURE_ORIGIN`, `OCI_ONBOARDING_ORIGIN`, `OCI_API_ORIGIN`, `OCI_AUTH_ORIGIN`, `OCI_STORAGE_ORIGIN`, `OCI_TURNSTILE_SITE_KEY` | Required before manual workflow; no defaults | Same public HTTPS URLs/key | GitHub repository Variables |
| `POSTGRES_ADMIN_PASSWORD`, `INSPECTION_RUNTIME_PASSWORD`, `INSPECTION_WORKER_PASSWORD`, `KEYCLOAK_DB_PASSWORD`, `KEYCLOAK_BOOTSTRAP_USERNAME`, `KEYCLOAK_BOOTSTRAP_PASSWORD`, `KEYCLOAK_PROVISIONING_SECRET` | Required; no defaults | Generate unique random hex passwords; username such as `bootstrap-admin` | Individual OCI Vault secrets named in `secrets.map` |
| `RABBITMQ_PASSWORD`, `DRAGONFLY_PASSWORD`, `TURNSTILE_SECRET`, `SUPER_ADMIN_PASSWORD`, `INSPECTION_METRICS_TOKEN`, `INSPECTION_OTP_PEPPER`, `NOTIFICATION_ACTIVE_PAYLOAD_KEY`, `NOTIFICATION_PAYLOAD_KEYS` | Required; no defaults | Key ID and JSON/base64 key ring as described above | Individual OCI Vault secrets |

Segredos no Vault devem ter uma única linha e não podem conter aspas simples (`'`); o `secrets-refresh` rejeita esses valores. Senhas usadas em DSNs e na URL do RabbitMQ são codificadas (URL-encoding) automaticamente.
| `OCI_S3_ACCESS_KEY`, `OCI_S3_SECRET_KEY`, `SMTP_USERNAME`, `SMTP_PASSWORD`, `GHCR_USERNAME`, `GHCR_READ_TOKEN` | Required; no defaults | OCI Customer Secret Key, OCI SMTP credentials, GHCR read-only PAT | Individual OCI Vault secrets |
| `API_IMAGE`, `KEYCLOAK_IMAGE`, `ADMIN_IMAGE`, `DASHBOARD_IMAGE`, `CAPTURE_IMAGE`, `ONBOARDING_IMAGE`, `POSTGRES_IMAGE`, `DRAGONFLY_IMAGE`, `RABBITMQ_IMAGE`, `CADDY_IMAGE`, `RELEASE_SHA` | Required for deploy; no defaults | Registry `@sha256:<64-hex>` references and commit SHA | Generated release manifest from GitHub Actions artifact |

Gere cada senha como uma longa sequência hexadecimal aleatória para que possa compor uma URL de conexão PostgreSQL com segurança. Gere `INSPECTION_OTP_PEPPER` com ao menos 32 bytes aleatórios representados em hexadecimal. Crie `NOTIFICATION_PAYLOAD_KEYS` como um mapa JSON de IDs de versão para chaves AES de 32 bytes codificadas em base64, e defina `NOTIFICATION_ACTIVE_PAYLOAD_KEY` como um desses IDs. Mantenha IDs antigos enquanto existirem entregas criptografadas que os referenciem. Em `secrets.map`, registre somente os OCIDs dos segredos no OCI Vault, seguindo [secrets.map.example](secrets.map.example). Defina proprietário root e modo `0600`.

O usuário OCI S3 é criado pelo Terraform, mas sua Customer Secret Key deve ser criada separadamente em OCI Console → Identity & Security → Users. Copie a chave uma única vez para o Vault; a OCI não a exibirá novamente. Crie credenciais SMTP em Email Delivery, aprove o remetente e publique os registros SPF/DKIM do domínio. Confirme a inscrição de e-mail criada para o tópico de alertas.

## Empacotar, provisionar e configurar DNS

Na raiz do repositório, execute `deploy/oci/ops.sh validate` e `deploy/oci/ops.sh package`. O empacotamento cria `foundation.zip`, `runtime.zip` e o arquivo de implantação do host em `/tmp/inspection-oci-packages`; arquivos de estado local e variáveis são excluídos. `validate` exige Terraform 1.5.7 e executa `fmt`, `init -backend=false` e `validate` nas duas raízes. Também confirme o schema e o Plan no OCI Resource Manager antes de Apply.

Antes de criar as stacks, confira disponibilidade A1, quotas Always Free regionais, franquia combinada de 200 GB para boot/volume de bloco, franquia de armazenamento, controle do domínio e acesso aos pacotes do GitHub. Crie `foundation` no OCI Resource Manager usando seu ZIP e Terraform 1.5.7. Revise o Plan; confirme que cria apenas o compartment do projeto, Vault/chave, bucket privado, IAM restrito, tópico e inscrição de e-mail; então execute Apply. Registre os outputs não secretos.

Após concluir `foundation`, confirme a inscrição de alertas e a aprovação do remetente. Publique os registros SPF/DKIM do domínio conforme as instruções do OCI Email Delivery. Crie fora do Terraform os segredos listados no Vault e copie somente os OCIDs. Crie `runtime` a partir do ZIP, na mesma região. Informe outputs de foundation, OCID exato da imagem Ubuntu 24.04 ARM64 e AD, chave SSH pública, um CIDR administrativo confiável e um mapa `secret_ocids` com cada nome/OCID de `secrets.map.example`. A política IAM concede à instância acesso somente aos segredos listados. Antes de Apply, revise no Plan listeners públicos, recursos pagos inesperados, substituições destrutivas e tamanho do volume. Os outputs de runtime fornecem o IP. Não aplique se o CIDR estiver mais amplo que o pretendido ou se qualquer recurso exceder as quotas revisadas.

Crie registros A de `admin`, `dashboard`, `capture`, `onboarding`, `api`, `auth` e `storage` apontando ao IP público de runtime. Publique registros SPF/DKIM do OCI Email Delivery conforme documentado pelo serviço. Verifique cada registro de fora da tenancy. Caddy obtém e renova certificados automaticamente por TLS-ALPN na porta 443; requer DNS publicado e volume de dados gravável na VM. [Requisitos de certificados do Caddy](https://caddyserver.com/docs/automatic-https).

## Preparar o host e fazer a primeira implantação

1. Crie os segredos no Vault e prepare localmente `public.env` e `secrets.map`. Nunca cole valores secretos em variáveis do Resource Manager, outputs de stacks, chamados ou histórico do shell. Inclua em `public.env` somente valores não secretos e em `secrets.map` somente OCIDs do Vault.
2. Baixe o bundle de runtime e o manifesto de release de um commit SHA; coloque-os em `deploy/oci/releases/`. Cada referência de imagem deve conter um digest imutável.
3. Antes da primeira formatação, confirme na OCI que o OCID do volume anexado corresponde ao output `data_volume_ocid` e que o volume é novo e vazio. Execute `INSPECTION_RUNTIME_HOST=<public-IP> INSPECTION_FORMAT_EMPTY_DATA_VOLUME=yes deploy/oci/ops.sh bootstrap`. O comando instala os pacotes Docker fixados, instala o bundle e recusa formatar um dispositivo que já tenha sistema de arquivos. Em bootstrap posterior, por exemplo após substituir a VM, omita `INSPECTION_FORMAT_EMPTY_DATA_VOLUME`.
4. Antes da primeira conexão SSH, confira a chave de host da VM pela impressão digital no Console OCI/console serial. Depois de confirmar a impressão digital, registre a chave no `known_hosts` da estação e só então use SSH/SCP com `StrictHostKeyChecking=yes`. Na raiz do repositório, prepare `public.env` a partir do exemplo, substitua todos os valores localmente e instale-o junto com o mapa que contém somente OCIDs, como root (substitua `<public-IP>`):

   ```sh
   cp deploy/oci/production.env.example /tmp/inspection-public.env
   # Edit /tmp/inspection-public.env with the approved non-secret values.
   scp -o StrictHostKeyChecking=yes /tmp/inspection-public.env ubuntu@<public-IP>:/tmp/inspection-public.env
   scp -o StrictHostKeyChecking=yes /local/path/secrets.map ubuntu@<public-IP>:/tmp/inspection-secrets.map
   ssh -o StrictHostKeyChecking=yes ubuntu@<public-IP> 'sudo install -o root -g root -m 0600 /tmp/inspection-public.env /etc/inspection/public.env && sudo install -o root -g root -m 0600 /tmp/inspection-secrets.map /etc/inspection/secrets.map && sudo rm -f /tmp/inspection-public.env /tmp/inspection-secrets.map'
   rm -f /tmp/inspection-public.env
   ```

   A OCI CLI instalada é a versão 3.94.1. Valide o acesso de Instance Principal consultando somente o OCID não secreto de um segredo de teste: `sudo /opt/inspection/oci-cli/bin/oci secrets secret-bundle get --secret-id <vault-secret-ocid> --auth instance_principal --query data.id --raw-output >/dev/null`.
5. Quando os sete registros A resolverem para o IP de runtime, execute `INSPECTION_RUNTIME_HOST=<public-IP> INSPECTION_DOMAIN=<domain> deploy/oci/ops.sh preflight`. Isso confirma DNS e autenticação `gh` disponível na estação. Confira se as credenciais GHCR somente leitura estão no Vault e no mapa de segredos do host, e se o artifact do workflow contém todos os digests. Depois execute `INSPECTION_RUNTIME_HOST=<public-IP> deploy/oci/ops.sh deploy <40-character-commit-SHA>`. A primeira implantação recupera os segredos, gera arquivos protegidos por substituição atômica, baixa imagens, verifica a saúde das dependências, executa o migrador controlado, inicializa o prompt somente se ausente, ativa os serviços e executa smoke tests. Uma migração com falha interrompe os serviços; o schema do banco nunca é revertido automaticamente.
6. O realm de produção importa `inspection-super-admin` com o subject definido em `SUPER_ADMIN_SUBJECT` e a senha guardada em `SUPER_ADMIN_PASSWORD`. Após implantar, entre no Admin com essa identidade e troque a senha inicial. O hostname público `auth` bloqueia `/admin*`, `/realms/master*`, métricas e endpoints de saúde. Só depois de confirmar o acesso dessa conta, remova da VM a conta temporária do realm master com `sudo /usr/local/sbin/inspection-remove-bootstrap-admin`.
7. Após remover a conta temporária, execute novamente `deploy/oci/ops.sh smoke`. Confira HTTPS, OIDC, readiness GraphQL, os quatro apps, envio SMTP para um destinatário de teste controlado e upload/download de objetos usando um registro de teste aprovado. Confirme retomada/cancelamento multipart, acesso assinado de curta duração, negação de acesso anônimo ao bucket privado e `ETag` visível no navegador. Isole objetos e destinatários de teste e remova-os pelo fluxo de retenção normal do produto.

Se DNS ou recuperação de segredos estiver incompleta, não inicie a stack da aplicação. Se a migração falhar, o script para a stack candidata, preserva o ponteiro da versão atual e o volume de dados e restaura a configuração Compose anterior. Inspecione os logs do migrador e corrija avançando; o script nunca faz downgrade do schema.

## Build, release, atualização e rollback

Defina no GitHub Actions as variáveis do repositório com as URLs públicas exatas antes de executar **OCI ARM64 images**: `OCI_ADMIN_ORIGIN`, `OCI_DASHBOARD_ORIGIN`, `OCI_CAPTURE_ORIGIN`, `OCI_ONBOARDING_ORIGIN`, `OCI_API_ORIGIN`, `OCI_AUTH_ORIGIN`, `OCI_STORAGE_ORIGIN` e `OCI_TURNSTILE_SITE_KEY`. O workflow valida o código, compila imagens `linux/arm64` fora da VM, publica no GHCR privado com tag do commit, captura os digests e envia o manifesto de release. Conceda ao host somente o token de leitura necessário e guarde-o no Vault. Armazenamento/tráfego do GHCR e minutos de Actions têm políticas de cobrança próprias; confira os limites da conta.

Baixe o manifesto gerado para `deploy/oci/releases/<SHA>.env`; verifique que cada imagem pertence ao proprietário GHCR esperado e termina em `@sha256:<digest>`. Execute `ops.sh preflight` e então `ops.sh deploy <SHA>`. Não edite um manifesto já implantado. O script serializa as execuções, prepara o bundle, autentica no GHCR somente durante o pull, encerra a sessão, executa o migrador antes de ativar API/worker e verifica readiness. Guarde o manifesto e o bundle da versão anterior.

Para fazer rollback, confira primeiro a faixa de compatibilidade de schema da release e compare-a com a versão atual. Então execute `INSPECTION_ROLLBACK_SCHEMA_APPROVED=yes INSPECTION_RUNTIME_HOST=<public-IP> deploy/oci/ops.sh rollback <previous-SHA>`. A confirmação explícita do operador é necessária porque a release anterior precisa ser compatível com o schema atual. O rollback troca somente as referências das imagens; nunca reverte o PostgreSQL. Se a compatibilidade for desconhecida ou a versão anterior não puder ler os dados migrados, mantenha os serviços parados e corrija avançando.

## Operação diária

```sh
deploy/oci/ops.sh status
deploy/oci/ops.sh smoke
deploy/oci/ops.sh logs inspection-api
deploy/oci/ops.sh logs inspection-worker
deploy/oci/ops.sh restart inspection-api
deploy/oci/ops.sh secrets refresh
```

`status` mostra a saúde do Compose e a unidade systemd de atualização de segredos. `logs` exibe até 200 linhas; Docker gira cada log de container a cada 10 MB e mantém três arquivos. Inspecione localmente e remova dados de tenant antes de compartilhar. Em cada dia de operação, confira `df -h`, `docker system df`, disponibilidade PostgreSQL, tamanho das filas RabbitMQ, saúde do Dragonfly e estado do snapshot local de cinco minutos, validade de certificados, uso do bucket OCI e alarmes do Monitoring. O timer de métricas publica o uso de disco e heartbeat a cada minuto. Snapshots Dragonfly ficam no mesmo volume de dados e não são backups. Remova camadas de imagem sem uso somente depois de confirmar que as releases atual e anterior ainda estão disponíveis. Não execute `docker compose down --volumes`.

Os alarmes Terraform monitoram CPU e memória do host; OCI Notifications envia alertas de limite à inscrição de e-mail confirmada. Os limites de capacidade são 85% de uso sustentado e disco em 80%/90% (aviso/crítico); gere alerta quando Object Storage se aproximar de 16/18 GB. Uma única VM e alarmes na nuvem não recuperam volume perdido nem impedem a Oracle de retomar uma instância Always Free que se qualifique como ociosa.

## Recuperação e solução de problemas

| Sintoma | Verificações e resposta segura |
|---|---|
| SSH negado | Confira IP público atual, CIDR da regra de segurança, chave privada SSH correspondente e entrada conhecida do host. Não abra SSH para `0.0.0.0/0`. |
| Volume não montado / sistema de arquivos inesperado | Pare a implantação. Confira OCID de anexação do volume e mapeamento do dispositivo `/dev/oracleoci`. Nunca formate um dispositivo que já tenha sistema de arquivos. |
| Vault `NotAuthorized` | Confira se a instância está no compartment dedicado, regra de correspondência do dynamic group e OCIDs dos segredos. Tente atualizar novamente somente após a propagação da política. |
| GHCR negado / arquitetura incorreta | Confira acesso somente leitura do token, proprietário da imagem, digest do manifesto e arquitetura em `docker image inspect`. Não use uma tag mutável como alternativa. |
| Certificado HTTPS ausente | Confira DNS público de fora da OCI, TCP 443, relógio do sistema e armazenamento persistente em `/srv/inspection/caddy`. TLS-ALPN do Caddy não usa a porta 80. |
| Keycloak redireciona para localhost / falha de login | Confira `KC_HOSTNAME`, issuer do realm, callbacks dos clients, origens públicas dos apps, subject inicial e cabeçalhos encaminhados pelo proxy. Mantenha realm master e caminhos administrativos privados. |
| Assinatura de storage negada | Confira namespace do endpoint, região OCI, par de chaves, bucket, URL em path-style e relógio do sistema. Garanta que o proxy restaure o `Host` OCI e use SNI TLS da OCI. Não torne o bucket público. |
| CORS de storage no navegador ou `ETag` ausente | Compare `Origin` da requisição às quatro origens permitidas; confirme tratamento de OPTIONS, PUT, GET e HEAD e exposição de `ETag`. Confira se o caminho começa com o nome do bucket configurado. |
| SMTP recusado | Confira remetente aprovado, endpoint SMTP regional, usuário/senha, STARTTLS, DNS SPF/DKIM e quotas de Email Delivery. Não troque por Mailpit. |
| Falha de migration ou readiness | Leia logs do migrador/API, saúde do banco e versão do schema. Mantenha o worker parado até a migração e roles de runtime estarem saudáveis. `/readyz` não comprova o funcionamento de toda integração. |
| OOM ou disco cheio | Confira limites dos containers, fila de PDFs, `df -h` e `docker system df`; interrompa trabalho não essencial, preserve arquivos do banco e avalie capacidade antes de redimensionar. Não apague volumes nem dados da aplicação para liberar espaço. |

Para substituir a VM, aplique um Plan revisado de runtime que preserve o volume de dados, anexe o volume existente à VM substituta no mesmo AD, verifique sistema de arquivos e UUID, atualize DNS para o novo IP público e execute bootstrap sem formatar. Se o volume de dados tiver sido perdido ou corrompido, este plano não tem cópia de recuperação.

## Parar e desativar

`ops.sh stop` para os serviços Compose sem apagar dados dos containers ou recursos OCI. Para retomar, atualize os segredos, valide a release e implante seu SHA. A desativação é uma mudança separada: revise um novo Terraform Plan, identifique VM, volume, bucket, Vault, chave e DNS individualmente, exporte antes os dados necessários e preserve volume/bucket até que o responsável pelos dados aceite explicitamente a perda. Nunca use a ação destroy do Resource Manager para simplesmente parar o ambiente.

## Procedimento destrutivo de desativação

Execute este procedimento somente em uma mudança separada, após o responsável aceitar a perda permanente ou verificar uma exportação independente. Pare a aplicação com `deploy/oci/ops.sh stop`, exporte os dados PostgreSQL/objetos necessários para um destino aprovado e registre outputs de foundation/runtime e registros DNS. Esvazie o bucket pelo OCI Object Storage para permitir sua exclusão pelo Terraform; isso apaga permanentemente os documentos. Na configuração runtime, remova os recursos exatos de VM, volume, VNIC/NSG/rede e política de segredos de runtime, bem como seus outputs; retire `prevent_destroy` do volume aprovado. Gere um novo ZIP runtime, confira que o Plan contém somente os recursos runtime registrados e execute Apply. Em foundation, remova os recursos exatos de bucket, IAM S3, alerta, remetente de e-mail, Vault/chave e respectivos outputs; retire `prevent_destroy` somente do bucket/Vault/chave aprovados. Revise e aplique esse Plan. Em outro Plan, remova o compartment por último, somente depois de a OCI confirmar que está vazio. Não remova recursos usados por outra carga. Por fim, remova manualmente os sete registros DNS da aplicação e os registros de e-mail. Consequência: bancos, documentos, certificados, segredos, logs e instância serão perdidos; não haverá recuperação sem exportação independente verificada. Este procedimento não oferece atalho de destroy em um comando.
