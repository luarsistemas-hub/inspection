"use client";

import { useEffect, useState } from "react";
import { Button, Field, Input, Select } from "@inspection/design-system";
import type * as G from "@/graphql/generated";
import { conditionOptions, environmentTypeOptions, itemTypeOptions } from "../inventory-presentation";
import { formatEvidenceTime, type ReviewEnvironment, type ReviewItem } from "./review-model";

const rejectionReasons = [
  ["NOT_EXISTS", "Não existe no imóvel"],
  ["NOT_RELEVANT", "Não é relevante para o inventário"],
  ["DUPLICATE", "Item duplicado"],
  ["PERSONAL_OBJECT", "Objeto pessoal"],
  ["OTHER", "Outro motivo"],
] as const;

/** RejectItemForm collects a rejection reason code accepted by the API. */
export function RejectItemForm({ itemName, onSubmit }: { itemName: string; onSubmit: (reason: string, reasonText: string | null) => Promise<void> }) {
  const [reason, setReason] = useState("");
  const [reasonText, setReasonText] = useState("");
  const [submitting, setSubmitting] = useState(false);
  const incomplete = !reason || (reason === "OTHER" && !reasonText.trim());
  return <form className="baseline-reject-form" onSubmit={(event) => {
    event.preventDefault();
    if (incomplete) return;
    setSubmitting(true);
    void onSubmit(reason, reason === "OTHER" ? reasonText.trim() : null).finally(() => setSubmitting(false));
  }}>
    <p>Informe por que “{itemName}” deve ser rejeitado.</p>
    <label>Motivo da rejeição
      <select aria-label="Motivo da rejeição" value={reason} onChange={(event) => setReason(event.target.value)} required>
        <option value="">Selecione um motivo</option>
        {rejectionReasons.map(([code, label]) => <option key={code} value={code}>{label}</option>)}
      </select>
    </label>
    {reason === "OTHER" && <label>Descreva o motivo<textarea aria-label="Descreva o motivo" value={reasonText} maxLength={500} onChange={(event) => setReasonText(event.target.value)} required /></label>}
    <Button type="submit" disabled={incomplete || submitting}>Confirmar rejeição</Button>
  </form>;
}

/**
 * editItemPatch builds the edit patch from the form values. A blank material
 * or color is sent as an empty string and a blank quantity as clearQuantity,
 * because the API reads null as "keep the current value".
 */
export function editItemPatch(item: ReviewItem, values: { name: string; typeCode: string; condition: string; material: string; color: string; quantity: string }): G.InventoryItemPatchInput {
  const quantity = values.quantity.trim();
  return {
    name: values.name.trim(), typeCode: values.typeCode, condition: values.condition,
    material: values.material.trim(), color: values.color.trim(),
    quantity: quantity ? Number(quantity) : null, clearQuantity: quantity ? null : true,
    observation: item.observation, limitation: item.limitation, environmentId: item.environmentId,
  };
}

