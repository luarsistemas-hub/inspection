# Checklist schema evaluation fixtures

These sanitized fixtures are a harness seed for the release-gated catalog schema evaluation. They contain synthetic evidence identifiers and no real property, person, transcript, or image. The release evaluation must exercise the configured LiteLLM product path; these files only pin the expected structured shape and attribute-provenance labels for local contract checks.
# Avaliação do catálogo v2

Estas fixtures são sanitizadas e servem para avaliar a compatibilidade e a
qualidade do schema v2 pelo caminho LiteLLM do produto. O schema v2 continua
desativado: sua ativação depende da avaliação do corpus e de um release gate
explícito, conforme ADR-007. Após aprovação, publique explicitamente uma nova
definição versionada com `DefinitionForTaskVersion`; ela se aplica somente a
análises futuras. Até lá, novas análises usam o schema v1, sem alterar o default
de produção nem os snapshots históricos.
