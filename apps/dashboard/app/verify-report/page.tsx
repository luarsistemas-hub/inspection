import { Suspense } from "react";
import { PublicReportVerification } from "@/features/dashboard/public-report-verification";

export default function VerifyReportPage() {
  return <main className="public-verification"><Suspense fallback={<p role="status">Carregando verificação…</p>}><PublicReportVerification /></Suspense></main>;
}
