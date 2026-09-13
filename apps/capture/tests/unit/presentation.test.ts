import { describe, expect, it } from "vitest";
import { presentCaptureSource, presentCaptureStatus, presentMediaStatus, presentRequirementLabel, presentRequirementSection } from "../../app/capture/[linkToken]/presentation";

describe("capture presenters", () => {
  it("localizes legacy requirements and dynamic states", () => {
    expect(presentCaptureStatus("SUBMITTED")).toBe("Enviada");
    expect(presentRequirementSection("property")).toBe("Imóvel");
    expect(presentRequirementLabel("Property overview")).toBe("Visão geral do imóvel");
    expect(presentCaptureSource("CAMERA_ONLY")).toBe("Câmera obrigatória");
    expect(presentCaptureStatus("FUTURE")).toBe("Situação não reconhecida");
    expect(presentMediaStatus("SCREENED")).toBe("Em análise");
    expect(presentMediaStatus("FUTURE")).toBe("Situação não reconhecida");
  });
});
