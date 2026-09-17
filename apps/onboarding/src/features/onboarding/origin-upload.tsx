"use client";

import { Button, Field, Input, Status } from "@inspection/design-system";
import Image from "next/image";
import { useEffect, useState, type Dispatch, type SetStateAction } from "react";

export type OriginUpload = { id: string; file: File; description: string; mediaId?: string; sending: boolean; failed: boolean };

export function OriginUploadCards({ uploads, onChange, onRetry }: { uploads: OriginUpload[]; onChange: Dispatch<SetStateAction<OriginUpload[]>>; onRetry: (id: string) => void }) {
  const addFiles = (files: FileList | null) => {
    if (!files) return;
    const selected = Array.from(files).filter((file) => /^image\/(jpeg|png|webp|heic|heif)$/.test(file.type));
    onChange((current) => [...current, ...selected.map((file) => ({ id: crypto.randomUUID(), file, description: "", sending: false, failed: false }))]);
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
  useEffect(() => {
    const url = URL.createObjectURL(upload.file);
    setPreview(url);
    return () => URL.revokeObjectURL(url);
  }, [upload.file]);
  return <article className="onboarding-upload">
    {preview ? <Image className="onboarding-upload-preview" src={preview} width={800} height={600} unoptimized alt={`Prévia de ${upload.file.name}`} /> : null}
    <strong>{upload.file.name}</strong>
    <Field label={`Descrição de ${upload.file.name}`} error={upload.failed ? "O envio falhou. Tente novamente." : undefined} required>
      <Input value={upload.description} maxLength={2000} disabled={upload.sending || Boolean(upload.mediaId)} onChange={(event) => onChange({ ...upload, description: event.target.value })} />
    </Field>
    <div className="onboarding-upload-actions">
      <Status tone={upload.failed ? "danger" : upload.mediaId ? "success" : upload.sending ? "warning" : "info"}>{upload.failed ? "Falha no envio" : upload.mediaId ? "Foto recebida com segurança" : upload.sending ? "Enviando foto…" : "Aguardando envio privado"}</Status>
      {upload.failed ? <Button variant="secondary" onClick={onRetry} disabled={upload.sending}>Tentar enviar novamente</Button> : null}
      {!upload.mediaId && !upload.sending ? <Button variant="secondary" onClick={onRemove}>Remover</Button> : null}
    </div>
  </article>;
}
