import { Icon } from "./icon.js";

export type ProductName = "Admin" | "Dashboard" | "Capture" | "Onboarding";
export type ProductIdentityProps = { product: ProductName; context?: string };

/** Identifies the active Inspection product without imposing a product shell. */
export function ProductIdentity({ product, context }: ProductIdentityProps) {
  const productLabel = product === "Capture" ? "Captura" : product;
  return <span aria-label={context ? `Inspeção ${productLabel}: ${context}` : `Inspeção ${productLabel}`} className="inspection-identity"><Icon name="check" /> <span>Inspeção · {productLabel}</span>{context ? <span>— {context}</span> : null}</span>;
}
