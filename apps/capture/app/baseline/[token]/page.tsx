"use client";

import { useEffect, useState } from "react";
import { Alert, Button, Card, Field, Input, ProductIdentity, Status, Textarea, ThemeSelector } from "@inspection/design-system";
import { clearCounterpartyCsrfToken, setCounterpartyCsrfToken } from "@/auth/counterparty-session";
import { counterpartyGraphql, counterpartyOperations } from "@/graphql/counterparty-client";
import { throwOnUserErrors } from "@/pwa/mutation-errors";
import { CONDITION_LABELS, OBSERVATION_LABELS, PURPOSE_LABELS, contestStatementState, counterpartyCanRespond, counterpartyErrorMessage, counterpartyOutcome, evidenceCaption } from "@/pwa/counterparty-presentation";

type Props = { params: Promise<{ token: string }> };
const mutationID = () => crypto.randomUUID();
export default function CounterpartyBaselinePage({ params }: Props) {
  const [token, setToken] = useState("");
  const [code, setCode] = useState("");
  const [stage, setStage] = useState<"otp" | "baseline" | "done">("otp");
  const [notice, setNotice] = useState("");
  const [statement, setStatement] = useState("");
  const [busy, setBusy] = useState(false);
  const [data, setData] = useState<Awaited<ReturnType<typeof loadBaseline>>>();
  useEffect(() => { void params.then(({ token: routeToken }) => setToken(routeToken)).catch(() => setNotice("Este convite não está disponível.")); return () => clearCounterpartyCsrfToken(); }, [params]);
  async function requestOtp(linkToken = token) {
    try {
      const result = await counterpartyGraphql(counterpartyOperations.requestOtp, { input: { token: linkToken, clientMutationId: mutationID() } });
      throwOnUserErrors(result.requestCounterpartyOtp);
      setNotice("Enviamos um código de acesso. Confira sua caixa de entrada.");
    } catch (error) { setNotice(counterpartyErrorMessage(error)); }
  }
  async function loadBaseline() {
    const result = await counterpartyGraphql(counterpartyOperations.baseline);
    return result.counterpartyBaseline;
  }
  const verify = async () => {
    if (!code.trim() || busy) return;
    setBusy(true);
    try {
      const result = await counterpartyGraphql(counterpartyOperations.verifyOtp, { input: { token, code, clientMutationId: mutationID() } });
      throwOnUserErrors(result.verifyCounterpartyOtp);
      setCounterpartyCsrfToken(result.verifyCounterpartyOtp.csrfToken);
      setData(await loadBaseline()); setStage("baseline"); setNotice("");
    } catch (error) { setNotice(counterpartyErrorMessage(error)); }
    finally { setBusy(false); }
  };
  const acknowledge = async () => {
    setBusy(true);
    try {
      const result = await counterpartyGraphql(counterpartyOperations.acknowledge, { input: { clientMutationId: mutationID() } });
      throwOnUserErrors(result.acknowledgeBaseline); setStage("done"); setNotice("Sua confirmação foi registrada.");
    } catch (error) { await handleResponseError(error); }
    finally { setBusy(false); }
  };
  const contest = async () => {
    if (!contestStatementState(statement).valid || busy) return;
    setBusy(true);
    try {
      const result = await counterpartyGraphql(counterpartyOperations.contest, { input: { statement, clientMutationId: mutationID() } });
      throwOnUserErrors(result.contestBaseline); setStage("done"); setNotice("Sua manifestação foi registrada.");
    } catch (error) { await handleResponseError(error); }
    finally { setBusy(false); }
  };
  async function handleResponseError(error: unknown) {
    if (typeof error === "object" && error !== null && "code" in error && String(error.code) === "INVALID_STATE") {
      try { setData(await loadBaseline()); setStage("baseline"); } catch { /* Keep the current snapshot if refresh is unavailable. */ }
    }
    setNotice(counterpartyErrorMessage(error));
  }
  const readOnly = Boolean(data?.readOnly);
  const alreadyResponded = Boolean(data?.response && data.response !== "PENDING");
  const canRespond = counterpartyCanRespond(readOnly, data?.response);
  const statementState = contestStatementState(statement);
  const outcome = data ? counterpartyOutcome(data) : null;
  const dateTime = new Intl.DateTimeFormat("pt-BR", { dateStyle: "long", timeStyle: "short" });
  return <main className="capture-shell"><div className="capture-workspace"><header className="capture-intro"><div className="capture-utility-bar"><ProductIdentity product="Capture" /><ThemeSelector /></div><h1>Conferir vistoria</h1></header>
    {stage === "otp" && <Card className="capture-card"><h2>Confirme seu acesso</h2><p>Use o código de seis dígitos enviado para liberar a consulta.</p><div className="capture-actions"><Button isPending={busy} disabled={busy || !token} onClick={() => void requestOtp()}>Enviar código</Button></div>{notice && <Alert tone="warning" aria-live="polite">{notice}</Alert>}{notice.includes("Enviamos") && <Field label="Código de seis dígitos"><Input aria-label="Código de seis dígitos" inputMode="numeric" maxLength={6} value={code} onChange={(event) => setCode(event.target.value)} /></Field>}{notice.includes("Enviamos") && <div className="capture-actions"><Button isPending={busy} disabled={busy || !code.trim()} onClick={() => void verify()}>Confirmar código</Button><Button variant="secondary" disabled={busy} onClick={() => void requestOtp()}>Solicitar outro código</Button></div>}</Card>}
    {stage === "baseline" && data && <Card className="capture-card capture-card--wide"><h2>Vistoria de referência</h2>{data.purpose && <p>Finalidade: {PURPOSE_LABELS[data.purpose] ?? "Finalidade registrada"}</p>}{data.activatedAt && <p>Publicada em: {dateTime.format(new Date(data.activatedAt))}</p>}<p>Prazo para resposta: {dateTime.format(new Date(data.deadlineAt))}</p>{data.newerVersionExists && <Alert tone="warning">Há uma versão mais recente desta vistoria.</Alert>}{readOnly && <Status tone="warning">Prazo encerrado</Status>}{alreadyResponded && outcome && <Status tone={outcome.tone}>{outcome.message}</Status>}
      {data.baseline.environments.map((environment, index) => <section key={`${environment.typeCode}:${environment.name}:${index}`}><h3>{environment.name}</h3><ul>{environment.items.map((item, itemIndex) => <li key={`${item.typeCode}:${item.name}:${itemIndex}`}><strong>{item.name}</strong>{item.quantity ? ` · ${item.quantity}` : ""} · {CONDITION_LABELS[item.condition] ?? "Condição registrada"} · {OBSERVATION_LABELS[item.observation] ?? "Observação registrada"}{item.material ? ` · ${item.material}` : ""}{item.color ? ` · ${item.color}` : ""}{item.limitation ? <p>Limitação: {item.limitation}</p> : null}{item.evidence.length > 0 && <div className="counterparty-evidence">{item.evidence.map((evidence, evidenceIndex) => <figure key={`${evidence.kind}:${evidence.position}:${evidenceIndex}`}>{evidence.url ? <a href={evidence.url} target="_blank" rel="noreferrer"><img src={evidence.url} alt={`${evidenceCaption(evidence, evidenceIndex)} de ${item.name}`} loading="lazy" /></a> : <span>Evidência indisponível</span>}<figcaption>{evidenceCaption(evidence, evidenceIndex)}</figcaption></figure>)}</div>}</li>)}</ul></section>)}
      {canRespond && <><div className="capture-actions"><Button disabled={busy} onClick={() => void acknowledge()}>Confirmar ciência</Button></div><Field label="Se não concordar, escreva sua manifestação" hint="Até 4.000 caracteres. Anexos não são aceitos."><Textarea aria-label="Manifestação" value={statement} aria-describedby="counterparty-statement-counter counterparty-statement-error" onChange={(event) => setStatement(event.target.value)} /></Field><p id="counterparty-statement-counter" aria-live="polite">{statementState.count.toLocaleString("pt-BR")} / 4.000 caracteres</p><p id="counterparty-statement-error" role={statementState.overLimit ? "alert" : undefined} hidden={!statementState.overLimit}>A manifestação deve ter no máximo 4.000 caracteres.</p><Button disabled={busy || !statementState.valid} isPending={busy} onClick={() => void contest()}>Enviar manifestação</Button></>}
    </Card>}
    {stage === "done" && <Card className="capture-card"><Status tone="success">Resposta registrada</Status><h2>Obrigado</h2><p>{notice}</p>{data?.newerVersionExists && <Alert tone="warning">Há uma versão mais recente desta vistoria.</Alert>}</Card>}
    {notice && stage !== "done" && <Alert tone="warning" aria-live="polite">{notice}</Alert>}
    <p className="capture-privacy-note">Acesso pessoal vinculado ao convite. Não compartilhe este link.</p></div></main>;
}
