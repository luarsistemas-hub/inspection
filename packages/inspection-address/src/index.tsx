"use client";

import { useEffect, useRef, useState, type InputHTMLAttributes } from "react";
import { type PostalAddress, type PostalLookup } from "./core";
export * from "./core";

const brazilianStates = [
  "AC", "AL", "AP", "AM", "BA", "CE", "DF", "ES", "GO", "MA", "MT", "MS", "MG",
  "PA", "PB", "PR", "PE", "PI", "RJ", "RN", "RS", "RO", "RR", "SC", "SP", "SE", "TO",
];

type AddressFormProps = {
  value: PostalAddress;
  onChange: (value: PostalAddress) => void;
  onLookup: (postalCode: string) => Promise<PostalLookup | null>;
  prefix: string;
  disabled?: boolean;
  autoLookup?: boolean;
  layout?: "default" | "essential-fields";
};

export function AddressForm({ value, onChange, onLookup, prefix, disabled = false, autoLookup = false, layout = "default" }: AddressFormProps) {
  const [loading, setLoading] = useState(false);
  const [message, setMessage] = useState("");
  const [detailsOpen, setDetailsOpen] = useState(false);
  const generation = useRef(0);
  const latest = useRef({ value, onChange });
  const lookupAction = useRef<() => Promise<void>>(async () => {});
  const lookupTimer = useRef<number | undefined>(undefined);
  useEffect(() => { latest.current = { value, onChange }; }, [value, onChange]);
  useEffect(() => () => { generation.current += 1; }, []);
  useEffect(() => {
    generation.current += 1;
    setLoading(false);
    setMessage("");
  }, [value.postalCode, disabled]);

  const update = (key: keyof PostalAddress, next: string | boolean) => {
    const updated = { ...value, [key]: next };
    if (key === "withoutNumber" && next) updated.number = "";
    if (key === "postalCode" || key === "city" || key === "state") updated.municipalityCode = "";
    if (key === "postalCode") generation.current += 1;
    latest.current = { value: updated, onChange };
    onChange(updated);
  };
  const input = (key: keyof PostalAddress, label: string, required = false, props: InputHTMLAttributes<HTMLInputElement> = {}) => (
    <label className="inspection-address-field" htmlFor={`${prefix}-${key}`}>
      <span>{label}{required ? " *" : ""}</span>
      <input id={`${prefix}-${key}`} name={`${prefix}-${key}`} autoComplete={key === "postalCode" ? "postal-code" : key === "street" ? "address-line1" : key === "complement" ? "address-line2" : key === "city" ? "address-level2" : key === "state" ? "address-level1" : "off"} required={required} value={String(value[key])} onChange={(event) => update(key, event.target.value)} {...props} />
    </label>
  );
  const stateSelect = (
    <label className="inspection-address-field" htmlFor={`${prefix}-state`}>
      <span>UF *</span>
      <select id={`${prefix}-state`} name={`${prefix}-state`} autoComplete="address-level1" required value={value.state.toUpperCase()} onChange={(event) => update("state", event.target.value)}>
        <option value="">Selecione</option>
        {brazilianStates.map((state) => <option key={state} value={state}>{state}</option>)}
      </select>
    </label>
  );
  const lookup = async () => {
    const request = ++generation.current;
    const started = value;
    const postalCode = value.postalCode.replace(/\D/g, "");
    if (!/^(\d{8}|\d{5}-\d{3})$/.test(value.postalCode.trim())) {
      setMessage("Informe um CEP com oito dígitos.");
      return;
    }
    setLoading(true);
    setMessage("");
    try {
      const found = await onLookup(postalCode);
      if (request !== generation.current) return;
      if (!found) {
        setMessage("CEP não encontrado. Você pode preencher o endereço manualmente.");
        return;
      }
      const current = latest.current.value;
      const updated = { ...current };
      // Preserve fields edited while the lookup was in flight, including empty edits.
      for (const key of ["street", "district", "city", "state"] as const) {
        if (current[key] === started[key]) updated[key] = found[key] ?? "";
      }
      if (current.city === started.city && current.state === started.state) updated.municipalityCode = found.municipalityCode ?? "";
      latest.current.onChange(updated);
      setMessage("Endereço consultado. Confira os dados e informe o número ou marque Sem número.");
    } catch {
      if (request === generation.current) setMessage("Consulta indisponível. Você pode preencher o endereço manualmente.");
    } finally {
      if (request === generation.current) setLoading(false);
    }
  };

  lookupAction.current = lookup;
  useEffect(() => {
    if (!autoLookup || disabled || !/^\d{8}$/.test(value.postalCode.replace(/\D/g, ""))) return;
    lookupTimer.current = window.setTimeout(() => { lookupTimer.current = undefined; void lookupAction.current(); }, 350);
    return () => { if (lookupTimer.current !== undefined) window.clearTimeout(lookupTimer.current); };
  }, [autoLookup, disabled, value.postalCode]);

  const compact = layout === "essential-fields";
  const detailsId = `${prefix}-additional-fields`;
  const hasAddressSummary = Boolean(value.street.trim() || value.city.trim() || value.state.trim());
  const summaryStreet = [value.street.trim(), value.withoutNumber ? "s/n" : value.number.trim(), value.complement.trim()].filter(Boolean).join(", ");

  return <fieldset className="inspection-address-form" disabled={disabled}>
    <legend>Endereço do imóvel *</legend>
    <div className="inspection-address-postal-row">
      {input("postalCode", "CEP", true, { inputMode: "numeric", maxLength: 9, pattern: "[0-9]{5}-?[0-9]{3}", title: "Informe um CEP com oito dígitos.", "aria-describedby": `${prefix}-lookup-status` })}
      <button className="inspection-address-lookup" type="button" onClick={() => { if (lookupTimer.current !== undefined) window.clearTimeout(lookupTimer.current); void lookup(); }} disabled={loading} aria-label="Buscar CEP" title={loading ? "Consultando CEP" : "Buscar CEP"} aria-busy={loading}>
        <svg aria-hidden="true" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><circle cx="11" cy="11" r="7" /><path d="m20 20-4-4" /></svg>
      </button>
    </div>
    <p id={`${prefix}-lookup-status`} role="status">{message || (compact && autoLookup ? "A busca do endereço começa ao completar o CEP." : "")}</p>
    {compact && hasAddressSummary ? <div className="inspection-address-summary" aria-live="polite">
      <div className="inspection-address-summary-copy">
        <strong>{summaryStreet}</strong>
        <span>{[value.district.trim(), value.city.trim(), value.state.trim().toUpperCase()].filter(Boolean).join(" · ")}</span>
      </div>
      <button className="inspection-address-edit" type="button" aria-expanded={detailsOpen} aria-controls={detailsId} onClick={() => setDetailsOpen((open) => !open)}>
        {detailsOpen ? "Fechar edição" : "Editar endereço"}
      </button>
    </div> : compact ? <button className="inspection-address-edit" type="button" aria-expanded={detailsOpen} aria-controls={detailsId} onClick={() => setDetailsOpen((open) => !open)}>
      {detailsOpen ? "Fechar edição" : "Preencher endereço manualmente"}
    </button> : null}
    {!compact ? input("street", "Logradouro", true, { maxLength: 200 }) : null}
    <div className="inspection-address-row inspection-address-number-row">
      <div>
        {input("number", "Número", !value.withoutNumber, { maxLength: 30, disabled: value.withoutNumber })}
        <label className="inspection-address-check"><input type="checkbox" checked={value.withoutNumber} onChange={(event) => update("withoutNumber", event.target.checked)} /> Sem número</label>
      </div>
      {input("complement", "Complemento", false, { maxLength: 200 })}
    </div>
    <div id={compact ? detailsId : undefined} className="inspection-address-additional" hidden={compact && !detailsOpen}>
      {compact ? input("street", "Logradouro", true, { maxLength: 200 }) : null}
      <div className="inspection-address-row">
        {input("district", "Bairro / distrito", false, { maxLength: 120 })}
        {input("city", "Cidade", true, { maxLength: 120 })}
        {stateSelect}
      </div>
      {input("reference", "Ponto de referência", false, { maxLength: 300 })}
    </div>
  </fieldset>;
}

