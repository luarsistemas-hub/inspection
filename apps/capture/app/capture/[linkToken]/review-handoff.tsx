import { captureReviewPresentation } from "@/pwa/environment-flow";

type Handoff = { inspectionId: string; originVersionId?: string | null } | null | undefined;

/** "Revisar agora" entry point shown on the confirmation when the invitation carries a reviewer. */
export function ReviewHandoff({ handoff }: { handoff: Handoff }) {
  if (!handoff) return null;
  const review = captureReviewPresentation({ inspectionId: handoff.inspectionId, originVersionId: handoff.originVersionId ?? null }, process.env.NEXT_PUBLIC_DASHBOARD_URL ?? "");
  return <><p>{review.preparing ? "A revisão está sendo preparada. Você poderá voltar mais tarde." : "A revisão da vistoria está disponível."}</p>{review.href ? <a className="button button--primary" href={review.href}>Revisar agora</a> : <p>A revisão poderá ser acessada pelo painel da vistoria.</p>}</>;
}
