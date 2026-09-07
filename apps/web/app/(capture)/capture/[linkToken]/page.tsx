"use client";

import { ChangeEvent, useEffect, useMemo, useState } from "react";
import { captureOperations, graphql } from "@/graphql/client";
import { CaptureDraft, digest, loadDraftsForResponsibility, readyForSubmission, removeDraft, saveDraft } from "@/pwa/drafts";
import { uploadDraft } from "@/pwa/uploads";
import { ptBR } from "@/locales/pt-BR";
import { RegisterServiceWorker } from "@/components/register-service-worker";
import { clearCaptureCsrfToken, setCaptureCsrfToken } from "@/auth/capture-session";

type Props = { params: Promise<{ linkToken: string }> };
type Stage = "otp" | "consent" | "capture" | "confirmation" | "error";
type GPS = { latitude: number; longitude: number; accuracyMeters: number; capturedAt: string; windowStartedAt: string };
type Requirement = { key: string; section: string; label: string; instructions?: string | null; required: boolean; descriptionRequired: boolean; impossibilityAllowed: boolean };
type Bootstrap = { externalCapture: { responsibilityId: string; recaptureRequestId?: string | null; kind: string; confirmationOnly: boolean; disclosureVersion: string; requirements: Requirement[] } };
const mutationID = () => crypto.randomUUID();

function requestGPS(): Promise<GPS> {
  const windowStartedAt = new Date().toISOString();
  return new Promise((resolve, reject) => {
    if (!navigator.geolocation) return reject(new Error("Este navegador não oferece localização."));
    navigator.geolocation.getCurrentPosition(
      (position) => {
        if (position.coords.accuracy > 50) return reject(new Error("A precisão da localização precisa ser de até 50 metros."));
        resolve({ latitude: position.coords.latitude, longitude: position.coords.longitude, accuracyMeters: position.coords.accuracy, capturedAt: new Date(position.timestamp).toISOString(), windowStartedAt });
      },
      () => reject(new Error("Não foi possível obter sua localização.")),
      { enableHighAccuracy: true, maximumAge: 0, timeout: 60_000 }
    );
  });
}

