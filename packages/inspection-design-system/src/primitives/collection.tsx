import type { ReactNode } from "react";

type BreadcrumbItem = { href?: string; label: string };

export type BreadcrumbsProps = { items: BreadcrumbItem[]; label?: string };

export function Breadcrumbs({ items, label = "Navegação estrutural" }: BreadcrumbsProps) {
  return <nav aria-label={label} className="inspection-breadcrumbs"><ol>{items.map((item, index) => <li key={`${item.label}-${index}`}>{item.href && index < items.length - 1 ? <a href={item.href}>{item.label}</a> : <span aria-current={index === items.length - 1 ? "page" : undefined}>{item.label}</span>}</li>)}</ol></nav>;
}

export type FilterBarProps = { children: ReactNode; onReset?: () => void; resetLabel?: string };

export function FilterBar({ children, onReset, resetLabel = "Limpar filtros" }: FilterBarProps) {
  return <form className="inspection-filter-bar" onSubmit={(event) => event.preventDefault()} aria-label="Filtros">{children}{onReset ? <button className="inspection-button inspection-button--secondary" type="button" onClick={onReset}>{resetLabel}</button> : null}</form>;
}

export type DataTableColumn = { id: string; label: string };
export type DataTableProps = { caption: string; columns: DataTableColumn[]; children: ReactNode; mobileLabel?: string };

export function DataTable({ caption, columns, children, mobileLabel = "Registros" }: DataTableProps) {
  return <div className="inspection-data-table" aria-label={mobileLabel}><table><caption>{caption}</caption><thead><tr>{columns.map((column) => <th key={column.id} scope="col">{column.label}</th>)}</tr></thead><tbody>{children}</tbody></table></div>;
}

export type PaginationProps = { page: number; hasNextPage: boolean; onPrevious: () => void; onNext: () => void };

export function Pagination({ page, hasNextPage, onPrevious, onNext }: PaginationProps) {
  return <nav aria-label="Paginação" className="inspection-pagination"><button className="inspection-button inspection-button--secondary" type="button" disabled={page <= 1} onClick={onPrevious}>Anterior</button><span role="status" aria-live="polite">Página {page}</span><button className="inspection-button inspection-button--secondary" type="button" disabled={!hasNextPage} onClick={onNext}>Próxima</button></nav>;
}
