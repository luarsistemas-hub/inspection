import { describe, expect, it } from "vitest";
import { presentOnboardingLabel, presentOnboardingOption, presentOnboardingStatus } from "@/features/onboarding/presentation";

describe("onboarding presenters", () => {
  it("localizes labels, choices, and statuses without exposing codes", () => {
    expect(presentOnboardingLabel("Agency")).toBe("Imobiliária");
    expect(presentOnboardingOption("RENTAL")).toBe("Locação");
    expect(presentOnboardingStatus("WAIT_FOR_DELIVERY")).toBe("Aguardar envio");
    expect(presentOnboardingStatus("FUTURE")).toBe("Situação não reconhecida");
  });
});
