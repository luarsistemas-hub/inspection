"use client";

import { Button, Field, Input, Status } from "@inspection/design-system";
import Image from "next/image";
import { useEffect, useState, type Dispatch, type SetStateAction } from "react";

export type OriginUpload = { id: string; file: File; description: string; attentionItems: string[]; mediaId?: string; sending: boolean; failed: boolean };

const maxAttentionItems = 20;
const maxAttentionItemLength = 80;

export function OriginUploadCards({ uploads, onChange, onRetry }: { uploads: OriginUpload[]; onChange: Dispatch<SetStateAction<OriginUpload[]>>; onRetry: (id: string) => void }) {
  const addFiles = (files: FileList | null) => {
    if (!files) return;
    const selected = Array.from(files).filter((file) => /^image\/(jpeg|png|webp|heic|heif)$/.test(file.type));
    onChange((current) => [...current, ...selected.map((file) => ({ id: crypto.randomUUID(), file, description: "", attentionItems: [], sending: false, failed: false }))]);
  };
  return <div className="onboarding-upload-list">
    <Field label="Fotos de referência" hint="Cada foto precisa de uma descrição antes de ser enviada.">
      <Input type="file" accept="image/jpeg,image/png,image/webp,image/heic,image/heif" multiple onChange={(event) => { addFiles(event.target.files); event.target.value = ""; }} />
    </Field>
    {uploads.map((upload) => <UploadCard key={upload.id} upload={upload} onChange={(next) => onChange((current) => current.map((item) => item.id === next.id ? next : item))} onRemove={() => onChange((current) => current.filter((item) => item.id !== upload.id))} onRetry={() => onRetry(upload.id)} />)}
  </div>;
}

function UploadCard({ upload, onChange, onRemove, onRetry }: { upload: OriginUpload; onChange: (upload: OriginUpload) => void; onRemove: () => void; onRetry: () => void }) {
  const [preview, setPreview] = useState("");
  const [attentionOpen, setAttentionOpen] = useState(false);
  const [attentionValue, setAttentionValue] = useState("");
  const [attentionError, setAttentionError] = useState("");
  const locked = upload.sending || Boolean(upload.mediaId);
  useEffect(() => {
    const url = URL.createObjectURL(upload.file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [upload.file]);
  const addAttentionItem = () => {
    const item = attentionValue.trim();
    if (!item) return;
    if (Array.from(item).length > maxAttentionItemLength) {
      setAttentionError("Cada item pode ter no máximo 80 caracteres.");
      return;
    }
    if (upload.attentionItems.some((current) => current.toLocaleLowerCase() === item.toLocaleLowerCase())) {
      setAttentionError("Este item já foi adicionado.");
      return;
    }
    if (upload.attentionItems.length >= maxAttentionItems) {
      setAttentionError("Adicione no máximo 20 itens.");
      return;
    }
    onChange({ ...upload, attentionItems: [...upload.attentionItems, item] });
    setAttentionValue("");
    setAttentionError("");
  };
  return <article className="onboarding-upload">
    {preview ? <Image className="onboarding-upload-preview" src={preview} width={800} height={600} unoptimized alt={`Prévia de ${upload.file.name}`} /> : null}
    <div className="onboarding-upload-heading"><strong>{upload.file.name}</strong><button className="onboarding-attention-toggle" type="button" aria-label="Itens de atenção" aria-expanded={attentionOpen} disabled={locked} onClick={() => setAttentionOpen((current) => !current)}>•••</button></div>
    <Field label={`Descrição de ${upload.file.name}`} error={upload.failed ? "O envio falhou. Tente novamente." : undefined} required>
      <Input value={upload.description} maxLength={2000} disabled={locked} onChange={(event) => onChange({ ...upload, description: event.target.value })} />
    </Field>
    {attentionOpen ? <section className="onboarding-attention-panel" aria-label={`Itens de atenção de ${upload.file.name}`}>
      <p>Inclua objetos que merecem atenção especial nesta foto.</p>
      <div className="onboarding-attention-add"><Input aria-label="Adicionar item" value={attentionValue} maxLength={maxAttentionItemLength} placeholder="Cafeteira" disabled={locked} onChange={(event) => { setAttentionValue(event.target.value); setAttentionError(""); }} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); addAttentionItem(); } }} /><Button variant="secondary" disabled={locked || !attentionValue.trim()} onClick={addAttentionItem}>Adicionar</Button></div>
      {attentionError ? <p className="onboarding-attention-error" role="alert">{attentionError}</p> : null}
      {upload.attentionItems.length > 0 ? <ul className="onboarding-attention-items">{upload.attentionItems.map((item) => <li key={item}><span>{item}</span><button type="button" aria-label={`Remover ${item}`} disabled={locked} onClick={() => onChange({ ...upload, attentionItems: upload.attentionItems.filter((current) => current !== item) })}>×</button></li>)}</ul> : null}
    </section> : upload.attentionItems.length > 0 ? <small className="onboarding-attention-summary">{upload.attentionItems.length} {upload.attentionItems.length === 1 ? "item de atenção" : "itens de atenção"}</small> : null}
    <div className="onboarding-upload-actions">
      <Status tone={upload.failed ? "danger" : upload.mediaId ? "success" : upload.sending ? "warning" : "info"}>{upload.failed ? "Falha no envio" : upload.mediaId ? "Foto recebida com segurança" : upload.sending ? "Enviando foto…" : "Aguardando envio privado"}</Status>
      {upload.failed ? <Button variant="secondary" onClick={onRetry} disabled={upload.sending}>Tentar enviar novamente</Button> : null}
      {!upload.mediaId && !upload.sending ? <Button variant="secondary" onClick={onRemove}>Remover</Button> : null}
    </div>
  </article>;
}
