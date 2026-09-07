# Funcionalidades

| Domínio | Operações |
| --- | --- |
| tenancy/access | tenant, unidades, memberships, convites internos e escopos |
| participants | cadastro, contatos, verificação, canais e desativação |
| segments/templates | publicação e ativação de versões e perfis |
| assets/origins | ativos, referências e convites de origem |
| schedules/inspections | recorrência, lembretes, inspeções, cancelamento e invalidação |
| projects | criação e transições de estágios |
| invitations/capture | OTP, sessão externa, metadata, impossibilidade e submissão |
| media | upload multipart, presign, conclusão, normalização e falso positivo |
| analysis/reports | comparação, classificação, snapshots e PDF |
| recapture | solicitação, submissão e expiração |
| notifications | intenções, entregas e status Twilio |
| retention/usage/audit/dashboard | purge, legal hold, consumo, auditoria e projeções |

Cada mutation devolve `userErrors` para entrada, conflito ou autorização. Eventos acionam as partes assíncronas; o frontend concentra gestão e captura.
