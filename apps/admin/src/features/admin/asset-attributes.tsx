"use client";

import { Field, Input, Select } from "@inspection/design-system";

export type SegmentAttributeSchema = {
  required?: string[];
  properties?: Record<string, { type?: string; enum?: string[]; maxLength?: number }>;
};

const labels: Record<string, string> = {
  propertyType: "Tipo do imóvel",
  purpose: "Finalidade",
};
const optionLabels: Record<string, string> = {
  APARTMENT: "Apartamento",
  HOUSE: "Casa",
  COMMERCIAL: "Imóvel comercial",
  LAND: "Terreno",
  SALE: "Venda",
  RENTAL: "Locação",
  MAINTENANCE: "Manutenção",
  INSURANCE: "Seguro",
};

export function AssetAttributeFields({
  schema,
  values,
  onChange,
}: {
  schema: SegmentAttributeSchema;
  values: Record<string, unknown>;
  onChange: (key: string, value: unknown) => void;
}) {
  const required = new Set(schema.required ?? []);
  return <>
    {Object.entries(schema.properties ?? {}).map(([key, property]) => {
      const label = labels[key] ?? key.replace(/([a-z])([A-Z])/g, "$1 $2").replace(/^./, (value) => value.toUpperCase());
      const value = values[key];
      if (property.enum?.length) return <Field key={key} label={label} required={required.has(key)}>
        <Select required={required.has(key)} value={String(value ?? "")} onChange={(event) => onChange(key, event.target.value)}>
          <option value="">Selecione</option>
          {property.enum.map((option) => <option key={option} value={option}>{optionLabels[option] ?? option}</option>)}
        </Select>
      </Field>;
      if (property.type === "boolean") return <Field key={key} label={label} required={required.has(key)}>
        <Select required={required.has(key)} value={value === true ? "true" : value === false ? "false" : ""} onChange={(event) => onChange(key, event.target.value === "" ? "" : event.target.value === "true")}>
          <option value="">Selecione</option><option value="true">Sim</option><option value="false">Não</option>
        </Select>
      </Field>;
      const type = property.type === "number" || property.type === "integer" ? "number" : "text";
      return <Field key={key} label={label} required={required.has(key)}>
        <Input required={required.has(key)} type={type} step={property.type === "integer" ? 1 : undefined} maxLength={property.maxLength} value={value == null ? "" : String(value)} onChange={(event) => onChange(key, event.target.value)} />
      </Field>;
    })}
  </>;
}

export function normalizeAssetAttributes(schema: SegmentAttributeSchema, values: Record<string, unknown>): Record<string, unknown> {
  const attributes: Record<string, unknown> = {};
  for (const [key, property] of Object.entries(schema.properties ?? {})) {
    const value = values[key];
    if (value === "" || value === undefined || value === null) continue;
    if (property.type === "number" || property.type === "integer") {
      const parsed = Number(value);
      if (Number.isFinite(parsed)) attributes[key] = parsed;
    } else {
      attributes[key] = value;
    }
  }
  return attributes;
}
