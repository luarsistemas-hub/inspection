import { describe, expect, it } from "vitest";
import { presentCaptureSource, presentCaptureStatus, presentMediaFeedback, presentMediaStatus, presentRequirementLabel, presentRequirementSection, presentSubmissionBlock, presentUploadFailure } from "../../app/capture/[linkToken]/presentation";

describe("capture presenters", () => {
  it("localizes legacy requirements and dynamic states", () => {
    expect(presentCaptureStatus("SUBMITTED")).toBe("Enviada");
    expect(presentRequirementSection("property")).toBe("Imóvel");
    expect(presentRequirementSection("Referência")).toBe("Referência");
    expect(presentRequirementSection("  ")).toBe("Seção não informada");
    expect(presentRequirementLabel("Property overview")).toBe("Visão geral do imóvel");
    expect(presentCaptureSource("CAMERA_ONLY")).toBe("Câmera obrigatória");
    expect(presentCaptureStatus("FUTURE")).toBe("Situação não reconhecida");
    expect(presentMediaStatus("SCREENED")).toBe("Bloqueada para revisão");
    expect(presentMediaStatus("FUTURE")).toBe("Situação não reconhecida");
  });

  it("explains blocked media and gives the participant a next step", () => {
    expect(presentMediaFeedback("SCREENED")).toEqual({
      tone: "warning",
      title: "Imagem bloqueada para revisão",
      message: "A análise automática encontrou um conteúdo que precisa ser revisado para este requisito.",
      guidance: "Confira se a foto mostra somente o imóvel. Se a análise estiver incorreta, explique o motivo e envie uma declaração de falso positivo."
    });
  });

  it("translates transient upload errors into an actionable message", () => {
    expect(presentUploadFailure({ message: "media verification and screening are pending" })).toBe("A foto foi recebida, mas a verificação ainda não terminou. Aguarde alguns segundos e toque em “Retomar envio”.");
    expect(presentUploadFailure({ message: "network request failed" })).toBe("Não foi possível concluir agora por causa da conexão. Verifique sua internet e toque em “Retomar envio”.");
  });

  it("explains why a review cannot be submitted", () => {
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 2, allRequirementsSatisfied: false })).toBe("2 imagens foram bloqueadas pela análise automática. Volte às evidências para substituir as fotos ou declarar um falso positivo.");
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 0, allRequirementsSatisfied: false })).toContain("Ainda não há evidências válidas");
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 0, allRequirementsSatisfied: true })).toBe("");
  });
});
