# Admin e Dashboard exibem erro cru ao expirar a autenticação

## Severidade

Alta — a sessão deixa a aplicação em estado inconsistente e expõe detalhe técnico.

## Como reproduzir

1. Autenticar no Admin ou Dashboard.
2. Aguardar a expiração do access token.
3. Executar uma consulta, aguardar o polling de notificações ou clicar em Atualizar.

## Atual

A resposta de autenticação em texto é processada incondicionalmente com
`response.json()`. A UI mostra `Unexpected token 'a', "authentica"... is not
valid JSON`; o tratamento de `UNAUTHENTICATED` não é alcançado.

## Esperado

A aplicação deve limpar a sessão e solicitar nova autenticação com mensagem
estável, independentemente do content type da resposta HTTP.

## Critérios de aceite

- Verificar status e `content-type` antes de decodificar JSON.
- Mapear 401/403 não JSON para `UNAUTHENTICATED`/`FORBIDDEN`.
- Cobrir expiração e refresh/reload em Admin e Dashboard.
