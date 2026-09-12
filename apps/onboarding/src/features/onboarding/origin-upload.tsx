"use client";

import { Button, Field, Input, Status } from "@inspection/design-system";
import { useState } from "react";

export type OriginUpload = { id: string; file: File; description: string; progress: number; failed: boolean };

export function OriginUploadCards({ uploads, onChange }: { uploads: OriginUpload[]; onChange: (uploads: OriginUpload[]) => void }) {
  const addFiles = (files: FileList | null) => {
    if (!files) return;
    onChange([...uploads, ...Array.from(files).filter((file) => file.type.startsWith("image/")).map((file) => ({ id: crypto.randomUUID(), file, description: "", progress: 0, failed: false }))]);
  };
  return <div className="onboarding-upload-list">
    <Field label="Fotos de referência" hint="Cada foto precisa de uma descrição antes de ser enviada.">
      <Input type="file" accept="image/*" multiple onChange={(event) => addFiles(event.target.files)} />
    </Field>
    {uploads.map((upload) => <UploadCard key={upload.id} upload={upload} onChange={(next) => onChange(uploads.map((item) => item.id === next.id ? next : item))} />)}
  </div>;
}

function UploadCard({ upload, onChange }: { upload: OriginUpload; onChange: (upload: OriginUpload) => void }) {
  const [sending, setSending] = useState(false);
  const retry = () => { setSending(true); onChange({ ...upload, failed: false, progress: 20 }); window.setTimeout(() => { onChange({ ...upload, failed: false, progress: 100 }); setSending(false); }, 250); };
  return <article className="onboarding-upload">
    <strong>{upload.file.name}</strong>
    <progress value={upload.progress} max="100" aria-label={`Progresso de ${upload.file.name}`} />
    <Field label={`Descrição de ${upload.file.name}`} error={upload.failed ? "O envio falhou. Tente novamente." : undefined} required>
      <Input value={upload.description} onChange={(event) => onChange({ ...upload, description: event.target.value })} />
    </Field>
    {upload.failed ? <Button onClick={retry} disabled={sending}>Tentar enviar novamente</Button> : <Status tone={upload.progress === 100 ? "warning" : "info"}>{upload.progress === 100 ? "Pronta para confirmar" : "Aguardando envio privado"}</Status>}
  </article>;
}
