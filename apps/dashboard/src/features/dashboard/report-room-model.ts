import type { ReportWorkspaceQuery } from "@/graphql/generated";

type Report = NonNullable<ReportWorkspaceQuery["report"]>;
export type RoomEvidence = Report["evidence"]["nodes"][number] & { snapshotId?: string };
export type RoomEntry = { requirement: Report["requirements"][number]; evidence: RoomEvidence[] };
export type RoomItem = {
  key: string;
  title: string;
  entries: RoomEntry[];
  findings: Report["findings"];
  instructions: string[];
  state: string;
  kind: "finding" | "recapture" | "pending" | "clear";
};
export type ReportRoom = { name: string; items: RoomItem[]; findings: Report["findings"] };

const videoTerms: Record<string, string> = {
  television: "Televisão", roller_shade: "Persiana",
  interior_to_exterior: "de dentro para fora", corner_transition: "transição entre os cantos",
  lower_corner: "canto inferior", upper_corner: "canto superior", full_view: "completa",
  glass_pane: "vidro", wall_and_window: "parede e janela", corner_view: "do canto",
  side_angle: "lateral", front_angle: "frontal",
};

function videoText(value: string) {
  return value.replace(/\b[a-z]+(?:_[a-z]+)*\b/g, (term) => videoTerms[term] ?? term);
}

function location(requirement: Report["requirements"][number]) {
  const label = videoText(requirement.label.trim());
  const section = requirement.section.trim();
  const generic = /^(vídeo|video|vistoria|main|geral)$/i.test(section);
  const parts = label.split(/\s+[—–·]\s+/);
  if (parts.length > 1 && (generic || parts[0].toLocaleLowerCase() === section.toLocaleLowerCase())) {
    return { room: generic ? parts[0] : section, title: parts.slice(1).join(" — ") };
  }
  return { room: generic || !section ? "Visão geral" : section, title: label };
}

function itemState(requirement: Report["requirements"][number], hasFindings: boolean): Pick<RoomItem, "kind" | "state"> {
  if (hasFindings) return { kind: "finding", state: "Requer atenção" };
  if (/^Para recaptura:/i.test(requirement.instructions?.trim() ?? "")) return { kind: "recapture", state: "Recaptura necessária" };
  if (requirement.analysisStatus === "FAILED") return { kind: "pending", state: "Análise indisponível" };
  if (requirement.analysisStatus === "INCONCLUSIVE" || requirement.analysisStatus === "PENDING") return { kind: "pending", state: "Comparação pendente" };
  return { kind: "clear", state: "Sem achados registrados" };
}

export function buildReportRooms(report: Report): { rooms: ReportRoom[]; captureInstructions: string[] } {
  const rooms = new Map<string, ReportRoom>();
  const evidence = report.evidence.nodes.map((item) => ({ ...item, snapshotId: report.id }));
  const captureInstructions: string[] = [];
  const roomFor = (name: string) => {
    let room = rooms.get(name);
    if (!room) { room = { name, items: [], findings: [] }; rooms.set(name, room); }
    return room;
  };

  for (const requirement of report.requirements) {
    const items = evidence.filter((item) => item.requirementKey === requirement.key);
    if (requirement.key === "video:main" && !items.length && report.requirements.some((item) => item.key !== "video:main" && item.key.startsWith("video:"))) {
      if (requirement.instructions) captureInstructions.push(requirement.instructions);
      continue;
    }
    const { room: name, title } = location(requirement);
    const room = roomFor(name);
    const findings = report.findings.filter((finding) => finding.evidenceIds.some((id) => items.some((item) => item.id === id)));
    const state = itemState(requirement, findings.length > 0);
    // Compact repeated scopes without pairing evidence across requirement boundaries.
    const groupKey = findings.length ? requirement.key : JSON.stringify([title, state.kind, requirement.analysisMode, requirement.analysisStatus, requirement.noRelevantChange]);
    let card = room.items.find((item) => item.key === groupKey);
    if (!card) {
      card = { key: groupKey, title, entries: [], findings, instructions: [], ...state };
      room.items.push(card);
    }
    card.entries.push({ requirement, evidence: items });
    if (requirement.instructions) {
      let instruction = videoText(requirement.instructions.trim());
      if (state.kind === "recapture") {
        instruction = instruction.replace(/^Para recaptura:\s*/i, "");
        const opening = `filme ${videoText(requirement.label)} `;
        if (instruction.toLocaleLowerCase().startsWith(opening.toLocaleLowerCase())) instruction = `Filme ${instruction.slice(opening.length)}`;
      }
      instruction = instruction.charAt(0).toLocaleUpperCase() + instruction.slice(1);
      if (!card.instructions.includes(instruction)) card.instructions.push(instruction);
    }
    room.findings = [...new Set([...room.findings, ...findings])];
  }

  const matchedKeys = new Set(report.requirements.map((item) => item.key));
  const unmatched = evidence.filter((item) => !matchedKeys.has(item.requirementKey));
  for (const key of new Set(unmatched.map((item) => item.requirementKey))) {
    const items = unmatched.filter((item) => item.requirementKey === key);
    const findings = report.findings.filter((finding) => finding.evidenceIds.some((id) => items.some((item) => item.id === id)));
    const room = roomFor("Evidências adicionais");
    room.items.push({ key, title: videoText(items[0].label || items[0].component || "Evidências adicionais"), entries: [{ requirement: { key, label: "Evidências adicionais", section: room.name, instructions: null, noRelevantChange: null, analysisMode: "CURRENT_ONLY", analysisStatus: "COMPLETED" }, evidence: items }], findings, instructions: [], kind: findings.length ? "finding" : "clear", state: findings.length ? "Requer atenção" : "Sem achados registrados" });
    room.findings = [...new Set([...room.findings, ...findings])];
  }
  const linkedFindings = new Set([...rooms.values()].flatMap((room) => room.findings));
  const unlinked = report.findings.filter((finding) => !linkedFindings.has(finding));
  if (unlinked.length) {
    const room = roomFor("Observações do laudo");
    room.findings.push(...unlinked);
    room.items.push({ key: "unlinked", title: "Observações do laudo", entries: [], findings: unlinked, instructions: [], kind: "finding", state: "Requer atenção" });
  }
  const priority = { finding: 0, recapture: 1, pending: 2, clear: 3 };
  for (const room of rooms.values()) room.items.sort((a, b) => priority[a.kind] - priority[b.kind]);
  return { rooms: [...rooms.values()], captureInstructions };
}
