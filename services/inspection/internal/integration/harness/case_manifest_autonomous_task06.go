package harness

import "fmt"

// AutonomousCaseStatus tells where one assigned case stands.
type AutonomousCaseStatus string

const (
	// AutonomousCovered means an automated test carrying the case identifier
	// (the AIP_ prefix) exists in the area named by AutonomousCase.Where.
	AutonomousCovered AutonomousCaseStatus = "COVERED"
	// AutonomousPartial means an automated test covers only part of the case;
	// Note states the gap and who owns it. The case still counts as open for the
	// completion gate (INSPECTION_REQUIRE_COMPLETE_MANIFEST).
	AutonomousPartial AutonomousCaseStatus = "PARTIAL"
	// AutonomousPending means the case is still open; Note says why and who owns it.
	AutonomousPending AutonomousCaseStatus = "PENDING"
	// AutonomousObsolete means a later decision superseded the case; Note records it.
	AutonomousObsolete AutonomousCaseStatus = "OBSOLETE"
)

// AutonomousCase is one case assigned to task 06 of the
// autonomous-inspection-platform workflow. Its identifiers collide with other
// workflows, so a test only counts when it carries the AIP_ prefix and lives in
// the area (Where) whose behavior the case defines.
type AutonomousCase struct {
	ID, Kind string
	Status   AutonomousCaseStatus
	// Where is a slash separated path fragment of the directory that holds the
	// covering test. Required for covered cases.
	Where string
	// File, when set, is the base name of the test file that must carry the
	// case label. It pins the case to a test that drives the real handler (the
	// integration build tag, PostgreSQL) instead of a stub, so a same-named
	// label in a stub-based package cannot satisfy it.
	File string
	// Criterion states, per case, what the covering test must prove. It is
	// documentation for the reviewer; the manifest test verifies the label and
	// the File pin mechanically.
	Criterion string
	Note      string
}

