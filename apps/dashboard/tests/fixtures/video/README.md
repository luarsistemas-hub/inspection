# Vídeos dos testes locais de laudo

Coloque os arquivos MP4/H.264 abaixo nesta pasta. Os MP4 são ignorados pelo Git.

- `origem.mp4`: vistoria de origem do cenário de comparação.
- `atual.mp4`: vistoria atual, compartilhada pelos cenários de checklist e comparação.

Use vídeos curtos, bem iluminados, do mesmo imóvel e sem pessoas ou documentos
legíveis. Para a comparação, mostre pelo menos um ambiente ou componente
reconhecível nos dois vídeos. Cada arquivo deve respeitar o limite de 1 GiB e a
duração máxima informada pela política local de vídeo.

## Preparar a stack

Configure `INSPECTION_LLM_MODE=live` e as credenciais LiteLLM/provedor em
`.env.inspection`, depois inicie a stack. Confira o modo efetivo nos serviços
locais antes de executar. Vídeo não exige uma ativação adicional no seed:

```sh
./scripts/local.sh up
INSPECTION_QA_MEDIA_KIND=VIDEO ./scripts/local.sh seed
```

O Google Chrome precisa estar instalado para reproduzir os MP4/H.264 na prévia
do Capture e nas evidências do laudo.

Execute na pasta `apps/dashboard`:

```sh
npm run test:e2e:video-live
```

Cada cenário também pode ser executado isoladamente:

```sh
npm run test:e2e:video-live -- --grep checklist
npm run test:e2e:video-live -- --grep comparação
```

Opcionalmente, configure `INSPECTION_E2E_USERNAME` e `INSPECTION_E2E_PASSWORD`
para a conta local do Dashboard ou `PLAYWRIGHT_VIDEO_LIVE_TIMEOUT_MS` para
alterar o limite de 30 minutos por cenário. O Playwright grava screenshots,
vídeos e traces de falhas em `test-results/video-live`.

Cada cenário cria um participante próprio com o endereço
`qa.inspection@example.test`, que é o destinatário de OTP permitido no ambiente
local.
