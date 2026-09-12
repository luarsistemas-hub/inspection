"use client";

import { Alert, Button, Card, Container, Field, Input, Select, Stack, Textarea } from "@inspection/design-system";
import { FormEvent, useEffect, useMemo, useState } from "react";
import { clearOnboardingSession } from "@/auth/onboarding-session";
import { clientMutationId, graphql, isOnboardingSessionFailure, mapUserErrors, type GraphQLFailure } from "@/graphql/client";
import { OnboardingDefinitionDocument, OnboardingSessionDocument, RequestOnboardingOtpDocument, SaveOnboardingStepDocument, VerifyOnboardingOtpDocument, type OnboardingDefinitionQuery, type OnboardingSessionQuery } from "@/graphql/generated";
import { isSupportedDefinition, resolveResume, sortedSteps, validateStep, type OnboardingDefinition, type StepValues } from "./definition";
import { OriginUploadCards, type OriginUpload } from "./origin-upload";

type View = "identity" | "verify" | "step" | "review" | "status";
type Session = NonNullable<OnboardingSessionQuery["onboardingSession"]>;
type Status = { state: string; nextAction: string; originStatus: string; deliveryStatus: string; inspectionId: string | null };

const failureText = (error: unknown) => (error as GraphQLFailure).message ?? "Não foi possível concluir a solicitação.";

function definitionFrom(result: OnboardingDefinitionQuery): OnboardingDefinition {
  return result.onboardingDefinition;
}

function draftKey(step: string) { return `inspection.onboarding.draft.${step}`; }
function readDraft(step: string) {
  try { return JSON.parse(sessionStorage.getItem(draftKey(step)) ?? "{}") as StepValues; } catch { return {}; }
}
function writeDraft(step: string, values: StepValues) { sessionStorage.setItem(draftKey(step), JSON.stringify(values)); }
function clearDrafts() { Object.keys(sessionStorage).filter((key) => key.startsWith("inspection.onboarding.draft.")).forEach((key) => sessionStorage.removeItem(key)); }

