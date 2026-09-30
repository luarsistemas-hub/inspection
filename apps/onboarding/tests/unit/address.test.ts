import { describe, expect, it } from "vitest";
import { emptyPostalAddress, formatPostalAddress, validPostalAddress } from "@inspection/address/core";

describe("Brazilian postal address", () => {
  it("accepts CEP, UF, number and no-number forms", () => {
    const value = { ...emptyPostalAddress(), postalCode: "01001-000", street: "Praça da Sé", number: "1", city: "São Paulo", state: "sp" };
    expect(validPostalAddress(value)).toBe(true);
    expect(validPostalAddress({ ...value, number: "", withoutNumber: true })).toBe(true);
    expect(validPostalAddress({ ...value, state: "XX" })).toBe(false);
    expect(validPostalAddress({ ...value, number: "1", withoutNumber: true })).toBe(false);
  });

  it("rejects incomplete drafts and values beyond the server limits", () => {
    expect(validPostalAddress({})).toBe(false);
    const value = { ...emptyPostalAddress(), postalCode: "01001000", street: "Praça da Sé", number: "1", city: "São Paulo", state: "SP" };
    expect(validPostalAddress({ ...value, complement: "a".repeat(201) })).toBe(false);
    expect(validPostalAddress({ ...value, municipalityCode: "123" })).toBe(false);
  });

  it("formats optional address parts consistently", () => {
    const value = { ...emptyPostalAddress(), postalCode: "01001000", street: " Praça da Sé ", withoutNumber: true, district: "Sé", city: "São Paulo", state: "sp" };
    expect(formatPostalAddress(value)).toBe("Praça da Sé, s/n — Sé — São Paulo/SP — CEP 01001-000");
  });
});
