import "@inspection/design-system/styles.css";
import "./styles.css";
import type { Metadata } from "next";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Inspection Captura", description: "Registro guiado de vistoria", manifest: "/manifest.webmanifest" };
export default function Layout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="pt-BR"><body>{children}</body></html>; }
