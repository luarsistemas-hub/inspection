# Plano inicial de infraestrutura — OCI Always Free

## Objetivo

Preparar uma implantação inicial do monorepo Inspection na Oracle Cloud Infrastructure (OCI), priorizando a franquia Always Free e preservando um caminho simples para evoluir quando uso, disponibilidade ou requisitos operacionais excederem os limites de uma VM única.

O guia Oracle indicado demonstra o Resource Manager com o template de exemplo MuShop. Esse template não representa a arquitetura do Inspection; será necessário criar uma configuração Terraform própria e carregá-la como uma pilha. [Guia Always Free com Resource Manager](https://docs.oracle.com/pt-br/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources_Launching.htm) · [Criar pilha a partir de ZIP](https://docs.oracle.com/en-us/iaas/Content/ResourceManager/Tasks/create-stack-local.htm).

## Arquitetura inicial

Executar a solução em uma VM Ubuntu ARM64 `VM.Standard.A1.Flex`, na região principal da tenancy:

| Recurso | Configuração inicial | Observações |
| --- | --- | --- |
| Compute | 2 OCPUs e 12 GB de memória | Total máximo mensal Always Free para A1; confirmar capacidade na região antes de provisionar. |
| Boot volume | 50 GB | Incluído no limite combinado de Block Volume. |
| Volume de dados | 150 GB | Persistência de PostgreSQL, RabbitMQ e demais serviços com estado. |
| OCI Object Storage | Até 20 GB | Bucket privado para os arquivos da aplicação, sujeito à franquia vigente da tenancy. |
| Rede | Uma VCN e subnet pública | Endereçar a VM publicamente; restringir portas no NSG. |

A franquia documentada para A1 equivale a 2 OCPUs e 12 GB; a conta também recebe 200 GB combinados de boot e block volumes e até 20 GB de Object Storage. Esses limites são compartilhados pela tenancy e devem ser conferidos no painel de quotas antes do `apply`. A1 deve ser criada na região principal. A Oracle pode retomar instâncias Always Free consideradas ociosas com base em uso de CPU, rede e memória durante sete dias. [Limites Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm).

### Serviços da aplicação

O ambiente de produção deverá conter os componentes reais necessários à solução:

- PostgreSQL, Dragonfly e RabbitMQ;
- OCI Object Storage como storage de objetos da aplicação, substituindo o MinIO;
- Keycloak e LiteLLM;
- Inspection API, worker, scheduler e migrador;
- Admin, Dashboard, Capture e Onboarding;
- proxy HTTPS, que encaminha tráfego público para os serviços correspondentes.

Usar um domínio controlado pelo projeto, com subdomínios separados para os quatro apps, API e Keycloak. O banco, broker, cache e painéis de administração permanecem inacessíveis pela internet. Publicar apenas HTTPS; permitir SSH somente a partir de um CIDR administrativo conhecido. Certificados TLS podem ser emitidos pelo proxy por ACME. O acesso aos objetos será feito pelo endpoint regional S3 compatível da OCI, não por um serviço MinIO hospedado na VM.

Os frontends Next.js recebem várias URLs `NEXT_PUBLIC_*` no build. A publicação deverá compilar imagens `linux/arm64` com os endereços definitivos de API, OIDC, storage e navegação entre apps. A implantação deve confirmar suporte ARM64 de todas as imagens antes do primeiro deploy.

## Provisionamento com Terraform

Criar configuração própria compatível com Oracle Resource Manager. O escopo Terraform inclui:

1. VCN, subnet pública, Internet Gateway, route table e Network Security Group;
2. regras de entrada para HTTPS e, se necessário ao procedimento escolhido, SSH restrito por CIDR;
3. instância A1 Ubuntu ARM64, boot volume de 50 GB e volume de dados de 150 GB;
4. bucket privado de Object Storage para os arquivos da aplicação, com CORS limitado às origens dos frontends;
5. tags e saídas sem conteúdo secreto, como IP público, OCIDs, namespace, endpoint regional e nome do bucket.

Usar o fluxo **Create stack → My configuration → ZIP file → revisar variáveis → criar pilha → executar e inspecionar o Plan → Apply**. Não aplicar automaticamente sem revisar o plano. O ZIP precisa conter os arquivos Terraform e declarar uma versão Terraform suportada pelo Resource Manager.

Não incluir senhas, tokens, chaves de API, chave SSH privada nem arquivos `.env` nas variáveis Terraform, `cloud-init`, outputs, repositório ou estado. Guardar segredos operacionais no OCI Vault Always Free e entregá-los ao Compose em arquivo protegido no host. Para o acesso S3 compatível, usar uma Customer Secret Key da OCI com permissões limitadas ao bucket; guardar suas credenciais no Vault e nunca no Terraform. Chaves privadas SSH são geradas e mantidas fora do Terraform. [Resource Manager e ZIP](https://docs.oracle.com/en-us/iaas/Content/ResourceManager/Tasks/create-stack-local.htm) · [Vault Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm).

## Perfil de produção e integrações

Criar um Compose de produção ou configuração equivalente, separado dos valores locais. Configurar o adapter S3 existente para usar o endpoint compatível OCI, o namespace, a região e as credenciais Customer Secret Key. Revisar o uso do MinIO Go SDK para selecionar o estilo de URL aceito pela OCI e assegurar que as URLs pré-assinadas geradas apontem para o endpoint correto. Configurar CORS no bucket para os domínios exatos dos frontends, métodos necessários aos uploads multipart e leitura, e exposição do cabeçalho `ETag`. Validar multipart, assinatura, expiração e acesso privado antes de migrar dados de usuários. A implantação não deve iniciar:

- Mailpit;
- `twilio-fake`, `meta-fake` ou `litellm-stub`;
- `inspection-seed` e outros seeds/fixtures de QA;
- WireMock ou qualquer configuração de `INSPECTION_LLM_MODE=mock`.

Configurar e validar Keycloak em modo de produção, PostgreSQL com as roles de runtime/migração já usadas pelo projeto, bucket privado OCI, RabbitMQ autenticado, TLS nas origens e valores explícitos de CORS, OIDC, callback e URLs públicas. Não reutilizar senhas ou defaults do Compose local.

Usar OCI Email Delivery como provedor SMTP real. A configuração requer credenciais SMTP, remetente aprovado e domínio autenticado conforme a documentação. A franquia Always Free publicada inclui até 3.000 e-mails por mês; verificar quotas e região disponíveis antes de contar com esse limite. [Introdução ao Email Delivery](https://docs.oracle.com/en-us/iaas/Content/Email/Reference/gettingstarted.htm) · [Recursos Always Free](https://docs.oracle.com/en-us/iaas/Content/FreeTier/freetier_topic-Always_Free_Resources.htm).

LiteLLM e WhatsApp ficam configurados para integração real, mas desativados no primeiro deploy até existirem credenciais e aprovação de orçamento dos provedores. Enquanto estiverem desativados, os fluxos dependentes devem permanecer indisponíveis; não devem ser encaminhados para stubs nem para modo mock. O deploy deverá confirmar o comportamento da aplicação nessa condição antes de ser considerado pronto para usuários.

## Dados e operação

- Montar o volume de dados em caminho estável do host e usar volumes nomeados ou bind mounts explícitos para PostgreSQL, RabbitMQ e demais serviços persistentes. Manter dados fora da camada gravável dos containers.
- Armazenar os arquivos de negócio diretamente no bucket privado OCI. Aplicar políticas de acesso mínimas e manter o bucket sem acesso anônimo; a aplicação entrega URLs pré-assinadas com validade limitada.
- Nesta fase, não provisionar nem implementar backups. Registrar que o plano de recuperação e backup deverá ser definido antes de armazenar dados que precisem de proteção contra perda.
- Acompanhar uso de CPU, memória, disco e Object Storage, saúde dos containers e espaço disponível. Configurar alertas de quota. A VM única não oferece alta disponibilidade e constitui ponto único de falha.
- Evitar que builds de Next.js e serviços pesados concorram com o processamento da aplicação na VM. Construir imagens ARM64 fora do host e promover versões identificadas; confirmar antes qual registry e fluxo de publicação cabem no orçamento gratuito.

## Fases e critérios de aceite

1. **Pré-checagem:** conferir região principal, quotas, capacidade A1, domínio/DNS, imagens ARM64 e orçamento; identificar todo requisito de provedor externo ainda sem credencial.
2. **Infraestrutura:** criar pilha Terraform; revisar `plan`; aplicar VCN, regras de rede, VM, volume e bucket de aplicação; registrar saídas não sensíveis.
3. **Host e deploy:** instalar Docker e Compose, montar o volume, configurar proxy/TLS, carregar segredos de forma protegida e iniciar serviços de produção; executar migrações como etapa controlada antes de promover API e worker.
4. **Validação:** verificar HTTPS, login OIDC, GraphQL, os quatro apps, upload multipart e download privados via URL assinada no OCI Object Storage, CORS, geração de PDF, persistência de filas e comunicação SMTP; confirmar que serviços fake/teste não iniciaram e que LLM/WhatsApp estão indisponíveis enquanto sem credenciais.
5. **Acompanhamento:** validar alarmes de capacidade e monitorar consumo nas primeiras semanas.

`/readyz` verifica apenas parte das dependências. A validação deverá checar separadamente PostgreSQL, RabbitMQ, Dragonfly, OCI Object Storage, Keycloak e LiteLLM conforme o estado habilitado de cada integração.

O plano passa para implantação quando todos os recursos previstos estiverem dentro das quotas gratuitas, o Plan Terraform não contiver recursos pagos inesperados, as imagens ARM64 estiverem acessíveis, as credenciais S3 compatíveis estiverem protegidas, os serviços reais essenciais iniciarem com segurança e uploads multipart e URLs pré-assinadas tiverem sido validados.

## Evolução

Quando a VM única atingir os limites de memória, CPU, armazenamento, disponibilidade ou recuperação, avaliar primeiro métricas e volume real. Evoluir em etapas, separando serviços com maior consumo e estado persistente, começando pelo banco ou processamento conforme os dados observados. Qualquer recurso além da franquia Always Free exige estimativa e revisão de custo antes de sua criação.
