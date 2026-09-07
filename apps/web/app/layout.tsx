import type { Metadata } from "next";
import "./styles.css";

// CSP nonce values are generated per request by middleware, so pages must not
// be statically emitted without the request nonce that Next attaches to scripts.
export const dynamic = "force-dynamic";

export const metadata: Metadata = {
  title: "Inspeção",
  description: "Plataforma de inspeções",
  manifest: "/manifest.webmanifest"
};

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="pt-BR"><body>{children}</body></html>;
}
