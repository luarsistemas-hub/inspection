"use client";

import { AccountMenu, Alert, Button, Card, Confirmation, Container, Dialog, Field, InfoDisclosure, Input, Select, Stack, Steps, Textarea, ThemeSelector } from "@inspection/design-system";
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { AddressFieldStyles, AddressForm, emptyPostalAddress, formatPostalAddress, type PostalAddress, type PostalLookup } from "@inspection/address";
import { clearOnboardingSession } from "@/auth/onboarding-session";
import { clientMutationId, graphql, isOnboardingSessionFailure, logoutOnboardingSession, mapUserErrors, uploadReferencePhoto, type GraphQLFailure } from "@/graphql/client";
import { CompleteOnboardingDocument, CorrectOnboardingResponsibleEmailDocument, OnboardingDefinitionDocument, OnboardingLookupPostalCodeDocument, OnboardingSessionDocument, OnboardingStatusDocument, RequestOnboardingOtpDocument, SaveOnboardingStepDocument, VerifyOnboardingOtpDocument, type OnboardingDefinitionQuery, type OnboardingSessionQuery, type OnboardingStatusQuery } from "@/graphql/generated";
import { isSupportedDefinition, nextConfirmedStep, sortedSteps, validateStep, valuesForParticipantMode, type OnboardingDefinition, type StepValues } from "./definition";
import { OriginUploadCards, originUploadError, type OriginUpload } from "./origin-upload";
import { presentOnboardingLabel, presentOnboardingOption, presentOnboardingStatus } from "./presentation";
import { TurnstileWidget } from "./turnstile-widget";

