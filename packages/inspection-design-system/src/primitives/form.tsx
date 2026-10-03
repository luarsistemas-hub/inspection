"use client";

import { Children, cloneElement, forwardRef, isValidElement, useId, type FieldsetHTMLAttributes, type InputHTMLAttributes, type ReactElement, type ReactNode, type Ref, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { ComboBox as AriaComboBox, Input as AriaInput, ListBox, ListBoxItem, Popover } from "react-aria-components";

export type FieldProps = { label: ReactNode; children: ReactNode; hint?: ReactNode; error?: ReactNode; required?: boolean };
export type ControlAccessibilityProps = Pick<InputHTMLAttributes<HTMLInputElement>, "id" | "aria-label" | "aria-labelledby" | "aria-describedby" | "aria-invalid">;
export type InputProps = InputHTMLAttributes<HTMLInputElement>;
export type SelectProps = SelectHTMLAttributes<HTMLSelectElement>;
export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;
export type CheckboxProps = InputHTMLAttributes<HTMLInputElement> & { label: ReactNode };
export type RadioProps = InputHTMLAttributes<HTMLInputElement> & { label: ReactNode };
export type ChoiceGroupProps = FieldsetHTMLAttributes<HTMLFieldSetElement> & { legend: ReactNode; children: ReactNode };
export type ErrorSummaryProps = { title?: string; errors: Array<{ fieldId?: string; message: string }> };
export type ComboboxOption = { value: string; label: string; description?: string };
export type ComboboxProps = ControlAccessibilityProps & {
  value: string;
  options: ComboboxOption[];
  onChange: (value: string) => void;
  onInputChange?: (value: string) => void;
  placeholder?: string;
  required?: boolean;
  disabled?: boolean;
  inputRef?: Ref<HTMLInputElement>;
};

/** Associates an input with its label, help text, and error state. */
export function Field({ label, children, hint, error, required = false }: FieldProps) {
  const generatedId = useId();
  const controlId = isValidElement(children) && typeof (children.props as Record<string, unknown>).id === "string" ? (children.props as Record<string, string>).id : generatedId;
  const labelId = `${generatedId}-label`;
  const hintId = hint ? `${generatedId}-hint` : undefined;
  const errorId = error ? `${generatedId}-error` : undefined;
  const control = Children.map(children, (child) => {
    if (!isValidElement(child)) return child;

    const childProps = child.props as Record<string, unknown>;
    const describedBy = [
      typeof childProps["aria-describedby"] === "string" ? childProps["aria-describedby"] : null,
      hintId,
      errorId,
    ].filter(Boolean).join(" ") || undefined;
    const labelledBy = typeof childProps["aria-labelledby"] === "string" ? childProps["aria-labelledby"] : labelId;

    return cloneElement(child as ReactElement<Record<string, unknown>>, {
      id: controlId,
      "aria-describedby": describedBy,
      ...(!childProps["aria-label"] && !childProps["aria-labelledby"] ? { "aria-labelledby": labelledBy } : {}),
      ...(error ? { "aria-invalid": true } : {}),
      ...(required ? { "aria-required": true, required: true } : {}),
    });
  });

  return <div className="inspection-field">
    <label id={labelId} htmlFor={controlId}>{label}{required ? <span aria-hidden="true"> *</span> : null}</label>
    <span>{control}</span>
    {hint ? <span className="inspection-hint" id={hintId}>{hint}</span> : null}
    {error ? <span className="inspection-error" id={errorId}>{error}</span> : null}
  </div>;
}

/** Renders a text input with shared accessible visual treatment. */
export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ className = "", ...props }, ref) {
  return <input className={`inspection-input ${className}`.trim()} ref={ref} {...props} />;
});

/** Renders a select control with shared accessible visual treatment. */
export const Select = forwardRef<HTMLSelectElement, SelectProps>(function Select({ className = "", ...props }, ref) {
  return <select className={`inspection-select ${className}`.trim()} ref={ref} {...props} />;
});

/** Renders an accessible searchable listbox for selecting a known value. */
export function Combobox({ value, options, onChange, onInputChange, placeholder = "Pesquisar…", required = false, disabled = false, inputRef, ...accessibility }: ComboboxProps) {
  const selectedKey = options.some((option) => option.value === value) ? value : null;
  const accessibleLabel = accessibility["aria-label"];
  return <AriaComboBox allowsEmptyCollection aria-label={accessibleLabel} aria-labelledby={accessibility["aria-labelledby"]} className="inspection-combobox" isDisabled={disabled} isRequired={required} menuTrigger="focus" onInputChange={onInputChange} onSelectionChange={(key) => onChange(typeof key === "string" ? key : "")} selectedKey={selectedKey}>
    <AriaInput {...accessibility} className="inspection-input" placeholder={placeholder} ref={inputRef} />
    <Popover className="inspection-combobox-popover"><ListBox aria-label={accessibleLabel ?? "Opções"} className="inspection-combobox-options" renderEmptyState={() => <span className="inspection-combobox-empty">Nenhuma opção encontrada.</span>}>{options.map((option) => <ListBoxItem id={option.value} key={option.value} textValue={option.label}><strong>{option.label}</strong>{option.description ? <span>{option.description}</span> : null}</ListBoxItem>)}</ListBox></Popover>
  </AriaComboBox>;
}

/** Renders a multiline field with shared accessible visual treatment. */
export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea({ className = "", ...props }, ref) {
  return <textarea className={`inspection-textarea ${className}`.trim()} ref={ref} {...props} />;
});

/** Renders a native checkbox with a visible label. */
export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox({ className = "", label, ...props }, ref) {
  return <label className={`inspection-choice ${className}`.trim()}><input ref={ref} type="checkbox" {...props} /><span>{label}</span></label>;
});

/** Renders a native radio with a visible label. */
export const Radio = forwardRef<HTMLInputElement, RadioProps>(function Radio({ className = "", label, ...props }, ref) {
  return <label className={`inspection-choice ${className}`.trim()}><input ref={ref} type="radio" {...props} /><span>{label}</span></label>;
});

/** Groups related native choices using their native fieldset semantics. */
export function ChoiceGroup({ legend, children, className = "", ...props }: ChoiceGroupProps) {
  return <fieldset className={`inspection-choice-group ${className}`.trim()} {...props}><legend>{legend}</legend>{children}</fieldset>;
}

/** Links form errors to existing controls without manufacturing focus targets. */
export function ErrorSummary({ title = "Revise os campos indicados", errors }: ErrorSummaryProps) {
  return <section className="inspection-error-summary" aria-labelledby="inspection-error-summary-title"><h2 id="inspection-error-summary-title">{title}</h2><ul>{errors.map((error, index) => <li key={`${error.fieldId ?? "form"}-${index}`}>{error.fieldId ? <a href={`#${error.fieldId}`} onClick={(event) => { const target = document.getElementById(error.fieldId!); if (target instanceof HTMLElement) { event.preventDefault(); target.focus(); } }}>{error.message}</a> : error.message}</li>)}</ul></section>;
}