/** Public, cookie-backed onboarding journey. It only retains non-sensitive unfinished field values per tab. */
export function OnboardingJourney({ initialView }: { initialView?: "status" } = {}) {
  const [definition, setDefinition] = useState<OnboardingDefinition>();
  const [session, setSession] = useState<Session>();
  const [view, setView] = useState<View>(initialView ?? "identity");
  const [locator, setLocator] = useState("");
  const [activeStep, setActiveStep] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<Status>();

  useEffect(() => {
    void Promise.all([graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" }), graphql(OnboardingSessionDocument)])
      .then(([definitionResult, sessionResult]) => {
        const nextDefinition = definitionFrom(definitionResult);
        if (!isSupportedDefinition(nextDefinition)) throw { message: "Esta versão do cadastro não é compatível. Tente novamente mais tarde." } satisfies GraphQLFailure;
        setDefinition(nextDefinition);
        if (sessionResult.onboardingSession) {
          const serverSession = sessionResult.onboardingSession;
          setSession(serverSession);
          const resumed = resolveResume(serverSession, { step: serverSession.currentStep });
          setActiveStep(resumed ?? sortedSteps(nextDefinition)[0]?.key ?? "");
          setView(initialView ?? "step");
        }
      })
      .catch((cause: unknown) => { if (!isOnboardingSessionFailure(cause)) setError(failureText(cause)); })
      .finally(() => setLoading(false));
  }, [initialView]);

  const steps = useMemo(() => definition ? sortedSteps(definition) : [], [definition]);
  const step = steps.find((item) => item.key === activeStep) ?? steps[0];

  const restart = () => {
    clearOnboardingSession(); clearDrafts(); setSession(undefined); setLocator(""); setActiveStep(""); setStatus(undefined); setError(""); setView("identity");
  };

  if (loading) return <PageShell title="Preparando seu cadastro"><p>Carregando as etapas disponíveis…</p></PageShell>;
  if (error && !definition) return <PageShell title="Não foi possível abrir o cadastro"><Alert tone="danger">{error}</Alert><Button onClick={() => location.reload()}>Tentar novamente</Button></PageShell>;
  if (!definition) return null;

  return <PageShell title={view === "identity" ? "Comece sua primeira inspeção" : view === "verify" ? "Confirme seu e-mail" : view === "review" ? "Revise a solicitação" : view === "status" ? "Acompanhe a solicitação" : step?.label ?? "Cadastro"} steps={steps} currentStep={activeStep}>
    {error ? <Alert tone="danger">{error}</Alert> : null}
    {view === "identity" ? <IdentityForm onRequested={(nextLocator) => { setLocator(nextLocator); setError(""); setView("verify"); }} onError={setError} /> : null}
    {view === "verify" ? <VerificationForm locator={locator} onVerified={(nextSession) => { setSession(nextSession); const resumed = resolveResume(nextSession, { step: nextSession.currentStep }); setActiveStep(resumed ?? steps[0]?.key ?? ""); setError(""); setView("step"); }} onBack={() => setView("identity")} onError={setError} /> : null}
    {view === "step" && step && session ? <StepForm step={step} session={session} onSaved={(nextSession, nextStatus) => {
      setSession(nextSession); if (nextStatus) setStatus(nextStatus);
      const nextIndex = steps.findIndex((item) => item.key === step.key) + 1;
      const next = steps[nextIndex];
      if (next) setActiveStep(next.key); else setView("review");
    }} onReview={() => setView("review")} onError={setError} /> : null}
    {view === "review" ? <Review values={steps.flatMap((item) => Object.entries(readDraft(item.key)).map(([key, value]) => ({ label: `${item.label}: ${key}`, value })))} status={status} onBack={() => { setActiveStep(session?.currentStep ?? steps.at(-1)?.key ?? ""); setView("step"); }} onSubmit={() => setView("status")} /> : null}
    {view === "status" ? <StatusView status={status ?? statusFromSession(session)} onRestart={restart} /> : null}
  </PageShell>;
}

function PageShell({ title, children, steps = [], currentStep = "" }: { title: string; children: React.ReactNode; steps?: OnboardingDefinition["steps"]; currentStep?: string }) {
  return <main className="onboarding-shell"><Container className="onboarding-main"><header className="onboarding-header"><p>Inspection · Imobiliária</p><h1>{title}</h1><p>Seus dados ficam protegidos nesta sessão. Você pode continuar no mesmo navegador.</p>{steps.length ? <ol className="onboarding-progress" aria-label="Etapas do cadastro">{steps.map((step) => <li key={step.key} data-current={step.key === currentStep}>{step.label}</li>)}</ol> : null}</header><Card><Stack gap="4">{children}</Stack></Card></Container></main>;
}

function IdentityForm({ onRequested, onError }: { onRequested: (locator: string) => void; onError: (message: string) => void }) {
  const [name, setName] = useState(""); const [email, setEmail] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({});
  const submit = async (event: FormEvent) => { event.preventDefault(); setSubmitting(true); setErrors({}); onError("");
    try { const result = await graphql(RequestOnboardingOtpDocument, { input: { name, email, clientMutationId: clientMutationId() } }); const payload = result.requestOnboardingOtp; const nextErrors = mapUserErrors(payload.userErrors); if (Object.keys(nextErrors).length) { setErrors(nextErrors); return; } if (!payload.sessionLocator) throw { message: "Não foi possível iniciar a verificação." } satisfies GraphQLFailure; onRequested(payload.sessionLocator); } catch (cause) { onError(failureText(cause)); } finally { setSubmitting(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Informe seus dados para receber um código de confirmação.</p><Field label="Seu nome" error={errors.name} required><Input autoComplete="name" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Seu e-mail" error={errors.email} required><Input type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><Button type="submit" disabled={submitting}>{submitting ? "Enviando…" : "Enviar código"}</Button></form>;
}

function VerificationForm({ locator, onVerified, onBack, onError }: { locator: string; onVerified: (session: Session) => void; onBack: () => void; onError: (message: string) => void }) {
  const [code, setCode] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({});
  const submit = async (event: FormEvent) => { event.preventDefault(); setSubmitting(true); setErrors({}); onError(""); try { const result = await graphql(VerifyOnboardingOtpDocument, { input: { sessionLocator: locator, code, clientMutationId: clientMutationId() } }); const payload = result.verifyOnboardingOtp; const nextErrors = mapUserErrors(payload.userErrors); if (Object.keys(nextErrors).length) { setErrors(nextErrors); return; } if (!payload.session) throw { message: "Não foi possível confirmar o código." } satisfies GraphQLFailure; onVerified(payload.session); } catch (cause) { onError(failureText(cause)); } finally { setSubmitting(false); } };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Digite o código de seis dígitos enviado ao seu e-mail.</p><Field label="Código de confirmação" error={errors.code} required><Input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} /></Field><div className="onboarding-actions"><Button type="submit" disabled={submitting || code.length !== 6}>{submitting ? "Confirmando…" : "Confirmar e continuar"}</Button><Button variant="secondary" onClick={onBack}>Voltar</Button></div></form>;
}

function StepForm({ step, session, onSaved, onReview, onError }: { step: OnboardingDefinition["steps"][number]; session: Session; onSaved: (session: Session, status?: Status) => void; onReview: () => void; onError: (message: string) => void }) {
  const [values, setValues] = useState<StepValues>(() => readDraft(step.key)); const [errors, setErrors] = useState<Record<string, string>>({}); const [uploads, setUploads] = useState<OriginUpload[]>([]); const [saving, setSaving] = useState(false);
  const update = (key: string, value: string) => { const next = { ...values, [key]: value }; setValues(next); writeDraft(step.key, next); };
  const submit = async (event: FormEvent) => { event.preventDefault(); const localErrors = validateStep(step, values); if (step.key === "origin" && values.mode === "FIXED_ORIGIN" && (!uploads.length || uploads.some((upload) => !upload.description.trim()))) localErrors.referencePhotos = "Adicione e descreva ao menos uma foto de referência."; setErrors(localErrors); if (Object.keys(localErrors).length) return; setSaving(true); onError("");
    try { const payload = { ...values, ...(step.key === "origin" ? { uploads: uploads.map((upload) => ({ name: upload.file.name, description: upload.description })) } : {}) }; const result = await graphql(SaveOnboardingStepDocument, { input: { step: step.key, payload, expectedVersion: session.version, clientMutationId: clientMutationId() } }); const response = result.saveOnboardingStep; const nextErrors = mapUserErrors(response.userErrors); if (Object.keys(nextErrors).length) { setErrors(nextErrors); return; } if (!response.session) throw { message: "O servidor não confirmou esta etapa." } satisfies GraphQLFailure; sessionStorage.removeItem(draftKey(step.key)); onSaved(response.session, response.status ?? undefined); } catch (cause) { onError(failureText(cause)); } finally { setSaving(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Preencha os dados solicitados. A etapa só avança depois da confirmação do servidor.</p>{step.fields.map((field) => <DynamicField key={field.key} field={field} value={values[field.key] ?? ""} error={errors[field.key]} onChange={(value) => update(field.key, value)} />)}{step.key === "origin" && values.mode === "FIXED_ORIGIN" ? <OriginUploadCards uploads={uploads} onChange={setUploads} /> : null}{errors.referencePhotos ? <Alert tone="danger">{errors.referencePhotos}</Alert> : null}<div className="onboarding-actions"><Button type="submit" disabled={saving}>{saving ? "Salvando…" : "Salvar e continuar"}</Button><Button variant="secondary" onClick={onReview}>Revisar dados salvos</Button></div></form>;
}

function DynamicField({ field, value, error, onChange }: { field: OnboardingDefinition["steps"][number]["fields"][number]; value: string; error?: string; onChange: (value: string) => void }) {
  const props = { value, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => onChange(event.target.value), placeholder: field.placeholder ?? undefined };
  if (field.type === "select") return <Field label={field.label} error={error} required={field.required}><Select {...props}><option value="">Selecione</option>{field.options.map((option) => <option key={option} value={option}>{option}</option>)}</Select></Field>;
  if (field.type === "textarea") return <Field label={field.label} error={error} required={field.required}><Textarea {...props} /></Field>;
  return <Field label={field.label} error={error} required={field.required}><Input {...props} type={field.type === "number" || field.type === "date" || field.type === "email" ? field.type : "text"} /></Field>;
}

function Review({ values, status, onBack, onSubmit }: { values: Array<{ label: string; value: string }>; status?: Status; onBack: () => void; onSubmit: () => void }) {
  return <div className="onboarding-review"><p className="onboarding-step-description">Confira os dados que foram confirmados em cada etapa.</p><dl>{values.length ? values.map((item) => <Fragment key={item.label}><dt>{item.label}</dt><dd>{item.value || "Não informado"}</dd></Fragment>) : <dd>Nenhuma etapa foi confirmada ainda.</dd>}</dl>{status ? <Alert tone={status.originStatus === "PENDING" || status.deliveryStatus === "PENDING" ? "warning" : "info"}>Origem: {status.originStatus}. Entrega: {status.deliveryStatus}. Próxima ação: {status.nextAction}.</Alert> : null}<div className="onboarding-actions"><Button onClick={onSubmit}>Ver status da solicitação</Button><Button variant="secondary" onClick={onBack}>Voltar à etapa</Button></div></div>;
}

function StatusView({ status, onRestart }: { status?: Status; onRestart: () => void }) {
  const pending = !status || status.originStatus === "PENDING" || status.deliveryStatus === "PENDING" || status.state === "FAILED";
  return <div className="onboarding-form"><Alert tone={pending ? "warning" : "success"} title={pending ? "Aguardando confirmação" : "Solicitação confirmada"}>{pending ? "O processamento de fotos ou a entrega ao participante ainda não foi confirmado. Volte mais tarde para consultar o estado no servidor." : `A solicitação foi confirmada${status?.inspectionId ? ` para a inspeção ${status.inspectionId}` : ""}.`}</Alert>{status?.state === "FAILED" ? <Alert tone="danger">A operação falhou de forma recuperável. Revise os dados e tente novamente quando o serviço estiver disponível.</Alert> : null}<div className="onboarding-actions"><Button variant="secondary" onClick={onRestart}>Recomeçar verificação</Button></div></div>;
}

function Fragment({ children }: { children: React.ReactNode }) { return <>{children}</>; }

function statusFromSession(session: Session | undefined): Status | undefined {
  if (!session) return undefined;
  if (session.state === "FAILED") return { state: session.state, nextAction: "RETRY", originStatus: "FAILED", deliveryStatus: "PENDING", inspectionId: null };
  if (session.state === "SUBMITTED") return { state: session.state, nextAction: "WAIT_FOR_DELIVERY", originStatus: "READY", deliveryStatus: "PENDING", inspectionId: null };
  if (session.state === "READY_TO_SUBMIT") return { state: session.state, nextAction: "REVIEW_AND_SUBMIT", originStatus: "READY", deliveryStatus: "PENDING", inspectionId: null };
  return { state: session.state, nextAction: "CONTINUE_ONBOARDING", originStatus: session.state === "ORIGIN_PENDING" ? "PENDING" : "NOT_REQUIRED", deliveryStatus: "NOT_STARTED", inspectionId: null };
}
