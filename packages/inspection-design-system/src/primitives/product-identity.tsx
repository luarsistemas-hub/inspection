import { Icon } from "./icon.js";

export type ProductName = "Admin" | "Dashboard" | "Capture";
export type ProductIdentityProps = { product: ProductName; context?: string };

/** Identifies the active Inspection product without imposing a product shell. */
export function ProductIdentity({ product, context }: ProductIdentityProps) {
  return <span aria-label={context ? `Inspeção ${product}: ${context}` : `Inspeção ${product}`} className="inspection-identity"><Icon name="check" /> <span>Inspeção · {product}</span>{context ? <span>— {context}</span> : null}</span>;
}
