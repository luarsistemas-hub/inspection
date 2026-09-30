export type PostalAddress = {
  countryCode: "BR";
  postalCode: string;
  street: string;
  number: string;
  withoutNumber: boolean;
  complement: string;
  district: string;
  city: string;
  state: string;
  municipalityCode: string;
  reference: string;
};

export type PostalLookup = Pick<PostalAddress, "postalCode"> & { street?: string | null; district?: string | null; city?: string | null; state?: string | null; municipalityCode?: string | null };

export const emptyPostalAddress = (): PostalAddress => ({ countryCode: "BR", postalCode: "", street: "", number: "", withoutNumber: false, complement: "", district: "", city: "", state: "", municipalityCode: "", reference: "" });
const brazilianStates = new Set("AC AL AP AM BA CE DF ES GO MA MT MS MG PA PB PR PE PI RJ RN RS RO RR SC SP SE TO".split(" "));

export function formatPostalAddress(value: PostalAddress): string {
  const street = [value.street.trim(), value.withoutNumber ? "s/n" : value.number.trim(), value.complement.trim()].filter(Boolean).join(", ");
  const locality = [value.district.trim(), value.city.trim()].filter(Boolean).join(" — ") + (value.state.trim() ? `/${value.state.trim().toUpperCase()}` : "");
  const postalCode = value.postalCode.replace(/\D/g, "");
  return [street, locality, postalCode ? `CEP ${postalCode.length === 8 ? `${postalCode.slice(0, 5)}-${postalCode.slice(5)}` : value.postalCode}` : ""].filter(Boolean).join(" — ");
}

export function validPostalAddress(input: unknown): input is PostalAddress {
  if (!input || typeof input !== "object" || Array.isArray(input)) return false;
  const value = input as PostalAddress;
  const limits = { postalCode: 9, street: 200, number: 30, complement: 200, district: 120, city: 120, state: 2, municipalityCode: 7, reference: 300 } as const;
  for (const [key, limit] of Object.entries(limits)) {
    const field = value[key as keyof typeof limits];
    if (typeof field !== "string" || [...field.trim()].length > limit) return false;
  }
  const cep = value.postalCode.trim();
  return /^(\d{8}|\d{5}-\d{3})$/.test(cep)
    && value.street.trim().length > 0
    && typeof value.withoutNumber === "boolean"
    && ((value.withoutNumber && !value.number.trim()) || (!value.withoutNumber && value.number.trim().length > 0))
    && value.city.trim().length > 0
    && brazilianStates.has(value.state.trim().toUpperCase())
    && (!value.municipalityCode.trim() || /^\d{7}$/.test(value.municipalityCode.trim()));
}
