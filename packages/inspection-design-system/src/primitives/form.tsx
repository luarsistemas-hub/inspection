import { useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";

export type FieldProps = { label: ReactNode; children: ReactNode; hint?: ReactNode; error?: ReactNode; required?: boolean };
export type InputProps = InputHTMLAttributes<HTMLInputElement>;
export type SelectProps = SelectHTMLAttributes<HTMLSelectElement>;
export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

/** Associates an input with its label, help text, and error state. */
export function Field({ label, children, hint, error, required = false }: FieldProps) {
  const id = useId();
  const descriptionId = hint || error ? `${id}-description` : undefined;
  return <label className="inspection-field">
    <span>{label}{required ? <span aria-hidden="true"> *</span> : null}</span>
    <span aria-describedby={descriptionId}>{children}</span>
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

/** Renders a multiline field with shared accessible visual treatment. */
export function Textarea({ className = "", ...props }: TextareaProps) {
  return <textarea className={`inspection-textarea ${className}`.trim()} {...props} />;
}
