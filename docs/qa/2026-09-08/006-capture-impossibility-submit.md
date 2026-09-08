# Capture: justificativa de impossibilidade válida não pode ser enviada sem foto

## Severidade

Alta — o caminho alternativo previsto pelo domínio fica inutilizável.

## Como reproduzir

1. Abrir uma responsabilidade cujo requisito permite impossibilidade.
2. Registrar uma justificativa válida.
3. Clicar em **Enviar inspeção completa**.

## Atual

A API salva a justificativa, mas `readyForSubmission` exige
`drafts.length > 0`. A tela bloqueia antes de chamar a mutation e mostra
`Aguarde 0 envio(s) pendente(s) antes de confirmar.`

## Esperado

Uma resposta de impossibilidade aceita deve satisfazer o requisito sem obrigar
a existência de mídia local.

## Critérios de aceite

- Incluir answers/impossibilidades no cálculo de prontidão do frontend ou consultar o estado canônico antes do submit.
- Permitir submit quando todos os requisitos estiverem satisfeitos por mídia ou impossibilidade.
- Cobrir requisito obrigatório com e sem `impossibilityAllowed`.
