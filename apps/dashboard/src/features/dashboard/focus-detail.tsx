import Link from "next/link";

export function FocusDetail({ title, details, href, actionLabel, eyebrow = "Selecionado", onAction }: {
  title: string;
  details: Array<[label: string, value: string]>;
  href?: string | null;
  actionLabel?: string;
  eyebrow?: string;
  onAction?: () => void;
}) {
  return <aside className="dashboard-focus-detail" aria-label={`${eyebrow}: ${title}`}>
    <span className="dashboard-focus-eyebrow">{eyebrow}</span>
    <h2>{title}</h2>
    <dl>{details.map(([label, value]) => <div key={label}><dt>{label}</dt><dd>{value}</dd></div>)}</dl>
    {href && <Link href={href} onClick={onAction}>{actionLabel ?? "Abrir detalhes"} <span aria-hidden="true">→</span></Link>}
  </aside>;
}
