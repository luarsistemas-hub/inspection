"use client";

import React, { useId, useState } from "react";
import { Button, Checkbox } from "@inspection/design-system";
import { presentVoiceError, voiceConsentVersion } from "@/pwa/audio-notes-state";

type Props = {
  /** Records the separate voice authorization; resolves when the server confirmed it. */
  onEnable: () => Promise<void>;
  /** Where the participant is, so the copy names photos or room clips. */
  subject: "foto" | "clipe";
};

/** Lets a participant who declined voice enable it later, with the same disclosure and a fresh, explicit choice. */
export function VoiceOptIn({ onEnable, subject }: Props) {
  const headingId = useId();
  const detailsId = useId();
  const [open, setOpen] = useState(false);
  const [accepted, setAccepted] = useState(false);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState("");

  const enable = async () => {
    if (pending || !accepted) return;
    setPending(true); setError("");
    try { await onEnable(); }
    catch (failure) { setError(presentVoiceError(failure, "Não foi possível registrar a autorização de voz. Fotos e vídeos continuam disponíveis; tente novamente.")); }
    finally { setPending(false); }
  };

  return <section className="capture-voice-notes capture-voice-optin" aria-labelledby={headingId}>
    <h3 id={headingId}>Anotações de voz (opcional)</h3>
    <p>Você pode gravar uma anotação curta para explicar cada {subject}. Seguir sem voz não impede nenhuma captura nem o envio.</p>
    <Button type="button" variant="secondary" aria-expanded={open} aria-controls={detailsId} onClick={() => setOpen((value) => !value)}>{open ? "Ocultar detalhes da voz" : "Ativar anotações de voz"}</Button>
    {open && <div id={detailsId} className="capture-voice-optin__details">
      <p>O microfone só será solicitado quando você iniciar uma anotação, a cada gravação. O áudio temporário é enviado para transcrição e removido da nossa cópia após a confirmação de recebimento durável. O texto poderá ser revisado por você. A exclusão da nossa cópia não controla a retenção contratual do provedor.</p>
      <Checkbox checked={accepted} label="Permitir anotações de voz (opcional)" onChange={(event) => setAccepted(event.target.checked)} />
      <p className="capture-voice-optin__version">Autorização de voz, versão {voiceConsentVersion}. A autorização das fotos e dos vídeos não vale para a voz.</p>
      <Button type="button" disabled={!accepted} isPending={pending} pendingLabel="Registrando autorização…" onClick={() => void enable()}>Autorizar anotações de voz</Button>
      {error && <p role="alert" className="capture-voice-optin__error">{error}</p>}
    </div>}
  </section>;
}
