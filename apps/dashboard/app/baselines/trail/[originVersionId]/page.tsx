import { DashboardShell } from "@/features/dashboard/dashboard-shell";
import { Suspense } from "react";

export default function BaselineTrailPage() {
  return <Suspense fallback={<main>Carregando inventários…</main>}><DashboardShell section="Inventários" /></Suspense>;
}