/** EditItemForm edits the descriptive fields of one item. */
export function EditItemForm({ item, onSubmit }: { item?: ReviewItem; onSubmit: (patch: G.InventoryItemPatchInput) => Promise<void> }) {
  const [name, setName] = useState(item?.name ?? "");
  const [typeCode, setTypeCode] = useState(item?.typeCode ?? "OTHER");
  const [condition, setCondition] = useState(item?.condition ?? "GOOD");
  const [material, setMaterial] = useState(item?.material ?? "");
  const [color, setColor] = useState(item?.color ?? "");
  const [quantity, setQuantity] = useState(item?.quantity?.toString() ?? "");
  const [submitting, setSubmitting] = useState(false);
  useEffect(() => {
    setName(item?.name ?? "");
    setTypeCode(item?.typeCode ?? "OTHER");
    setCondition(item?.condition ?? "GOOD");
    setMaterial(item?.material ?? "");
    setColor(item?.color ?? "");
    setQuantity(item?.quantity?.toString() ?? "");
  }, [item?.id]);
  return <form className="baseline-edit-form" onSubmit={(event) => {
    event.preventDefault();
    if (!item || !name.trim()) return;
    setSubmitting(true);
    void onSubmit(editItemPatch(item, { name, typeCode, condition, material, color, quantity })).finally(() => setSubmitting(false));
  }}>
    <Field label="Nome do item"><Input required maxLength={80} value={name} onChange={(event) => setName(event.target.value)} /></Field>
    <Field label="Tipo do item"><Select value={typeCode} onChange={(event) => setTypeCode(event.target.value)}>{itemTypeOptions.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field>
    <Field label="Condição"><Select value={condition} onChange={(event) => setCondition(event.target.value)}>{conditionOptions.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field>
    <Field label="Material"><Input maxLength={200} value={material} onChange={(event) => setMaterial(event.target.value)} /></Field>
    <Field label="Cor"><Input maxLength={100} value={color} onChange={(event) => setColor(event.target.value)} /></Field>
    <Field label="Quantidade"><Input type="number" min={1} max={999} step={1} value={quantity} onChange={(event) => setQuantity(event.target.value)} /></Field>
    <Button type="submit" disabled={!item || !name.trim() || submitting}>Salvar alterações</Button>
  </form>;
}

/** MergeItemsForm selects 2 to 10 items of one environment to merge. */
export function MergeItemsForm({ items, currentId, onSubmit }: { items: ReviewItem[]; currentId: string; onSubmit: (itemIds: string[]) => Promise<void> }) {
  const [selected, setSelected] = useState<string[]>([currentId]);
  const [busy, setBusy] = useState(false);
  const valid = selected.length >= 2 && selected.length <= 10;
  return <form onSubmit={(event) => {
    event.preventDefault();
    if (!valid) return;
    setBusy(true);
    void onSubmit(selected).finally(() => setBusy(false));
  }}>
    <p>Selecione de 2 a 10 itens do mesmo ambiente para formar um único item.</p>
    <fieldset><legend>Itens deste ambiente</legend>
      {items.filter((item) => item.decision !== "REJECTED").map((item) => <label className="baseline-choice" key={item.id}>
        <input type="checkbox" checked={selected.includes(item.id)} onChange={(event) => setSelected((current) => event.target.checked ? [...current, item.id] : current.filter((id) => id !== item.id))} />{item.name}
      </label>)}
    </fieldset>
    <Button type="submit" disabled={busy || !valid}>Mesclar itens selecionados</Button>
  </form>;
}

/** SplitItemForm divides one item into two parts and distributes its evidence. */
export function SplitItemForm({ item, onSubmit }: { item?: ReviewItem; onSubmit: (parts: G.InventorySplitPartInput[]) => Promise<void> }) {
  const [firstName, setFirstName] = useState(item ? `${item.name} — parte 1` : "");
  const [secondName, setSecondName] = useState(item ? `${item.name} — parte 2` : "");
  const [firstEvidence, setFirstEvidence] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setFirstName(item ? `${item.name} — parte 1` : "");
    setSecondName(item ? `${item.name} — parte 2` : "");
    setFirstEvidence([]);
  }, [item?.id]);
  return <form onSubmit={(event) => {
    event.preventDefault();
    if (!item || !firstName.trim() || !secondName.trim()) return;
    setBusy(true);
    const first = new Set(firstEvidence);
    void onSubmit([
      { name: firstName.trim(), typeCode: item.typeCode, condition: item.condition, evidenceIds: firstEvidence },
      { name: secondName.trim(), typeCode: item.typeCode, condition: item.condition, evidenceIds: item.evidence.filter((evidence) => !first.has(evidence.id)).map((evidence) => evidence.id) },
    ]).finally(() => setBusy(false));
  }}>
    <p>Defina dois nomes. Cada evidência marcada ficará na primeira parte; as demais ficarão na segunda.</p>
    <Field label="Nome da primeira parte"><Input required maxLength={80} value={firstName} onChange={(event) => setFirstName(event.target.value)} /></Field>
    <Field label="Nome da segunda parte"><Input required maxLength={80} value={secondName} onChange={(event) => setSecondName(event.target.value)} /></Field>
    <fieldset><legend>Evidências da primeira parte</legend>
      {item?.evidence.map((evidence) => <label className="baseline-choice" key={evidence.id}>
        <input type="checkbox" checked={firstEvidence.includes(evidence.id)} onChange={(event) => setFirstEvidence((current) => event.target.checked ? [...current, evidence.id] : current.filter((id) => id !== evidence.id))} />
        {evidence.kind === "VIDEO_FRAME" ? "Quadro de vídeo" : "Foto"}{evidence.timestampMs != null ? ` · ${formatEvidenceTime(evidence.timestampMs)}` : ""}
      </label>)}
    </fieldset>
    <Button type="submit" disabled={!item || !firstName.trim() || !secondName.trim() || busy}>Dividir item</Button>
  </form>;
}

/** EnvironmentForm adds an expected environment or edits an existing one. */
export function EnvironmentForm({ environment, onSubmit }: { environment?: ReviewEnvironment; onSubmit: (value: { name: string; typeCode: string }) => Promise<void> }) {
  const [name, setName] = useState(environment?.name ?? "");
  const [typeCode, setTypeCode] = useState(environment?.typeCode ?? "OTHER");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setName(environment?.name ?? "");
    setTypeCode(environment?.typeCode ?? "OTHER");
  }, [environment?.id]);
  return <form onSubmit={(event) => {
    event.preventDefault();
    if (!name.trim()) return;
    setBusy(true);
    void onSubmit({ name: name.trim(), typeCode }).finally(() => setBusy(false));
  }}>
    <Field label="Nome do ambiente"><Input required maxLength={80} value={name} onChange={(event) => setName(event.target.value)} /></Field>
    <Field label="Tipo do ambiente"><Select value={typeCode} onChange={(event) => setTypeCode(event.target.value)}>{environmentTypeOptions.map(([code, label]) => <option key={code} value={code}>{label}</option>)}</Select></Field>
    <Button type="submit" disabled={!name.trim() || busy}>{environment ? "Salvar ambiente" : "Adicionar ambiente"}</Button>
  </form>;
}