export function AddressFieldStyles() {
  return <style>{`
    .inspection-address-form{border:0;padding:0;margin:0;min-width:0}
    .inspection-address-form legend{font-weight:600;margin-bottom:.5rem}
    .inspection-address-form [role=status]:empty{margin:0}
    .inspection-address-row{display:flex;align-items:end;gap:.75rem;flex-wrap:wrap}
    .inspection-address-row>*{flex:1;min-width:min(100%,10rem)}
    .inspection-address-postal-row{display:grid;grid-template-columns:minmax(0,1fr) 3rem;align-items:end;gap:.5rem}
    .inspection-address-postal-row .inspection-address-field{min-width:0}
    .inspection-address-number-row{align-items:start}
    .inspection-address-lookup{box-sizing:border-box;display:grid;place-items:center;width:3rem;height:3rem;margin-bottom:.6rem;padding:.5rem;border:0;border-radius:50%;background:transparent;color:var(--inspection-brand-strong,#2563eb);cursor:pointer}
    .inspection-address-lookup svg{width:1.5rem;height:1.5rem}
    .inspection-address-lookup:hover:not(:disabled){background:color-mix(in srgb,var(--inspection-brand,#2563eb) 10%,transparent)}
    .inspection-address-lookup:focus-visible{outline:var(--inspection-focus-ring,2px solid currentColor);outline-offset:3px}
    .inspection-address-lookup:disabled{opacity:.55;cursor:not-allowed}
    .inspection-address-field{display:flex;flex-direction:column;gap:.35rem;margin:.6rem 0;min-width:0}
    .inspection-address-row>.inspection-address-field{flex:1 1 min(100%,10rem);min-width:min(100%,10rem)}
    .inspection-address-field input,.inspection-address-field select{box-sizing:border-box;width:100%;min-height:48px;padding:.65rem .75rem;border:1px solid var(--inspection-outline,#7b879d);border-radius:.5rem;font:inherit;color:inherit;background:var(--inspection-surface-raised,transparent)}
    .inspection-address-field input:focus-visible,.inspection-address-field select:focus-visible{outline:var(--inspection-focus-ring,2px solid currentColor);outline-offset:2px}
    .inspection-address-field input:disabled,.inspection-address-field select:disabled{opacity:.6}
    .inspection-address-check{display:flex;align-items:center;gap:.5rem;min-height:48px}
    .inspection-address-check input{flex:none;width:1.25rem;height:1.25rem}
    .inspection-address-summary{display:flex;justify-content:space-between;align-items:center;gap:1rem;margin:.5rem 0;padding:.8rem 1rem;border-radius:.5rem;background:color-mix(in srgb,var(--inspection-brand,#2563eb) 8%,var(--inspection-surface-raised,#fff));}
    .inspection-address-summary-copy{display:flex;flex-direction:column;min-width:0;overflow-wrap:anywhere}
    .inspection-address-summary-copy span{font-size:.9em;opacity:.8}
    .inspection-address-edit{flex:none;min-height:44px;padding:.5rem .75rem;border:0;border-radius:.4rem;background:transparent;color:var(--inspection-brand-strong,#2563eb);font:inherit;font-weight:600;cursor:pointer}
    .inspection-address-edit:hover{background:color-mix(in srgb,var(--inspection-brand,#2563eb) 10%,transparent)}
    .inspection-address-edit:focus-visible{outline:var(--inspection-focus-ring,2px solid currentColor);outline-offset:2px}
    .inspection-address-additional[hidden]{display:none}
    @media(max-width:480px){.inspection-address-summary{align-items:flex-start;flex-direction:column;gap:.25rem}.inspection-address-edit{padding-left:0}}
  `}</style>;
}
