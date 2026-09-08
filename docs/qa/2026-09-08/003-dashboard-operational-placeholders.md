# Dashboard: ações operacionais e páginas de detalhe são placeholders

## Severidade

Bloqueante — não é possível executar o ciclo operacional pelo frontend.

## Como reproduzir

1. Entrar no Dashboard como `TENANT_ADMIN`.
2. Abrir Inspeções, Projetos, Triagem ou Relatórios.
3. Clicar em **Nova ação** ou **Publicar relatório**.
4. Em uma triagem populada, clicar na inspeção.

## Atual

Os botões apenas alteram uma mensagem local. O link de inspeção só acrescenta
`inspectionId` à URL e mantém a mesma tela genérica, sem carregar o recurso.

## Esperado

Cada ação deve abrir o formulário/contexto correto, chamar a mutation
correspondente e renderizar sucesso, conflito, validação e falha assíncrona.

## Critérios de aceite

- Implementar criação/cancelamento/etapas/recaptura conforme capacidades.
- Implementar publicação e invalidação de relatório.
- Renderizar detalhes de inspeção e projeto selecionados.
- Adicionar E2E autenticado para caminho feliz e estados de erro.
