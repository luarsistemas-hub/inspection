"use client";

import { Alert, Button, Card, Container, Field, Input, Select, Stack, Textarea } from "@inspection/design-system";
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { clearOnboardingSession } from "@/auth/onboarding-session";
import { clientMutationId, graphql, isOnboardingSessionFailure, mapUserErrors, type GraphQLFailure } from "@/graphql/client";
import { CompleteOnboardingDocument, OnboardingDefinitionDocument, OnboardingSessionDocument, RequestOnboardingOtpDocument, SaveOnboardingStepDocument, VerifyOnboardingOtpDocument, type OnboardingDefinitionQuery, type OnboardingSessionQuery } from "@/graphql/generated";
import { isSupportedDefinition, sortedSteps, validateStep, type OnboardingDefinition, type StepValues } from "./definition";
import { OriginUploadCards, type OriginUpload } from "./origin-upload";
import { presentOnboardingLabel, presentOnboardingOption, presentOnboardingStatus } from "./presentation";

type View = "identity" | "verify" | "step" | "review" | "status";
type Session = NonNullable<OnboardingSessionQuery["onboardingSession"]>;
type Status = { state: string; nextAction: string; originStatus: string; deliveryStatus: string; inspectionId: string | null };

const failureText = (error: unknown) => (error as GraphQLFailure).message ?? "Não foi possível concluir a solicitação.";

function showUserErrors(errors: ReadonlyArray<{ message: string; field?: string | null }>, setErrors: (value: Record<string, string>) => void, onError: (message: string) => void) {
  const mapped = mapUserErrors(errors);
  if (Object.keys(mapped).length) {
    setErrors(mapped);
  } else if (errors.length) {
    onError(errors[0].message);
  }
  return errors.length > 0;
}

function definitionFrom(result: OnboardingDefinitionQuery): OnboardingDefinition {
  return result.onboardingDefinition;
}

function draftKey(sessionID: string, step: string) { return `inspection.onboarding.draft.${sessionID}.${step}`; }
function readDraft(sessionID: string, step: string) {
	try { return JSON.parse(sessionStorage.getItem(draftKey(sessionID, step)) ?? "{}") as StepValues; } catch { return {}; }
}
function writeDraft(sessionID: string, step: string, values: StepValues) { sessionStorage.setItem(draftKey(sessionID, step), JSON.stringify(values)); }
function clearDrafts() { Object.keys(sessionStorage).filter((key) => key.startsWith("inspection.onboarding.draft.")).forEach((key) => sessionStorage.removeItem(key)); }

function confirmedSteps(value: unknown): Record<string, StepValues> {
  return value && typeof value === "object" ? value as Record<string, StepValues> : {};
}

function nextServerStep(definition: OnboardingDefinition, session: Session) {
  const steps = sortedSteps(definition);
  if (session.state === "IDENTITY_VERIFIED") return steps[0]?.key ?? "";
  const completed = steps.findIndex((item) => item.key.toUpperCase() === session.currentStep.toUpperCase());
  return steps[Math.min(completed + 1, steps.length - 1)]?.key ?? steps[0]?.key ?? "";
}