type View = "identity" | "verify" | "step" | "review" | "status";
type Session = NonNullable<OnboardingSessionQuery["onboardingSession"]>;
type Status = NonNullable<OnboardingStatusQuery["onboardingStatus"]>;

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
  return nextConfirmedStep(definition, session);
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
  const [restartOpen, setRestartOpen] = useState(false);
  const [reconciling, setReconciling] = useState(false);
  const generation = useRef(0);
  const completionInFlight = useRef(false);
  const submissionID = useRef(clientMutationId());

  useEffect(() => {
    void Promise.all([graphql(OnboardingDefinitionDocument, { segment: "REAL_ESTATE" }), graphql(OnboardingSessionDocument)])
      .then(([definitionResult, sessionResult]) => {
        const serverSession = sessionResult.onboardingSession;
        const nextDefinition = serverSession ? serverSession.definition as unknown as OnboardingDefinition : definitionFrom(definitionResult);
        if (!isSupportedDefinition(nextDefinition)) throw { message: "Esta versão do cadastro não é compatível. Tente novamente mais tarde." } satisfies GraphQLFailure;
        setDefinition(nextDefinition);
        if (serverSession) {
          setSession(serverSession);
          setConfirmedValues(confirmedSteps(serverSession.completedSteps));
          setActiveStep(nextServerStep(nextDefinition, serverSession));
          setView(initialView ?? (serverSession.state === "PARTICIPANT_SAVED" || serverSession.state === "READY_TO_SUBMIT" ? "review" : serverSession.state === "SUBMITTED" ? "status" : "step"));
        } else {
          setView("identity");
        }
      })
      .catch((cause: unknown) => { if (!isOnboardingSessionFailure(cause)) setError(failureText(cause)); })
      .finally(() => setLoading(false));
  }, [initialView]);

  useEffect(() => {
    if (view !== "status" || !session) return;
    let cancelled = false;
    const refresh = async () => {
      try {
        const result = await graphql(OnboardingStatusDocument);
        if (!cancelled && result.onboardingStatus) setStatus(result.onboardingStatus);
      } catch (cause) {
        if (!cancelled && !isOnboardingSessionFailure(cause)) setError(failureText(cause));
      }
    };
    void refresh();
    const expired = Date.parse(session.expiresAt) <= Date.now();
    const terminal = expired || status?.deliveryStatus === "FAILED" || status?.deliveryStatus === "UNKNOWN" || (status?.responsibilityStatus != null && status.responsibilityStatus !== "PENDING");
    if (terminal) return () => { cancelled = true; };
    const interval = window.setInterval(() => void refresh(), status?.deliveryStatus === "QUEUED" || status?.deliveryStatus === "PROCESSING" ? 5000 : 30000);
    return () => { cancelled = true; window.clearInterval(interval); };
  }, [session, status?.deliveryStatus, view]);

  const steps = useMemo(() => definition ? sortedSteps(definition) : [], [definition]);
  const step = steps.find((item) => item.key === activeStep) ?? steps[0];

  const restart = () => {
    generation.current += 1; clearOnboardingSession(); clearDrafts(); setSession(undefined); setLocator(""); setEmail(""); setActiveStep(""); setStatus(undefined); setConfirmedValues({}); submissionID.current = clientMutationId(); setError(""); setView("identity");
  };

  const refreshStatus = async () => {
    setReconciling(true); setError("");
    try {
      const result = await graphql(OnboardingStatusDocument);
      if (!result.onboardingStatus) throw { message: "O servidor ainda não confirmou a situação. Tente consultar novamente." } satisfies GraphQLFailure;
      setStatus(result.onboardingStatus); setView("status");
    } catch (cause) { setError(failureText(cause)); }
    finally { setReconciling(false); }
  };

  const complete = async () => {
    if (completionInFlight.current) return;
    completionInFlight.current = true; setSubmitting(true); setError("");
    try {
      const result = await graphql(CompleteOnboardingDocument, { input: { clientMutationId: submissionID.current } });
      const payload = result.completeOnboarding;
      if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; }
      if (!payload.status || !payload.session) throw { message: "O servidor não confirmou o estado da solicitação." } satisfies GraphQLFailure;
      if (payload.status.state === "SUBMITTED" && !payload.request) throw { message: "O servidor não confirmou a criação da vistoria." } satisfies GraphQLFailure;
      setSession(payload.session); setStatus(payload.status); if (payload.status.state === "SUBMITTED") clearDrafts(); setView("status");
    } catch {
      setError("Não foi possível confirmar a resposta. Vamos consultar a situação antes de permitir outra tentativa.");
      await refreshStatus();
    } finally { completionInFlight.current = false; setSubmitting(false); }
  };

  if (loading) return <PageShell title="Preparando seu cadastro"><p>Carregando as etapas disponíveis…</p></PageShell>;
  if (error && !definition) return <PageShell title="Não foi possível abrir o cadastro"><Alert tone="danger">{error}</Alert><Button onClick={() => location.reload()}>Tentar novamente</Button></PageShell>;
  if (!definition) return null;

  const logout = async () => {
    try {
      await logoutOnboardingSession();
      restart();
    } catch (cause) {
      setError(failureText(cause));
    }
  };

  return <PageShell title={view === "identity" ? "Comece sua primeira vistoria" : view === "verify" ? "Confirme seu e-mail" : view === "review" ? "Revise os dados" : view === "status" ? "Acompanhe a vistoria" : presentOnboardingLabel(step?.label ?? "Cadastro")} steps={steps} currentStep={activeStep} account={session?.owner} onSignOut={logout}>
    {error ? <Alert tone="danger">{error}</Alert> : null}
    {initialView === "status" && !session ? <Alert tone="info">Nenhuma solicitação ativa foi encontrada. Inicie a verificação para começar um cadastro.</Alert> : null}
    {view === "identity" ? <IdentityForm generation={generation.current} onRequested={(nextLocator, nextEmail, requestGeneration) => { if (requestGeneration !== generation.current) return; clearDrafts(); setLocator(nextLocator); setEmail(nextEmail); setError(""); setView("verify"); }} onError={setError} /> : null}
    {view === "verify" ? <VerificationForm generation={generation.current} email={email} locator={locator} onVerified={(nextSession, requestGeneration) => { if (requestGeneration !== generation.current) return; clearDrafts(); setSession(nextSession); setActiveStep(nextServerStep(definition, nextSession)); setError(""); setView("step"); }} onBack={() => setView("identity")} onError={setError} /> : null}
    {view === "step" && step && session ? <StepForm key={step.key} step={step} session={session} onSaved={(nextSession, nextStatus, values) => {
      setSession(nextSession); if (nextStatus) setStatus(nextStatus);
      setConfirmedValues((current) => ({ ...current, [step.key]: values }));
      const next = responseStep(nextSession, steps);
      if (next) setActiveStep(next); else setView("review");
    }} onRestart={() => setRestartOpen(true)} onError={setError} /> : null}
    {view === "review" ? <Review values={steps.flatMap((item) => Object.entries(confirmedValues[item.key] ?? {}).filter(([key, value]) => key !== "emailConfirmation" && (typeof value === "string" || (key === "addressDetails" && typeof value === "object" && !Array.isArray(value)))).map(([key, value]) => { const field = item.fields.find((candidate) => candidate.key === key); const display = field?.type === "select" && typeof value === "string" ? presentOnboardingOption(value, field.choices) : field?.type === "address" && typeof value === "object" ? formatPostalAddress(value as PostalAddress) : String(value); return { label: `${presentOnboardingLabel(item.label)}: ${presentOnboardingLabel(field?.label ?? key)}`, value: display }; }))} status={status} originMode={typeof confirmedValues.origin?.mode === "string" ? confirmedValues.origin.mode : undefined} originConfirmed={Array.isArray((confirmedValues.origin as Record<string, unknown> | undefined)?.mediaIds)} onBack={() => { setActiveStep(session?.currentStep ?? steps.at(-1)?.key ?? ""); setView("step"); }} onRestart={() => setRestartOpen(true)} onSubmit={complete} submitting={submitting} /> : null}
    {view === "status" ? <StatusView status={status ?? statusFromSession(session)} submitting={submitting || reconciling} onRetry={refreshStatus} onRestart={() => setRestartOpen(true)} onCorrected={setStatus} /> : null}
    {restartOpen ? <Dialog isOpen onClose={() => setRestartOpen(false)} title="Iniciar novo cadastro?">
      <Confirmation target="Cadastro atual" scope="Esta sessão de onboarding" consequence="Os campos ainda não enviados e as fotos que só estão nesta página serão descartados. Será necessário iniciar uma nova verificação por e-mail." confirmLabel="Descartar e começar novamente" onCancel={() => setRestartOpen(false)} onConfirm={() => { setRestartOpen(false); restart(); }} />
    </Dialog> : null}
  </PageShell>;
}

