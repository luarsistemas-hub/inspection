import type { Metadata } from "next";
import "@inspection/design-system/styles.css";
import "./onboarding.css";

export const metadata: Metadata = { title: "Começar vistoria", description: "Cadastro inicial para vistorias imobiliárias." };

export default function RootLayout({ children }: Readonly<{ children: React.ReactNode }>) {
  return <html lang="pt-BR"><body>{children}</body></html>;
}