/** Public, cookie-backed onboarding journey. It only retains non-sensitive unfinished field values per tab. */
export function OnboardingJourney({ initialView }: { initialView?: "status" } = {}) {
  const [definition, setDefinition] = useState<OnboardingDefinition>();
  const [session, setSession] = useState<Session>();
  const [view, setView] = useState<View>(initialView ?? "identity");
  const [locator, setLocator] = useState("");
  const [email, setEmail] = useState("");
  const [activeStep, setActiveStep] = useState("");
  const [error, setError] = useState("");
  const [loading, setLoading] = useState(true);
  const [status, setStatus] = useState<Status>();
  const [confirmedValues, setConfirmedValues] = useState<Record<string, StepValues>>({});
  const [submitting, setSubmitting] = useState(false);
  const generation = useRef(0);
  const submissionID = useRef(clientMutationId());

  useEffect(() => {
    void Promise.all([graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" }), graphql(OnboardingSessionDocument)])
      .then(([definitionResult, sessionResult]) => {
        const nextDefinition = definitionFrom(definitionResult);
        if (!isSupportedDefinition(nextDefinition)) throw { message: "Esta versão do cadastro não é compatível. Tente novamente mais tarde." } satisfies GraphQLFailure;
        setDefinition(nextDefinition);
        if (sessionResult.onboardingSession) {
          const serverSession = sessionResult.onboardingSession;
          setSession(serverSession);
          setConfirmedValues(confirmedSteps(serverSession.completedSteps));
          setActiveStep(nextServerStep(nextDefinition, serverSession));
          setView(initialView ?? (serverSession.state === "PARTICIPANT_SAVED" || serverSession.state === "READY_TO_SUBMIT" ? "review" : serverSession.state === "SUBMITTED" ? "status" : "step"));
        }
      })
      .catch((cause: unknown) => { if (!isOnboardingSessionFailure(cause)) setError(failureText(cause)); })
      .finally(() => setLoading(false));
  }, [initialView]);

  const steps = useMemo(() => definition ? sortedSteps(definition) : [], [definition]);
  const step = steps.find((item) => item.key === activeStep) ?? steps[0];

  const restart = () => {
    generation.current += 1; clearOnboardingSession(); clearDrafts(); setSession(undefined); setLocator(""); setEmail(""); setActiveStep(""); setStatus(undefined); setConfirmedValues({}); submissionID.current = clientMutationId(); setError(""); setView("identity");
  };

  const complete = async () => {
    setSubmitting(true); setError("");
    try {
      const result = await graphql(CompleteOnboardingDocument, { input: { clientMutationId: submissionID.current } });
      const payload = result.completeOnboarding;
      if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; }
      if (!payload.status || !payload.session) throw { message: "O servidor não confirmou o estado da solicitação." } satisfies GraphQLFailure;
      if (payload.status.state === "SUBMITTED" && !payload.request) throw { message: "O servidor não confirmou a criação da vistoria." } satisfies GraphQLFailure;
      setSession(payload.session); setStatus(payload.status); if (payload.status.state === "SUBMITTED") clearDrafts(); setView("status");
    } catch (cause) { setError(failureText(cause)); } finally { setSubmitting(false); }
  };

  if (loading) return <PageShell title="Preparando seu cadastro"><p>Carregando as etapas disponíveis…</p></PageShell>;
  if (error && !definition) return <PageShell title="Não foi possível abrir o cadastro"><Alert tone="danger">{error}</Alert><Button onClick={() => location.reload()}>Tentar novamente</Button></PageShell>;
  if (!definition) return null;

  return <PageShell title={view === "identity" ? "Comece sua primeira vistoria" : view === "verify" ? "Confirme seu e-mail" : view === "review" ? "Revise os dados" : view === "status" ? "Acompanhe a vistoria" : presentOnboardingLabel(step?.label ?? "Cadastro")} steps={steps} currentStep={activeStep}>
    {error ? <Alert tone="danger">{error}</Alert> : null}
    {view === "identity" ? <IdentityForm generation={generation.current} onRequested={(nextLocator, nextEmail, requestGeneration) => { if (requestGeneration !== generation.current) return; clearDrafts(); setLocator(nextLocator); setEmail(nextEmail); setError(""); setView("verify"); }} onError={setError} /> : null}
    {view === "verify" ? <VerificationForm generation={generation.current} email={email} locator={locator} onVerified={(nextSession, requestGeneration) => { if (requestGeneration !== generation.current) return; clearDrafts(); setSession(nextSession); setActiveStep(nextServerStep(definition, nextSession)); setError(""); setView("step"); }} onBack={() => setView("identity")} onError={setError} /> : null}
    {view === "step" && step && session ? <StepForm key={step.key} step={step} session={session} onSaved={(nextSession, nextStatus, values) => {
      setSession(nextSession); if (nextStatus) setStatus(nextStatus);
      setConfirmedValues((current) => ({ ...current, [step.key]: values }));
      const nextIndex = steps.findIndex((item) => item.key === step.key) + 1;
      const next = steps[nextIndex];
      if (next) setActiveStep(next.key); else setView("review");
    }} onReview={() => setView("review")} onRestart={restart} onError={setError} /> : null}
    {view === "review" ? <Review values={steps.flatMap((item) => Object.entries(confirmedValues[item.key] ?? {}).map(([key, value]) => ({ label: `${presentOnboardingLabel(item.label)}: ${presentOnboardingLabel(item.fields.find((field) => field.key === key)?.label ?? key)}`, value: item.fields.find((field) => field.key === key)?.type === "select" ? presentOnboardingOption(value, item.fields.find((field) => field.key === key)?.choices) : value })))} status={status} onBack={() => { setActiveStep(session?.currentStep ?? steps.at(-1)?.key ?? ""); setView("step"); }} onRestart={restart} onSubmit={complete} submitting={submitting} /> : null}
    {view === "status" ? <StatusView status={status ?? statusFromSession(session)} submitting={submitting} onRetry={complete} onRestart={restart} /> : null}
  </PageShell>;
}

function PageShell({ title, children, steps = [], currentStep = "" }: { title: string; children: React.ReactNode; steps?: OnboardingDefinition["steps"]; currentStep?: string }) {
  return <main className="onboarding-shell"><Container className="onboarding-main"><header className="onboarding-header"><p>Inspection · Imobiliárias</p><h1>{title}</h1><p>Seus dados ficam protegidos nesta sessão. Você pode continuar no mesmo navegador.</p>{steps.length ? <ol className="onboarding-progress" aria-label="Etapas do cadastro">{steps.map((step) => <li key={step.key} data-current={step.key === currentStep}>{presentOnboardingLabel(step.label)}</li>)}</ol> : null}</header><Card><Stack gap="4">{children}</Stack></Card></Container></main>;
}

function IdentityForm({ generation, onRequested, onError }: { generation: number; onRequested: (locator: string, email: string, generation: number) => void; onError: (message: string) => void }) {
  const [name, setName] = useState(""); const [email, setEmail] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({});
  const submit = async (event: FormEvent) => { event.preventDefault(); setSubmitting(true); setErrors({}); onError("");
    try { const result = await graphql(RequestOnboardingOtpDocument, { input: { name, email, clientMutationId: clientMutationId() } }); const payload = result.requestOnboardingOtp; if (showUserErrors(payload.userErrors, setErrors, onError)) return; if (!payload.sessionLocator) throw { message: "Não foi possível iniciar a verificação." } satisfies GraphQLFailure; onRequested(payload.sessionLocator, email.trim().toLowerCase(), generation); } catch (cause) { onError(failureText(cause)); } finally { setSubmitting(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Informe seus dados para receber um código de confirmação.</p><Field label="Seu nome" error={errors.name} required><Input autoComplete="name" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Seu e-mail" error={errors.email} required><Input type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><Button type="submit" disabled={submitting}>{submitting ? "Enviando…" : "Enviar código"}</Button></form>;
}

function VerificationForm({ generation, email, locator, onVerified, onBack, onError }: { generation: number; email: string; locator: string; onVerified: (session: Session, generation: number) => void; onBack: () => void; onError: (message: string) => void }) {
  const [code, setCode] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({});
  const submit = async (event: FormEvent) => { event.preventDefault(); setSubmitting(true); setErrors({}); onError(""); try { const result = await graphql(VerifyOnboardingOtpDocument, { input: { sessionLocator: locator, code, clientMutationId: clientMutationId() } }); const payload = result.verifyOnboardingOtp; if (showUserErrors(payload.userErrors, setErrors, onError)) return; if (!payload.session) throw { message: "Não foi possível confirmar o código." } satisfies GraphQLFailure; onVerified(payload.session, generation); } catch (cause) { onError(failureText(cause)); } finally { setSubmitting(false); } };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Digite o código de seis dígitos enviado para <strong>{email}</strong>.</p><Field label="Código de confirmação" error={errors.code} required><Input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} /></Field><div className="onboarding-actions"><Button type="submit" disabled={submitting || code.length !== 6}>{submitting ? "Confirmando…" : "Confirmar e continuar"}</Button><Button variant="secondary" onClick={onBack}>Voltar</Button></div></form>;
}

function StepForm({ step, session, onSaved, onReview, onRestart, onError }: { step: OnboardingDefinition["steps"][number]; session: Session; onSaved: (session: Session, status: Status | undefined, values: StepValues) => void; onReview: () => void; onRestart: () => void; onError: (message: string) => void }) {
  const existingAgency = step.key === "agency" ? session.existingAgency : null;
  const [values, setValues] = useState<StepValues>(() => existingAgency ? { name: existingAgency.name, agencyName: existingAgency.name, existingAgencyId: existingAgency.tenantId } : readDraft(session.id, step.key)); const [errors, setErrors] = useState<Record<string, string>>({}); const [uploads, setUploads] = useState<OriginUpload[]>([]); const [saving, setSaving] = useState(false);
  const update = (key: string, value: string) => { const next = { ...values, [key]: value }; setValues(next); writeDraft(session.id, step.key, next); };
  const submit = async (event: FormEvent) => { event.preventDefault(); const localErrors = validateStep(step, values); if (step.key === "origin" && values.mode === "FIXED_ORIGIN" && (!uploads.length || uploads.some((upload) => !upload.description.trim()))) localErrors.referencePhotos = "Adicione e descreva ao menos uma foto de referência."; setErrors(localErrors); if (Object.keys(localErrors).length) return; setSaving(true); onError("");
    try { const payload = { ...values, ...(step.key === "origin" ? { uploads: uploads.map((upload) => ({ name: upload.file.name, description: upload.description })) } : {}) }; const result = await graphql(SaveOnboardingStepDocument, { input: { step: step.key, payload, expectedVersion: session.version, clientMutationId: clientMutationId() } }); const response = result.saveOnboardingStep; if (showUserErrors(response.userErrors, setErrors, onError)) return; if (!response.session) throw { message: "O servidor não confirmou esta etapa." } satisfies GraphQLFailure; sessionStorage.removeItem(draftKey(session.id, step.key)); onSaved(response.session, response.status ?? undefined, values); } catch (cause) { onError(failureText(cause)); } finally { setSaving(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Preencha os dados solicitados. A etapa só avança depois da confirmação do servidor.</p>{existingAgency ? <><Alert tone="info">Encontramos a imobiliária <strong>{existingAgency.name}</strong> vinculada a este e-mail. Ela será reutilizada nesta solicitação.</Alert><Field label="Nome da imobiliária" required><Input value={existingAgency.name} readOnly /></Field></> : step.fields.map((field) => <DynamicField key={field.key} field={field} value={values[field.key] ?? ""} error={errors[field.key]} onChange={(value) => update(field.key, value)} />)}{step.key === "origin" && values.mode === "FIXED_ORIGIN" ? <OriginUploadCards uploads={uploads} onChange={setUploads} /> : null}{errors.referencePhotos ? <Alert tone="danger">{errors.referencePhotos}</Alert> : null}<div className="onboarding-actions"><Button type="submit" disabled={saving}>{saving ? "Salvando…" : "Salvar e continuar"}</Button><Button variant="secondary" onClick={onReview}>Revisar dados salvos</Button><Button variant="secondary" onClick={onRestart}>Iniciar novo cadastro</Button></div></form>;
}

function DynamicField({ field, value, error, onChange }: { field: OnboardingDefinition["steps"][number]["fields"][number]; value: string; error?: string; onChange: (value: string) => void }) {
  const props = { value, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => onChange(event.target.value), placeholder: field.placeholder ?? undefined };
  if (field.type === "select") return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Select {...props}><option value="">Selecione</option>{field.options.map((option) => <option key={option} value={option}>{presentOnboardingOption(option, field.choices)}</option>)}</Select></Field>;
  if (field.type === "textarea") return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Textarea {...props} /></Field>;
  return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Input {...props} type={field.type === "number" || field.type === "date" || field.type === "email" ? field.type : "text"} /></Field>;
}

function Review({ values, status, onBack, onRestart, onSubmit, submitting }: { values: Array<{ label: string; value: string }>; status?: Status; onBack: () => void; onRestart: () => void; onSubmit: () => Promise<void>; submitting: boolean }) {
  return <div className="onboarding-review"><p className="onboarding-step-description">Confira os dados que foram confirmados em cada etapa.</p><dl>{values.length ? values.map((item) => <Fragment key={item.label}><dt>{item.label}</dt><dd>{item.value || "Não informado"}</dd></Fragment>) : <dd>Nenhuma etapa foi confirmada ainda.</dd>}</dl>{status ? <Alert tone={status.originStatus === "PENDING" || status.deliveryStatus === "PENDING" ? "warning" : "info"}>Origem: {presentOnboardingStatus(status.originStatus)}. Entrega: {presentOnboardingStatus(status.deliveryStatus)}. Próxima ação: {presentOnboardingStatus(status.nextAction)}.</Alert> : null}<div className="onboarding-actions"><Button onClick={() => void onSubmit()} disabled={submitting}>{submitting ? "Criando vistoria…" : "Criar primeira vistoria"}</Button><Button variant="secondary" onClick={onBack}>Voltar à etapa</Button><Button variant="secondary" onClick={onRestart}>Iniciar novo cadastro</Button></div></div>;
}

function StatusView({ status, submitting, onRetry, onRestart }: { status?: Status; submitting: boolean; onRetry: () => Promise<void>; onRestart: () => void }) {
  const created = status?.state === "SUBMITTED" && Boolean(status.inspectionId);
  const pending = !created || status?.originStatus === "PENDING" || status?.deliveryStatus === "PENDING";
  const waitingForOrigin = status?.state === "ORIGIN_PENDING" || status?.originStatus === "PENDING";
  return <div className="onboarding-form"><Alert tone={created ? "success" : "warning"} title={created ? "Primeira vistoria criada" : waitingForOrigin ? "Fotos de referência em processamento" : "Aguardando confirmação"}>{created ? `A vistoria ${status.inspectionId} foi criada. ${pending ? "O link de captura está sendo entregue ao responsável pela vistoria." : "O link de captura foi entregue ao responsável pela vistoria."}` : waitingForOrigin ? "As fotos de referência ainda não estão prontas. Tente novamente após o processamento." : "A solicitação ainda não foi confirmada pelo servidor."}</Alert>{status?.state === "FAILED" ? <Alert tone="danger">A operação falhou de forma recuperável. Revise os dados e tente novamente quando o serviço estiver disponível.</Alert> : null}<div className="onboarding-actions">{waitingForOrigin ? <Button onClick={() => void onRetry()} disabled={submitting}>{submitting ? "Consultando…" : "Tentar novamente"}</Button> : null}<Button variant="secondary" onClick={onRestart}>Iniciar novo cadastro</Button></div></div>;
}

function Fragment({ children }: { children: React.ReactNode }) { return <>{children}</>; }

function statusFromSession(session: Session | undefined): Status | undefined {
  if (!session) return undefined;
  if (session.state === "FAILED") return { state: session.state, nextAction: "RETRY", originStatus: "FAILED", deliveryStatus: "PENDING", inspectionId: null };
  if (session.state === "SUBMITTED") return { state: session.state, nextAction: "WAIT_FOR_DELIVERY", originStatus: "READY", deliveryStatus: "PENDING", inspectionId: null };
  if (session.state === "READY_TO_SUBMIT") return { state: session.state, nextAction: "REVIEW_AND_SUBMIT", originStatus: "READY", deliveryStatus: "PENDING", inspectionId: null };
  return { state: session.state, nextAction: "CONTINUE_ONBOARDING", originStatus: session.state === "ORIGIN_PENDING" ? "PENDING" : "NOT_REQUIRED", deliveryStatus: "NOT_STARTED", inspectionId: null };
}
