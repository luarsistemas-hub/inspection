import { Children, cloneElement, isValidElement, useEffect, useId, useRef, useState, type InputHTMLAttributes, type ReactElement, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";

export type FieldProps = { label: ReactNode; children: ReactNode; hint?: ReactNode; error?: ReactNode; required?: boolean };
export type InputProps = InputHTMLAttributes<HTMLInputElement>;
export type SelectProps = SelectHTMLAttributes<HTMLSelectElement>;
export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;
export type ComboboxOption = { value: string; label: string; description?: string };
export type ComboboxProps = {
  value: string;
  options: ComboboxOption[];
  onChange: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
  "aria-label"?: string;
};

/** Associates an input with its label, help text, and error state. */
export function Field({ label, children, hint, error, required = false }: FieldProps) {
  const id = useId();
  const descriptionId = hint || error ? `${id}-description` : undefined;
  const control = Children.map(children, (child) => {
    if (!isValidElement(child)) return child;

    const childProps = child.props as Record<string, unknown>;
    const describedBy = [
      typeof childProps["aria-describedby"] === "string" ? childProps["aria-describedby"] : null,
      descriptionId,
    ].filter(Boolean).join(" ") || undefined;

    return cloneElement(child as ReactElement<Record<string, unknown>>, {
      "aria-describedby": describedBy,
      ...(error ? { "aria-invalid": true } : {}),
      ...(required ? { "aria-required": true, required: true } : {}),
    });
  });

  return <label className="inspection-field">
    <span>{label}{required ? <span aria-hidden="true"> *</span> : null}</span>
    <span>{control}</span>
    {hint || error ? <span id={descriptionId}>{hint ? <span className="inspection-hint">{hint}</span> : null}{error ? <span className="inspection-error" role="alert">{error}</span> : null}</span> : null}
  </label>;
}

/** Renders a text input with shared accessible visual treatment. */
export function Input({ className = "", ...props }: InputProps) {
  return <input className={`inspection-input ${className}`.trim()} {...props} />;
}

/** Renders a select control with shared accessible visual treatment. */
export function Select({ className = "", ...props }: SelectProps) {
  return <select className={`inspection-select ${className}`.trim()} {...props} />;
}

/** Renders an accessible searchable listbox for selecting a known value. */
export function Combobox({ value, options, onChange, placeholder = "Pesquisar…", required = false, disabled = false, "aria-label": ariaLabel }: ComboboxProps) {
  const inputID = useId();
  const listID = `${inputID}-options`;
  const inputRef = useRef<HTMLInputElement>(null);
  const [query, setQuery] = useState("");
  const [open, setOpen] = useState(false);
  const [activeIndex, setActiveIndex] = useState(-1);
  const selected = options.find((option) => option.value === value);
  const filtered = query.trim() ? options.filter((option) => `${option.label} ${option.description ?? ""}`.toLocaleLowerCase().includes(query.trim().toLocaleLowerCase())) : options;

  useEffect(() => {
    setQuery(selected?.label ?? "");
    setActiveIndex(-1);
  }, [selected?.label, value]);

  const choose = (option: ComboboxOption) => {
    onChange(option.value);
    setQuery(option.label);
    setOpen(false);
    setActiveIndex(-1);
    inputRef.current?.setCustomValidity("");
  };
  const resetQuery = () => { setQuery(selected?.label ?? ""); inputRef.current?.setCustomValidity(""); };

  return <div className="inspection-combobox">
    <input
      ref={inputRef}
      id={inputID}
      className="inspection-input"
      role="combobox"
      aria-label={ariaLabel}
      aria-autocomplete="list"
      aria-controls={listID}
      aria-expanded={open}
      aria-activedescendant={activeIndex >= 0 ? `${listID}-${activeIndex}` : undefined}
      autoComplete="off"
      disabled={disabled}
      required={required}
      placeholder={placeholder}
      value={query}
      onFocus={() => setOpen(true)}
      onChange={(event) => { setQuery(event.target.value); setOpen(true); setActiveIndex(0); if (value) onChange(""); event.currentTarget.setCustomValidity("Selecione uma opção da lista."); }}
      onBlur={() => window.setTimeout(() => { resetQuery(); setOpen(false); setActiveIndex(-1); }, 120)}
      onKeyDown={(event) => {
        if (event.key === "ArrowDown") { event.preventDefault(); setOpen(true); setActiveIndex((index) => Math.min(index + 1, filtered.length - 1)); }
        if (event.key === "ArrowUp") { event.preventDefault(); setActiveIndex((index) => Math.max(index - 1, 0)); }
        if (event.key === "Enter" && open && filtered[activeIndex]) { event.preventDefault(); choose(filtered[activeIndex]); }
        if (event.key === "Escape") { event.preventDefault(); resetQuery(); setOpen(false); setActiveIndex(-1); }
      }}
    />
    {open && <ul id={listID} className="inspection-combobox-options" role="listbox" aria-label={ariaLabel ?? "Opções"}>
      {filtered.length ? filtered.map((option, index) => <li
        id={`${listID}-${index}`}
        key={option.value}
        role="option"
        aria-selected={option.value === value}
        data-active={index === activeIndex ? "true" : undefined}
        onMouseDown={(event) => event.preventDefault()}
        onClick={() => choose(option)}
      >
        <strong>{option.label}</strong>{option.description ? <span>{option.description}</span> : null}
      </li>) : <li className="inspection-combobox-empty" role="status">Nenhuma opção encontrada.</li>}
    </ul>}
  </div>;
}

/** Renders a multiline field with shared accessible visual treatment. */
export function Textarea({ className = "", ...props }: TextareaProps) {
  return <textarea className={`inspection-textarea ${className}`.trim()} {...props} />;
}