function PageShell({ title, children, steps = [], currentStep = "", account, onSignOut }: { title: string; children: React.ReactNode; steps?: OnboardingDefinition["steps"]; currentStep?: string; account?: { name: string; email: string }; onSignOut?: () => void }) {
  const currentIndex = steps.findIndex((item) => item.key === currentStep);
  const stepItems = steps.map((item, index) => ({ id: item.key, label: presentOnboardingLabel(item.label), state: index < currentIndex ? "complete" as const : item.key === currentStep ? "current" as const : "pending" as const }));
  return <main className="onboarding-shell"><Container className="onboarding-main"><header className="onboarding-header"><div className="onboarding-utility-bar"><ThemeSelector />{account && onSignOut ? <AccountMenu name={account.name} description="Sessão de cadastro" email={account.email} onSignOut={onSignOut} /> : null}</div><InfoDisclosure className="onboarding-title-disclosure" label="cadastro e salvamento" heading={<h1>{title}</h1>}>Os campos ficam neste navegador e as etapas salvas são confirmadas pelo servidor. Fotos ainda não enviadas precisam ser selecionadas novamente após recarregar a página.</InfoDisclosure>{currentStep && stepItems.length ? <details className="onboarding-steps-panel"><summary>Etapa {currentIndex + 1} de {steps.length}: {stepItems[currentIndex]?.label}</summary><Steps label="Etapas do cadastro" items={stepItems} /></details> : null}</header><Card><Stack gap="4">{children}</Stack></Card></Container></main>;
}

