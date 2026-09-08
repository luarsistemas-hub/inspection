import { Children, cloneElement, isValidElement, useId, type InputHTMLAttributes, type ReactElement, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";

export type FieldProps = { label: ReactNode; children: ReactNode; hint?: ReactNode; error?: ReactNode; required?: boolean };
export type InputProps = InputHTMLAttributes<HTMLInputElement>;
export type SelectProps = SelectHTMLAttributes<HTMLSelectElement>;
export type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

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

/** Renders a multiline field with shared accessible visual treatment. */
export function Textarea({ className = "", ...props }: TextareaProps) {
  return <textarea className={`inspection-textarea ${className}`.trim()} {...props} />;
}
