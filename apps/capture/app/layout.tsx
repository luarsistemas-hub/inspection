import "@inspection/design-system/styles.css";
import "./styles.css";
import type { Metadata } from "next";

export const dynamic = "force-dynamic";
export const metadata: Metadata = { title: "Inspeção Capture", description: "Registro guiado de inspeção", manifest: "/manifest.webmanifest" };
export default function Layout({ children }: Readonly<{ children: React.ReactNode }>) { return <html lang="pt-BR"><body>{children}</body></html>; }