export default function CapturePage({ params }: Props) {
  const [stage, setStage] = useState<Stage>("otp");
  const [token, setToken] = useState("");
  const [code, setCode] = useState("");
  const [drafts, setDrafts] = useState<CaptureDraft[]>([]);
  const [requirements, setRequirements] = useState<Requirement[]>([]);
  const [responsibilityId, setResponsibilityId] = useState("");
  const [recaptureRequestId, setRecaptureRequestId] = useState<string>();
  const [captureKind, setCaptureKind] = useState("INSPECTION");
  const [disclosureVersion, setDisclosureVersion] = useState("");
  const [online, setOnline] = useState(true);
  const [notice, setNotice] = useState("");
  const [selected, setSelected] = useState("");
  const [description, setDescription] = useState("");
  const [reason, setReason] = useState("");
  const [source, setSource] = useState<"camera" | "gallery">("camera");
  const [gps, setGPS] = useState<GPS>();
  const [busy, setBusy] = useState(false);

  const refreshDrafts = async (scope = responsibilityId) => { if (scope) setDrafts(await loadDraftsForResponsibility(scope)); };

  useEffect(() => {
    void params.then(async ({ linkToken }) => {
      setToken(linkToken);
      history.replaceState(null, "", "/capture/acesso");
      const result = await graphql<{ requestInvitationOtp: { status: string; userErrors: Array<{ message: string }> } }>("capture", captureOperations.exchange, { linkToken, id: mutationID() });
      const failure = result.requestInvitationOtp.userErrors[0];
      if (failure) throw new Error(failure.message);
    }).catch(() => { setStage("error"); setNotice("Este convite não está disponível. Solicite um novo link."); });
  }, [params]);

  useEffect(() => {
    setOnline(navigator.onLine);
    const update = () => setOnline(navigator.onLine);
    addEventListener("online", update); addEventListener("offline", update);
    return () => { removeEventListener("online", update); removeEventListener("offline", update); };
  }, []);

  const pending = useMemo(() => drafts.filter((draft) => !draft.parts.every((part) => part.complete)).length, [drafts]);

  const verify = async () => {
    try {
      const data = await graphql<{ verifyInvitationOtp: { csrfToken: string; userErrors: Array<{ message: string }> } }>("capture", captureOperations.verifyOTP, { linkToken: token, code, id: mutationID() });
      const failure = data.verifyInvitationOtp.userErrors[0];
      if (failure) throw new Error(failure.message);
      setCaptureCsrfToken(data.verifyInvitationOtp.csrfToken);
      setStage("consent");
    } catch { setNotice("Código inválido ou expirado. Confira o código recebido."); }
  };

  const accept = async () => {
    try {
      const result = await graphql<{ acceptProcessing: { userErrors: Array<{ message: string }> } }>("capture", captureOperations.accept, { disclosureVersion: disclosureVersion || "pt-BR-1", photoProcessing: true, aiAnalysis: true, gpsUse: true, id: mutationID() });
      if (result.acceptProcessing.userErrors.length) throw new Error(result.acceptProcessing.userErrors[0].message);
      const capture = await graphql<Bootstrap>("capture", captureOperations.bootstrap);
      const external = capture.externalCapture;
      setDisclosureVersion(external.disclosureVersion);
      if (external.confirmationOnly) { setStage("confirmation"); return; }
      setResponsibilityId(external.responsibilityId); setRecaptureRequestId(external.recaptureRequestId ?? undefined); setCaptureKind(external.kind); setRequirements(external.requirements); setSelected(external.requirements[0]?.key ?? "");
      await refreshDrafts(external.responsibilityId); setStage("capture");
    } catch { setNotice("Não foi possível registrar o aceite. Tente novamente."); }
  };

  const registerGPS = async () => {
    try { setGPS(await requestGPS()); setNotice("Localização registrada para a próxima evidência."); }
    catch (error) { setNotice(error instanceof Error ? error.message : "Não foi possível registrar a localização."); }
  };

  const resume = async (draft: CaptureDraft) => {
    if (!online) { setNotice("Conecte-se para retomar o envio."); return; }
    setBusy(true);
    try { await uploadDraft(draft, draft.metadata.gps); await refreshDrafts(); setNotice("Evidência enviada e verificada."); }
    catch (error) { setNotice(error instanceof Error ? error.message : "O envio será retomado quando houver conexão."); }
    finally { setBusy(false); }
  };

  const addFile = async (event: ChangeEvent<HTMLInputElement>) => {
    const file = event.target.files?.[0];
    if (!file || !selected || !responsibilityId) return;
    if (!/^image\/(jpeg|png|webp|heic|heif)$/.test(file.type) || file.size > 20 * 1024 * 1024) { setNotice("Use JPEG, PNG, WebP, HEIC ou HEIF de até 20 MB."); return; }
    const draft: CaptureDraft = { id: mutationID(), responsibilityId, blob: file, sha256: await digest(file), parts: [], metadata: { requirementKey: selected, description, source, capturedAt: new Date().toISOString(), gps }, answer: { progress: 0 } };
    await saveDraft(draft); await refreshDrafts(); setDescription("");
    if (online) await resume(draft); else setNotice("Foto salva neste dispositivo. O envio será retomado quando houver conexão.");
  };

  const declareImpossible = async () => {
    if (!selected || !reason.trim()) { setNotice("Explique por que esta evidência não pode ser registrada."); return; }
    try { await graphql("capture", captureOperations.impossibility, { key: selected, reason, id: mutationID() }); setReason(""); setNotice("Justificativa registrada."); }
    catch { setNotice("Não foi possível registrar a justificativa."); }
  };

  const submit = async (incomplete: boolean) => {
    if (!readyForSubmission(drafts, online)) { setNotice(!online ? "Conecte-se para verificar e enviar a inspeção." : "Aguarde todas as fotos ficarem prontas antes de enviar."); return; }
    try {
      if (captureKind === "RECAPTURE") {
        if (!recaptureRequestId) throw new Error("A solicitação de recaptura não está disponível.");
        const result = await graphql<{ submitRecapture: { userErrors: Array<{ message: string }> } }>("capture", captureOperations.submitRecapture, { requestId: recaptureRequestId, confirmIncomplete: incomplete, id: mutationID() });
        if (result.submitRecapture.userErrors[0]) throw new Error(result.submitRecapture.userErrors[0].message);
      } else {
        const result = await graphql<{ submitCapture: { userErrors: Array<{ message: string }> } }>("capture", captureOperations.submit, { confirmIncomplete: incomplete, id: mutationID() });
        if (result.submitCapture.userErrors[0]) throw new Error(result.submitCapture.userErrors[0].message);
      }
      await Promise.all(drafts.map((draft) => removeDraft(draft.id))); setDrafts([]); setStage("confirmation"); clearCaptureCsrfToken();
    } catch { setNotice("Não foi possível confirmar o envio. Seus dados continuam protegidos neste dispositivo."); }
  };

  return <main><RegisterServiceWorker /><h1>Registrar inspeção</h1><p className="warning">{ptBR.sensitiveWarning}</p>{!online && <p className="card" role="status">{ptBR.offline}</p>}
    {stage === "otp" && <section className="card"><h2>Confirme seu acesso</h2><label>Código de seis dígitos <input inputMode="numeric" pattern="[0-9]{6}" maxLength={6} value={code} onChange={(event) => setCode(event.target.value)} /></label><button onClick={() => void verify()}>Confirmar código</button></section>}
    {stage === "consent" && <section className="card"><h2>Uso dos seus dados</h2><p>Suas fotos, a análise assistida e a localização são usadas nesta inspeção, conforme a retenção informada pelo responsável. Não há decisão automática sobre você.</p><button onClick={() => void accept()}>Aceito o processamento necessário</button><button onClick={() => setStage("error")}>Não aceito</button></section>}
    {stage === "capture" && <section className="card"><h2>Captura guiada</h2><p>{ptBR.advisory}</p><p className="status">{drafts.length - pending} pronta(s), {pending} pendente(s). A confirmação exige conexão e cada mídia verificada.</p><label>Requisito <select aria-label="Requisito" value={selected} onChange={(event) => setSelected(event.target.value)}>{requirements.map((requirement) => <option key={requirement.key} value={requirement.key}>{requirement.section}: {requirement.label}</option>)}</select></label>{requirements.find((requirement) => requirement.key === selected)?.instructions && <p>{requirements.find((requirement) => requirement.key === selected)?.instructions}</p>}<label>Descrição da foto <textarea value={description} onChange={(event) => setDescription(event.target.value)} required /></label><fieldset><legend>Origem da foto</legend><label><input type="radio" checked={source === "camera"} onChange={() => setSource("camera")} /> Câmera</label><label><input type="radio" checked={source === "gallery"} onChange={() => setSource("gallery")} /> Galeria</label></fieldset><button type="button" onClick={() => void registerGPS()}>Usar minha localização</button>{gps && <p role="status">Localização com precisão de {Math.round(gps.accuracyMeters)} metros registrada.</p>}<label>Adicionar foto <input aria-label="Adicionar foto" type="file" accept="image/jpeg,image/png,image/webp,image/heic,image/heif" capture={source === "camera" ? "environment" : undefined} disabled={!description.trim() || busy} onChange={(event) => void addFile(event)} /></label>{drafts.filter((draft) => !draft.parts.every((part) => part.complete)).map((draft) => <button key={draft.id} disabled={!online || busy} onClick={() => void resume(draft)}>Retomar envio pendente</button>)}<label>Impossibilidade <textarea value={reason} onChange={(event) => setReason(event.target.value)} /></label><button onClick={() => void declareImpossible()}>Registrar impossibilidade</button><button disabled={!online || busy} onClick={() => void submit(false)}>Enviar inspeção completa</button><button disabled={!online || busy} onClick={() => void submit(true)}>Confirmar envio incompleto</button></section>}
    {stage === "confirmation" && <section className="card"><h2>Recebemos sua confirmação</h2><p>Obrigado. Esta área não exibe fotos enviadas, resultados internos nem relatórios.</p></section>}
    {stage === "error" && <section className="card"><h2>Acesso indisponível</h2><p>{notice || "O aceite é necessário para continuar."}</p></section>}
    {notice && stage !== "error" && <p role="status" className="card">{notice}</p>}
  </main>;
}
