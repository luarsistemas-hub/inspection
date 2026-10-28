import type { ReportWorkspaceQuery } from "@/graphql/generated";
import { environmentTypeLabel, environmentTypeOptions } from "./inventory-presentation";

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
  living_room: "Sala de estar", dining_room: "Sala de jantar", kitchen: "Cozinha",
  bedroom: "Quarto", suite: "Suíte", bathroom: "Banheiro", toilet: "Lavabo",
  laundry: "Lavanderia", service_area: "Área de serviço", balcony: "Varanda",
  garage: "Garagem", hallway: "Corredor", entrance: "Entrada", office: "Escritório",
  storage: "Depósito", external_area: "Área externa", pool_area: "Área da piscina",
  gourmet_area: "Área gourmet", floor: "Piso", wall: "Parede", ceiling: "Teto",
  door: "Porta", window: "Janela", countertop: "Bancada", sink: "Pia", faucet: "Torneira",
  cabinet: "Armário", shower_box: "Box", mirror: "Espelho", stove_cooktop: "Fogão ou cooktop",
  refrigerator: "Geladeira", air_conditioner: "Ar-condicionado", water_heater: "Aquecedor de água",
  wardrobe: "Guarda-roupa", sofa: "Sofá", table: "Mesa", chair: "Cadeira",
  curtains_blinds: "Cortinas ou persianas", additional_evidence: "Evidências adicionais",
};
const roomNames: Record<string, string> = {
  "living room": "Sala de estar", "dining room": "Sala de jantar", kitchen: "Cozinha",
  bedroom: "Quarto", suite: "Suíte", bathroom: "Banheiro", toilet: "Lavabo",
  laundry: "Lavanderia", "service area": "Área de serviço", balcony: "Varanda",
  garage: "Garagem", hallway: "Corredor", entrance: "Entrada", office: "Escritório",
  storage: "Depósito", "external area": "Área externa", "pool area": "Área da piscina",
  "gourmet area": "Área gourmet",
};

function videoText(value: string) {
  return value.replace(/\b[a-z]+(?:_[a-z]+)*\b/gi, (term) => videoTerms[term.toLocaleLowerCase()] ?? term);
}

function roomLabel(value: string) {
  const normalized = value.trim();
  if (!normalized) return normalized;
  const translatedCode = environmentTypeOptions.some(([code]) => code === normalized.toLocaleUpperCase())
    ? environmentTypeLabel(normalized.toLocaleUpperCase())
    : undefined;
  if (translatedCode) return translatedCode;
  const translatedName = roomNames[normalized.toLocaleLowerCase()];
  if (translatedName) return translatedName;
  return videoText(normalized);
}

export function presentReportSection(value: string) { return roomLabel(value); }
export function presentReportLabel(value: string) { return videoText(value); }

function location(requirement: Report["requirements"][number]) {
  const label = videoText(requirement.label.trim());
  const rawSection = requirement.section.trim();
  const section = roomLabel(rawSection);
  const generic = /^(vídeo|video|vistoria|main|geral)$/i.test(rawSection);
  const parts = label.split(/\s+[—–·]\s+/);
  if (parts.length > 1 && (generic || parts[0].toLocaleLowerCase() === rawSection.toLocaleLowerCase() || parts[0].toLocaleLowerCase() === section.toLocaleLowerCase())) {
    return { room: generic ? roomLabel(parts[0]) : section, title: videoText(parts.slice(1).join(" — ")) };
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
