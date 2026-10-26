"use client";

import { usePathname } from "next/navigation";
import { BaselinesHome } from "./baselines/baselines-home";
import { InventoryReview } from "./baselines/inventory-review";
import { InventoryWaiting } from "./baselines/inventory-waiting";

/** BaselinesJourney routes the baselines section to history, review or the Capture handoff wait. */
export function BaselinesJourney({ canReview, isCustomer = false, readHistory = canReview }: { canReview: boolean; isCustomer?: boolean; readHistory?: boolean }) {
  const pathname = usePathname();
  const inspectionId = pathname.match(/^\/baselines\/review\/inspection\/([^/]+)$/)?.[1];
  const reviewId = inspectionId ? undefined : pathname.match(/^\/baselines\/review\/([^/]+)$/)?.[1];
  if (inspectionId) return <InventoryWaiting inspectionId={decodeURIComponent(inspectionId)} canReview={canReview} />;
  if (reviewId && !canReview) return <section role="alert"><h2>Acesso restrito</h2><p>Somente operadores autorizados podem revisar um rascunho.</p></section>;
  if (reviewId) return <InventoryReview reviewId={decodeURIComponent(reviewId)} />;
  return <BaselinesHome canReview={canReview} isCustomer={isCustomer} readHistory={readHistory} />;
}