function responseStep(session: Session, steps: OnboardingDefinition["steps"]) {
  return nextConfirmedStep({ steps, schemaVersion: 1, segment: "REAL_ESTATE", version: 1, segmentVersion: "", originModes: [] }, session);
}

function IdentityForm({ generation, onRequested, onError }: { generation: number; onRequested: (locator: string, email: string, generation: number) => void; onError: (message: string) => void }) {
  const [name, setName] = useState(""); const [email, setEmail] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({}); const [turnstileToken, setTurnstileToken] = useState(""); const [turnstileResetKey, setTurnstileResetKey] = useState(0); const inFlight = useRef(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (inFlight.current || !turnstileToken) return; inFlight.current = true; setSubmitting(true); setErrors({}); onError("");
    try { const result = await graphql(RequestOnboardingOtpDocument, { input: { name, email, turnstileToken, clientMutationId: clientMutationId() } }); const payload = result.requestOnboardingOtp; if (showUserErrors(payload.userErrors, setErrors, onError)) { const captchaError = payload.userErrors.find((item) => item.field === "turnstileToken"); if (captchaError) onError(captchaError.message); setTurnstileToken(""); setTurnstileResetKey((value) => value + 1); return; } if (!payload.sessionLocator) throw { message: "Não foi possível iniciar a verificação." } satisfies GraphQLFailure; onRequested(payload.sessionLocator, email.trim().toLowerCase(), generation); } catch (cause) { setTurnstileToken(""); setTurnstileResetKey((value) => value + 1); onError(failureText(cause)); } finally { inFlight.current = false; setSubmitting(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Informe seus dados para receber um código de confirmação.</p><Field label="Seu nome" error={errors.name} required><Input autoComplete="name" value={name} onChange={(event) => setName(event.target.value)} /></Field><Field label="Seu e-mail" error={errors.email} required><Input type="email" autoComplete="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><TurnstileWidget onToken={setTurnstileToken} resetKey={turnstileResetKey} /><Button type="submit" disabled={submitting || !turnstileToken} isPending={submitting} pendingLabel="Enviando…">Enviar código</Button></form>;
}

function VerificationForm({ generation, email, locator, onVerified, onBack, onError }: { generation: number; email: string; locator: string; onVerified: (session: Session, generation: number) => void; onBack: () => void; onError: (message: string) => void }) {
  const [code, setCode] = useState(""); const [submitting, setSubmitting] = useState(false); const [errors, setErrors] = useState<Record<string, string>>({}); const inFlight = useRef(false);
  const submit = async (event: FormEvent) => { event.preventDefault(); if (inFlight.current) return; inFlight.current = true; setSubmitting(true); setErrors({}); onError(""); try { const result = await graphql(VerifyOnboardingOtpDocument, { input: { sessionLocator: locator, code, clientMutationId: clientMutationId() } }); const payload = result.verifyOnboardingOtp; if (showUserErrors(payload.userErrors, setErrors, onError)) return; if (!payload.session) throw { message: "Não foi possível confirmar o código." } satisfies GraphQLFailure; onVerified(payload.session, generation); } catch (cause) { onError(failureText(cause)); } finally { inFlight.current = false; setSubmitting(false); } };
  return <form className="onboarding-form" onSubmit={submit} noValidate><p className="onboarding-step-description">Digite o código de seis dígitos enviado para <strong>{email}</strong>.</p><Field label="Código de confirmação" error={errors.code} required><Input inputMode="numeric" autoComplete="one-time-code" maxLength={6} value={code} onChange={(event) => setCode(event.target.value.replace(/\D/g, ""))} /></Field><div className="onboarding-actions"><Button type="submit" disabled={submitting || code.length !== 6}>{submitting ? "Confirmando…" : "Confirmar e continuar"}</Button><Button variant="secondary" onClick={onBack}>Voltar</Button></div></form>;
}

function StepForm({ step, session, onSaved, onRestart, onError }: { step: OnboardingDefinition["steps"][number]; session: Session; onSaved: (session: Session, status: Status | undefined, values: StepValues) => void; onRestart: () => void; onError: (message: string) => void }) {
  const existingAgency = step.key === "agency" ? session.existingAgency : null;
  const [values, setValues] = useState<StepValues>(() => existingAgency ? { name: existingAgency.name, agencyName: existingAgency.name, existingAgencyId: existingAgency.tenantId } : readDraft(session.id, step.key)); const [errors, setErrors] = useState<Record<string, string>>({}); const [uploads, setUploads] = useState<OriginUpload[]>([]); const [saving, setSaving] = useState(false);
  const savingRef = useRef(false);
  const activeUploads = useRef(new Set<string>());
  const updateField = (key: string, value: string) => {
    const next = step.key === "participant" && key === "mode"
      ? valuesForParticipantMode(values, value, session.owner)
      : { ...values, [key]: value };
    setValues(next);
    writeDraft(session.id, step.key, next);
  };
  const updateAddress = (value: PostalAddress) => { const next = { ...values, addressDetails: value }; setValues(next); writeDraft(session.id, step.key, next); };
  const lookupAddress = async (postalCode: string): Promise<PostalLookup | null> => {
    const result = await graphql(OnboardingLookupPostalCodeDocument, { postalCode });
    const lookup = result.lookupPostalCode;
    return lookup.found ? { postalCode: lookup.postalCode, street: lookup.street ?? "", district: lookup.district ?? "", city: lookup.city ?? "", state: lookup.state ?? "", municipalityCode: lookup.municipalityCode ?? "" } : null;
  };
  const uploadOne = async (upload: OriginUpload) => {
    if (upload.mediaId) return upload.mediaId;
    if (activeUploads.current.has(upload.id)) throw { message: "O envio desta foto já está em andamento." } satisfies GraphQLFailure;
    activeUploads.current.add(upload.id);
    setUploads((current) => current.map((item) => item.id === upload.id ? { ...item, sending: true, failed: false } : item));
    try {
      const mediaId = await uploadReferencePhoto(upload.file, upload.description, upload.attentionItems, upload.id);
      setUploads((current) => current.map((item) => item.id === upload.id ? { ...item, mediaId, sending: false } : item));
      return mediaId;
    } catch (cause) {
      setUploads((current) => current.map((item) => item.id === upload.id ? { ...item, sending: false, failed: true } : item));
      throw cause;
    } finally {
      activeUploads.current.delete(upload.id);
    }
  };
  const retry = (id: string) => {
    const upload = uploads.find((item) => item.id === id);
    if (upload && !activeUploads.current.has(upload.id) && !upload.sending) void uploadOne(upload).catch((cause) => onError(failureText(cause)));
  };
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (savingRef.current) return;
    const localErrors = validateStep(step, values);
    if (step.key === "origin" && values.mode === "FIXED_ORIGIN") {
      const uploadError = originUploadError(uploads);
      if (uploadError) localErrors.referencePhotos = uploadError;
    }
    setErrors(localErrors);
    if (Object.keys(localErrors).length) return;
    savingRef.current = true; setSaving(true); onError("");
    try {
      const mediaIds: string[] = [];
      if (step.key === "origin" && values.mode === "FIXED_ORIGIN") {
        for (const upload of uploads) mediaIds.push(await uploadOne(upload));
      }
      const payload = { ...values, ...(step.key === "origin" ? { mediaIds } : {}) };
      const result = await graphql(SaveOnboardingStepDocument, { input: { step: step.key, payload, expectedVersion: session.version, clientMutationId: clientMutationId() } });
      const response = result.saveOnboardingStep;
      if (showUserErrors(response.userErrors, setErrors, onError)) return;
      if (!response.session) throw { message: "O servidor não confirmou esta etapa." } satisfies GraphQLFailure;
      sessionStorage.removeItem(draftKey(session.id, step.key));
      onSaved(response.session, response.status ?? undefined, { ...values, ...(step.key === "origin" ? { mediaIds } : {}) });
    } catch (cause) { onError(failureText(cause)); } finally { savingRef.current = false; setSaving(false); }
  };
  return <form className="onboarding-form" onSubmit={submit} noValidate><AddressFieldStyles /><p className="onboarding-step-description">Preencha os dados solicitados. A etapa só avança depois da confirmação do servidor.</p>{existingAgency ? <><Alert tone="info">Encontramos a imobiliária <strong>{existingAgency.name}</strong> vinculada a este e-mail. Ela será reutilizada nesta solicitação.</Alert><Field label="Nome da imobiliária" required><Input value={existingAgency.name} readOnly /></Field></> : step.fields.filter((field) => field.key !== "emailConfirmation" || values.mode === "DELEGATE").map((field) => field.type === "address" ? <div key={field.key}><AddressForm value={values.addressDetails && typeof values.addressDetails === "object" && !Array.isArray(values.addressDetails) ? values.addressDetails as PostalAddress : emptyPostalAddress()} onChange={updateAddress} onLookup={lookupAddress} prefix="onboarding-address" disabled={saving} autoLookup layout="essential-fields" />{errors[field.key] ? <p role="alert">{errors[field.key]}</p> : null}</div> : <DynamicField key={field.key} field={field} value={typeof values[field.key] === "string" ? values[field.key] as string : ""} error={errors[field.key]} onChange={(value) => updateField(field.key, value)} />)}{step.key === "origin" && values.mode === "FIXED_ORIGIN" ? <OriginUploadCards uploads={uploads} onChange={setUploads} onRetry={retry} /> : null}{errors.referencePhotos ? <Alert tone="danger">{errors.referencePhotos}</Alert> : null}<div className="onboarding-actions"><Button type="submit" isPending={saving} pendingLabel="Salvando…">Salvar e continuar</Button><Button variant="secondary" onClick={onRestart} disabled={saving}>Iniciar novo cadastro</Button></div></form>;
}

function DynamicField({ field, value, error, onChange }: { field: OnboardingDefinition["steps"][number]["fields"][number]; value: string; error?: string; onChange: (value: string) => void }) {
  const props = { value, onChange: (event: React.ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) => onChange(event.target.value), placeholder: field.placeholder ?? undefined };
  if (field.type === "select") return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Select {...props}><option value="">Selecione</option>{field.options.map((option) => <option key={option} value={option}>{presentOnboardingOption(option, field.choices)}</option>)}</Select></Field>;
  if (field.type === "textarea") return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Textarea {...props} /></Field>;
  return <Field label={presentOnboardingLabel(field.label)} error={error} required={field.required}><Input {...props} type={field.type === "number" || field.type === "date" || field.type === "email" ? field.type : "text"} /></Field>;
}

function Review({ values, status, originMode, originConfirmed, onBack, onRestart, onSubmit, submitting }: { values: Array<{ label: string; value: string }>; status?: Status; originMode?: string; originConfirmed: boolean; onBack: () => void; onRestart: () => void; onSubmit: () => Promise<void>; submitting: boolean }) {
  const originReady = originMode !== "FIXED_ORIGIN" || originConfirmed || status?.originStatus === "READY" || status?.originStatus === "ACTIVE";
  return <div className="onboarding-review"><p className="onboarding-step-description">Confira os dados que foram confirmados em cada etapa.</p><dl>{values.length ? values.map((item) => <Fragment key={item.label}><dt>{item.label}</dt><dd>{item.value || "Não informado"}</dd></Fragment>) : <dd>Nenhuma etapa foi confirmada ainda.</dd>}</dl>{!originReady ? <Alert tone="warning">As fotos de referência ainda não foram confirmadas como prontas. Consulte o status do servidor antes de continuar.</Alert> : null}{status ? <Alert tone="info">Origem: {presentOnboardingStatus(status.originStatus)}. Entrega: {presentOnboardingStatus(status.deliveryStatus)}. Próxima ação: {presentOnboardingStatus(status.nextAction)}.</Alert> : null}<div className="onboarding-actions"><Button onClick={() => void onSubmit()} isPending={submitting} pendingLabel="Enviando solicitação…" disabled={!originReady}>Criar primeira vistoria</Button><Button variant="secondary" onClick={onBack}>Voltar à etapa</Button><Button variant="secondary" onClick={onRestart}>Iniciar novo cadastro</Button></div></div>;
}

function StatusView({ status, submitting, onRetry, onRestart, onCorrected }: { status?: Status; submitting: boolean; onRetry: () => Promise<void>; onRestart: () => void; onCorrected: (status: Status) => void }) {
  const accepted = status?.state === "SUBMITTED" || Boolean(status?.requestId);
  const created = Boolean(status?.inspectionId);
  const waitingForOrigin = status?.originStatus === "PENDING" || status?.nextAction === "WAIT_FOR_ORIGIN";
  const deliveryFailed = status?.deliveryStatus === "FAILED";
  const deliveryUnknown = status?.deliveryStatus === "UNKNOWN";
  const delivered = ["ACCEPTED", "SENT", "DELIVERED"].includes(status?.deliveryStatus ?? "");
  const responsibilityConfirmed = Boolean(status?.responsibilityStatus && status.responsibilityStatus !== "PENDING");
  const message = !status ? "A situação ainda não foi confirmada pelo servidor." : deliveryFailed ? "A vistoria foi recebida, mas não foi possível enviar o link. Confira o endereço e use a correção disponível, se permitida." : deliveryUnknown ? "A vistoria foi recebida, mas o envio do link ainda não foi confirmado. Consulte a situação antes de tentar novamente." : responsibilityConfirmed ? "A pessoa responsável confirmou o acesso ao link." : waitingForOrigin ? "A solicitação foi aceita. As fotos de referência continuam em processamento; nenhuma vistoria foi criada ainda." : created ? `A vistoria ${status?.inspectionId} foi criada. ${delivered ? "O serviço de e-mail aceitou o convite." : "O envio do convite ainda está pendente."}` : accepted ? "A configuração foi aceita pelo servidor. A criação da vistoria e o envio do convite ainda não foram confirmados." : "A solicitação ainda não foi confirmada pelo servidor.";
  const title = !status ? "Situação não confirmada" : deliveryFailed ? "Falha no envio do convite" : deliveryUnknown ? "Envio do convite não confirmado" : responsibilityConfirmed ? "Acesso confirmado" : waitingForOrigin ? "Fotos de referência em processamento" : created ? "Vistoria criada" : accepted ? "Configuração aceita" : "Aguardando confirmação";
  const tone = deliveryFailed ? "danger" : responsibilityConfirmed ? "success" : accepted ? "info" : "warning";
  return <div className="onboarding-form"><Alert tone={tone} title={title}>{message}</Alert>{status ? <dl className="onboarding-status-details"><dt>Configuração</dt><dd>{presentOnboardingStatus(status.state)}</dd><dt>Fotos de referência</dt><dd>{presentOnboardingStatus(status.originStatus)}</dd>{created ? <><dt>Vistoria</dt><dd>{status.inspectionId}</dd></> : null}<dt>Convite</dt><dd>{presentOnboardingStatus(status.deliveryStatus)}</dd><dt>Próxima ação</dt><dd>{presentOnboardingStatus(status.nextAction)}</dd></dl> : null}{status?.canCorrectResponsibleEmail ? <ResponsibleEmailCorrection status={status} onCorrected={onCorrected} /> : null}<div className="onboarding-actions"><Button variant="secondary" isPending={submitting} pendingLabel="Consultando…" onClick={() => void onRetry()}>Consultar situação</Button><Button variant="secondary" onClick={onRestart}>Iniciar novo cadastro</Button></div></div>;
}

function ResponsibleEmailCorrection({ status, onCorrected }: { status: Status; onCorrected: (status: Status) => void }) {
  const [email, setEmail] = useState(status.responsibleEmail ?? "");
  const [confirmation, setConfirmation] = useState(email);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    if (!/^[^\s@]+@[^\s@]+\.[^\s@]+$/.test(email.trim())) { setError("Informe um e-mail válido."); return; }
    if (email.trim().toLowerCase() !== confirmation.trim().toLowerCase()) { setError("Os e-mails precisam ser iguais."); return; }
    setBusy(true); setError("");
    try {
      const result = await graphql(CorrectOnboardingResponsibleEmailDocument, { input: { email, emailConfirmation: confirmation, expectedResponsibilityVersion: status.responsibilityVersion ?? 1, clientMutationId: clientMutationId() } });
      const payload = result.correctOnboardingResponsibleEmail;
      if (payload.userErrors.length) { setError(payload.userErrors[0].message); return; }
      if (payload.status) onCorrected(payload.status); else setError("O servidor não confirmou a correção do e-mail.");
    } catch (cause) { setError(failureText(cause)); } finally { setBusy(false); }
  };
  return <form className="onboarding-form" onSubmit={submit}><p><strong>Corrigir e reenviar</strong></p><p>{status.deliveryStatus === "NOT_STARTED" ? "Confirme o endereço para gerar e enviar o link. O responsável não precisa estar online agora." : "O link anterior será invalidado. O responsável não precisa estar online agora."}</p><Field label="Novo e-mail" required><Input type="email" value={email} onChange={(event) => setEmail(event.target.value)} /></Field><Field label="Confirme o novo e-mail" required><Input type="email" value={confirmation} onChange={(event) => setConfirmation(event.target.value)} /></Field>{error ? <Alert tone="danger">{error}</Alert> : null}<Button type="submit" disabled={busy}>{busy ? "Reenviando…" : "Corrigir e reenviar"}</Button></form>;
}

function Fragment({ children }: { children: React.ReactNode }) { return <>{children}</>; }

function statusFromSession(session: Session | undefined): Status | undefined {
  if (!session) return undefined;
  const selectedOrigin = confirmedSteps(session.completedSteps).origin;
  const originPending = selectedOrigin?.mode === "FIXED_ORIGIN" && session.state !== "SUBMITTED";
  if (session.state === "FAILED") return { state: session.state, nextAction: "RETRY", originStatus: "FAILED", deliveryStatus: "NOT_STARTED", inspectionId: null, requestId: null, responsibleEmail: null, responsibilityStatus: null, responsibilityVersion: null, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: null };
  if (session.state === "SUBMITTED") return { state: session.state, nextAction: "WAIT_FOR_DELIVERY", originStatus: selectedOrigin?.mode === "FIXED_ORIGIN" ? "ACTIVE" : "NOT_REQUIRED", deliveryStatus: "NOT_STARTED", inspectionId: null, requestId: null, responsibleEmail: null, responsibilityStatus: null, responsibilityVersion: null, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: null };
  if (session.state === "READY_TO_SUBMIT") return { state: session.state, nextAction: "REVIEW_AND_SUBMIT", originStatus: "READY", deliveryStatus: "NOT_STARTED", inspectionId: null, requestId: null, responsibleEmail: null, responsibilityStatus: null, responsibilityVersion: null, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: null };
  return { state: session.state, nextAction: originPending ? "WAIT_FOR_ORIGIN" : "CONTINUE_ONBOARDING", originStatus: originPending || session.state === "ORIGIN_PENDING" ? "PENDING" : "NOT_REQUIRED", deliveryStatus: "NOT_STARTED", inspectionId: null, requestId: null, responsibleEmail: null, responsibilityStatus: null, responsibilityVersion: null, deliveryFailureCode: null, canCorrectResponsibleEmail: false, updatedAt: null };
}
