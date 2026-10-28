"use client";

import { usePathname } from "next/navigation";
import { BaselinesHome } from "./baselines/baselines-home";
import { InventoryReview } from "./baselines/inventory-review";
import { InventoryTrailView } from "./baselines/inventory-trail";
import { InventoryWaiting } from "./baselines/inventory-waiting";

/** BaselinesJourney routes the baselines section to history, review, audit trail or the Capture handoff wait. */
export function BaselinesJourney({ canReview, isCustomer = false, readHistory = canReview, canReadTrail = false }: { canReview: boolean; isCustomer?: boolean; readHistory?: boolean; canReadTrail?: boolean }) {
  const pathname = usePathname();
  const trailId = pathname.match(/^\/baselines\/trail\/([^/]+)$/)?.[1];
  if (trailId && !canReadTrail) return <section role="alert"><h2>Acesso restrito</h2><p>Somente auditores, gestores e administradores podem consultar a trilha de auditoria.</p></section>;
  if (trailId) return <InventoryTrailView versionId={decodeURIComponent(trailId)} />;
  const inspectionId = pathname.match(/^\/baselines\/review\/inspection\/([^/]+)$/)?.[1];
  const reviewId = inspectionId ? undefined : pathname.match(/^\/baselines\/review\/([^/]+)$/)?.[1];
  if (inspectionId) return <InventoryWaiting inspectionId={decodeURIComponent(inspectionId)} canReview={canReview} />;
  if (reviewId && !canReview) return <section role="alert"><h2>Acesso restrito</h2><p>Somente operadores autorizados podem revisar um rascunho.</p></section>;
  if (reviewId) return <InventoryReview reviewId={decodeURIComponent(reviewId)} />;
  return <BaselinesHome canReview={canReview} isCustomer={isCustomer} readHistory={readHistory} canReadTrail={canReadTrail} />;
}
