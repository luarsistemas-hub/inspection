# Capture: corrida após upload deixa mídia pronta visualmente, mas sem metadados e sem retomada

## Severidade

Bloqueante — o participante não consegue concluir a inspeção após um upload válido.

## Como reproduzir

1. Abrir convite válido, confirmar OTP e aceitar o processamento.
2. Informar uma descrição e selecionar uma imagem PNG válida.
3. Aguardar o upload multipart.
4. Tentar enviar a inspeção.

## Atual

O cliente chama `saveCaptureMetadata` imediatamente após `completeMediaUpload`,
antes de a mídia chegar a `VERIFIED`/`SCREENED`. A mutation falha, mas a UI
mostra `1 pronta(s), 0 pendente(s)`. Como `Array.every` em `parts: []` também é
verdadeiro no estado antigo, o botão **Retomar envio pendente** desaparece. O
envio final responde `Aguarde 0 envio(s) pendente(s)`.

No banco, a mídia chegou depois a `SCREENED`, porém `requirement_key` e
`description` permaneceram vazios.

## Esperado

O fluxo deve aguardar/pollar a conclusão da verificação, persistir metadados e
só então marcar a evidência como pronta. Falhas recuperáveis devem manter uma
ação de retomada.

## Critérios de aceite

- Modelar estado explícito para upload, verificação, screening e metadados.
- Não considerar `parts: []` como upload pronto.
- Permitir retomada após falha em complete/metadata.
- Exibir a mensagem segura retornada pela API em vez do fallback genérico.
- Cobrir a corrida real com worker em E2E.