// AutonomousTask06Cases returns the 148 cases assigned to task 06 of
// autonomous-inspection-platform (`.compozy/tasks/autonomous-inspection-platform/task_06.md`),
// kept apart from Task06AssignedCases, which lists cases of another workflow.
// A fresh slice is returned so a caller cannot mutate the process-wide contract.
func AutonomousTask06Cases() []AutonomousCase {
	const (
		harnessPath   = "internal/integration/harness"
		resolversPath = "platform/graphql/resolvers"
		retentionNote = "Fora do item AI-A: política, purga e prazos de retenção pertencem à frente de retenção; ainda sem teste AIP_."
		// otherFrontNote marks mechanical coverage: the label exists, and the
		// front that wrote it answers for the semantics.
		otherFrontNote = "Rotulado por outra frente (fora do item AI-A); o manifesto só verifica o rótulo, a semântica é revisada por quem o implementou."
	)
	var cases []AutonomousCase
	kindOf := func(prefix string) string {
		if prefix == "UT" {
			return "unit"
		}
		return "integration"
	}
	add := func(prefix string, from, to int, status AutonomousCaseStatus, where, note string) {
		for number := from; number <= to; number++ {
			cases = append(cases, AutonomousCase{ID: fmt.Sprintf("%s-%03d", prefix, number), Kind: kindOf(prefix), Status: status, Where: where, Note: note})
		}
	}
	covered := func(prefix string, where string, numbers ...int) {
		for _, number := range numbers {
			add(prefix, number, number, AutonomousCovered, where, "")
		}
	}
	// one lists a single case by identifier, with its real status.
	one := func(number int, status AutonomousCaseStatus, where, note string) {
		add("IT", number, number, status, where, note)
	}
	// pinned lists a covered case whose label must live in a specific test file.
	pinned := func(number int, where, file, criterion string) {
		cases = append(cases, AutonomousCase{ID: fmt.Sprintf("IT-%03d", number), Kind: "integration", Status: AutonomousCovered, Where: where, File: file, Criterion: criterion})
	}

	// Unit cases.
	covered("UT", "platform/llm", 11, 12)
	covered("UT", "features/reports/render_pdf", 15, 16)
	covered("UT", "features/analysis/core", 34, 35, 67)
	covered("UT", "features/reports/", 36, 37)
	covered("UT", "features/retention/core", 44)
	covered("UT", "features/retention/purge_data", 45)
	covered("UT", "features/dashboard/core", 48, 49)
	covered("UT", "features/analysis/process_comparison", 66)

	// Analysis of PHOTO evidence and classification.
	add("IT", 241, 260, AutonomousCovered, harnessPath, "")
	// Reports.
	covered("IT", "features/reports/generate_snapshot", 261, 262, 264, 265, 267, 270)
	covered("IT", "features/reports/render_pdf", 263, 266)
	covered("IT", resolversPath, 268)
	add("IT", 269, 269, AutonomousPending, "", "Depende de o tipo Report expor publicação e rótulo de invalidação (item de publicação/schema); o item AI-A não edita o schema.")
	// Project timeline, consolidated and historical stages.
	add("IT", 271, 271, AutonomousObsolete, "", "Superado: o modo consolidado e a configuração de modo de relatório foram removidos (migration 50, report_mode); não há modo desconhecido a recusar.")
	add("IT", 272, 275, AutonomousPending, "", "Fora do item AI-A: linha do tempo de projeto (dashboard/project_timeline) pertence à frente de projetos; ainda sem teste AIP_.")
	add("IT", 276, 276, AutonomousObsolete, "", "Superado: a consolidação de versões do projeto foi removida com o modo consolidado (migration 50).")
	add("IT", 277, 280, AutonomousPending, "", "Fora do item AI-A: linha do tempo de projeto (dashboard/project_timeline) pertence à frente de projetos; ainda sem teste AIP_.")
	// Dashboard and triage.
	add("IT", 281, 290, AutonomousPending, "", "Fora do item AI-A: dashboard/summary e dashboard/list_triage pertencem à frente do dashboard; ainda sem teste AIP_ (IT-427..430 cobrem apenas o transporte GraphQL).")
	// Critical alerts, labelled by the notification front.
	add("IT", 291, 292, AutonomousCovered, "features/reports/handle_classified", otherFrontNote)
	add("IT", 293, 293, AutonomousPending, "", "O teste rotulado em handle_classified verifica a identidade da inspeção por destino, não o agrupamento/fila sob limite de taxa que o caso define; o pacote pertence à frente de publicação/alertas.")
	add("IT", 294, 294, AutonomousPending, "", "O teste rotulado em handle_classified verifica apenas que a perda de escopo interrompe alertas futuros; o caso exige negar o acesso ao link de uma mensagem já entregue. Pacote da frente de publicação/alertas.")
	add("IT", 295, 300, AutonomousCovered, "features/reports/handle_classified", otherFrontNote)
	// Retention policy and purge, labelled by the retention front.
	add("IT", 301, 303, AutonomousCovered, "features/retention/configure_policy", otherFrontNote)
	add("IT", 304, 304, AutonomousPending, "", retentionNote)
	add("IT", 305, 310, AutonomousCovered, "features/retention/configure_policy", otherFrontNote)
	// Catalog seeds for construction and cleaning (IT-321..340), one entry per
	// case. The labels TestAIP_IT321To340… and the pairs TestAIP_ITnnnITmmm…
	// expand to ranges in the scanner, so the status below follows _tests.md
	// (not the label) for every case.
	one(321, AutonomousCovered, "features/templates/catalog", "Revisado (ADR-020): progresso/checklist deixaram de existir; vale o limite de evidência inválido (TestAIP_IT321IT331…) mais a obra pelo pipeline comum (TestAIP_IT321To340…).")
	one(322, AutonomousCovered, "features/templates/catalog", "Revisado (ADR-021): seeds são sem referência por definição; modos que exigem referência não iniciam sem ela (TestAIP_IT322IT332…).")
	one(323, AutonomousCovered, harnessPath, "")
	one(324, AutonomousCovered, harnessPath, "")
	one(325, AutonomousCovered, harnessPath, "")
	one(326, AutonomousPending, "", "Retomada de captura por etapa não tem teste específico de obra; o rascunho é por responsabilidade (testes de captura). Dono: frente de segmentos (AI-B).")
	one(327, AutonomousPartial, harnessPath, "TestAIP_IT321To340… prova um resultado de análise, uma classificação e uma versão de relatório com reentrega; nenhum consumidor leva a etapa a COMPLETED (projects/core só inicia/pula), então 'um resultado de etapa' depende dessa feature.")
	one(328, AutonomousCovered, harnessPath, "")
	one(329, AutonomousCovered, harnessPath, "")
	one(330, AutonomousCovered, harnessPath, "")
	one(331, AutonomousPartial, "features/templates/catalog", "Só os limites de evidência inválidos estão cobertos (TestAIP_IT321IT331…); 'a etapa não conclui até corrigir' depende da conclusão de etapa não implementada (ver IT-327).")
	one(332, AutonomousCovered, "features/templates/catalog", "Revisado (ADR-021): modos baseados em origem exigem referência (TestAIP_IT322IT332…); seeds não usam origem.")
	one(333, AutonomousCovered, "features/templates/catalog", "")
	one(334, AutonomousPartial, harnessPath, "Projeto interno e outro tenant negados (TestAIP_IT324IT334…); a negação do relatório interno para a sessão externa de limpeza é coberta por matrizes de relatório de outro workflow, sem teste AIP_ próprio.")
	one(335, AutonomousPending, "", "Corrida entre troca de origem e criação de etapa não foi tratada: os seeds de limpeza não usam origem (ADR-021). Dono: frente de segmentos (AI-B).")
	one(336, AutonomousPending, "", "Perda de conectividade é comportamento do PWA de captura (Playwright), fora do item AI-A. Dono: frente de segmentos (AI-B).")
	one(337, AutonomousPartial, harnessPath, "TestAIP_IT321To340… prova que a mesma submissão duas vezes gera um único job/resultado de análise; 'um resultado de etapa aceito' depende da conclusão de etapa não implementada (ver IT-327).")
	one(338, AutonomousObsolete, "", "Superado por ADR-021: seeds de limpeza não exigem origem completa, logo não há início a bloquear por origem incompleta. Cobertura equivalente: IT-332 (modos baseados em origem não iniciam sem referência).")
	one(339, AutonomousCovered, harnessPath, "")
	one(340, AutonomousPartial, harnessPath, "Linha do tempo de 100 etapas navegável (TestAIP_IT323IT330…); filtros por ativo e navegação do relatório são de frontend, sem teste AIP_.")
	// LiteLLM, Gotenberg and retention flows.
	add("IT", 371, 373, AutonomousCovered, harnessPath, "")
	add("IT", 374, 374, AutonomousObsolete, "", "Superado: o Gotenberg foi removido; o PDF é montado em processo por Maroto (features/reports/render_pdf). Cobertura equivalente: UT-015, UT-016, IT-263, IT-266.")
	add("IT", 375, 375, AutonomousObsolete, "", "Superado: sem Gotenberg não há HTML-only por falha do renderizador externo; a falha do PDF fica registrada (FAILED) sem alterar análise nem classificação. Cobertura equivalente: IT-266.")
	add("IT", 390, 390, AutonomousPending, "", retentionNote)
	// GraphQL transport.
	add("IT", 423, 430, AutonomousCovered, resolversPath, "")
	add("IT", 433, 438, AutonomousCovered, resolversPath, "")
	add("IT", 527, 534, AutonomousCovered, resolversPath, "")
	// Event contracts. The consumers run through the real handlers against
	// PostgreSQL in harness/aip_events_integration_test.go (integration tag);
	// platform/messaging only exercises stub handlers and does not count.
	const eventsFile = "aip_events_integration_test.go"
	for number := 561; number <= 562; number++ {
		pinned(number, harnessPath, eventsFile, "O consumidor real de capture.submitted cria um job e a mensagem inválida chega à DLQ nomeada, em PostgreSQL.")
	}
	for number := 567; number <= 576; number++ {
		pinned(number, harnessPath, eventsFile, "O handler real do evento consome uma vez (idempotente, via inbox) e a mensagem inválida chega à DLQ nomeada, em PostgreSQL.")
	}
	for number := 583; number <= 586; number++ {
		pinned(number, harnessPath, eventsFile, "O handler real de retenção consome uma vez e a mensagem inválida chega à DLQ nomeada, em PostgreSQL.")
	}
	return cases
}

// PendingAutonomousTask06Cases lists the cases that are neither covered nor
// obsolete (pending and partial), which is what keeps the task open.
func PendingAutonomousTask06Cases() []AutonomousCase {
	var pending []AutonomousCase
	for _, item := range AutonomousTask06Cases() {
		if item.Status == AutonomousPending || item.Status == AutonomousPartial {
			pending = append(pending, item)
		}
	}
	return pending
}
