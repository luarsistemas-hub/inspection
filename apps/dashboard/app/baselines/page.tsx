import { DashboardShell } from "@/features/dashboard/dashboard-shell";
import { Suspense } from "react";

export default function BaselinesPage() {
  return <Suspense fallback={<main>Carregando inventários…</main>}><DashboardShell section="Inventários" /></Suspense>;
}
