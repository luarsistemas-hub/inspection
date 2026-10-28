import { describe, expect, it } from "vitest";
import { presentCaptureError, presentCaptureSource, presentCaptureStatus, presentMediaFeedback, presentMediaStatus, presentReplacementNotice, presentRequirementLabel, presentRequirementOption, presentRequirementSection, presentSubmissionBlock, presentUploadFailure, presentUploadProgress } from "../../app/capture/[linkToken]/presentation";

describe("capture presenters", () => {
  it("localizes legacy requirements and dynamic states", () => {
    expect(presentCaptureStatus("SUBMITTED")).toBe("Enviada");
    expect(presentRequirementSection("property")).toBe("Imóvel");
    expect(presentRequirementSection("Referência")).toBe("Referência");
    expect(presentRequirementSection("  ")).toBe("Seção não informada");
    expect(presentRequirementSection("DINING_ROOM")).toBe("Sala de jantar");
    expect(presentRequirementOption("DINING_ROOM", "DINING_ROOM")).toBe("Sala de jantar");
    expect(presentRequirementOption("Sala de jantar", "Sala de jantar")).toBe("Sala de jantar");
    expect(presentRequirementOption("Ambiente", "Sala de jantar")).toBe("Ambiente: Sala de jantar");
    expect(presentRequirementLabel("Property overview")).toBe("Visão geral do imóvel");
    expect(presentRequirementLabel("DINING_ROOM")).toBe("Sala de jantar");
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
      guidance: "A foto não poderá ser enviada nesta vistoria. Tire outra foto ou aguarde a revisão do gestor."
    });
  });

  it("translates transient upload errors into an actionable message", () => {
    expect(presentUploadFailure({ message: "media verification and screening are pending" })).toBe("A foto foi recebida, mas a verificação ainda não terminou. Aguarde alguns segundos e toque em “Retomar envio”.");
    expect(presentUploadFailure({ message: "network request failed" })).toBe("Não foi possível concluir agora por causa da conexão. Verifique sua internet e toque em “Retomar envio”.");
    expect(presentUploadFailure({ message: "environment requirement is required" })).toContain("O ambiente escolhido não está disponível");
    expect(presentUploadFailure({ message: "invalid environment Sala de jantar" })).toContain("Ela continua salva neste dispositivo");
    expect(presentCaptureError(new Error("environment requirement is required"), "Não foi possível salvar a foto.")).toBe("Não foi possível salvar a foto.");
    expect(presentCaptureError(new Error("A localização não está disponível."), "Não foi possível salvar a foto.")).toBe("A localização não está disponível.");
    expect(presentCaptureError(new Error("invalid Área value"), "Não foi possível salvar a foto.")).toBe("Não foi possível salvar a foto.");
    expect(presentCaptureError(new Error("Image decoding is unavailable"), "Não foi possível preparar a foto.")).toContain("não conseguiu abrir a imagem");
    expect(presentUploadProgress({ phase: "preparing", percent: 0, completedParts: 0, totalParts: 1 })).toBe("Preparando envio");
    expect(presentUploadProgress({ phase: "complete", percent: 100, completedParts: 1, totalParts: 1 })).toBe("Envio concluído");
  });

  it("explains why a review cannot be submitted", () => {
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 2, allRequirementsSatisfied: false })).toBe("2 imagens foram bloqueadas pela análise automática. Tire outras fotos ou aguarde a revisão do gestor.");
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 0, allRequirementsSatisfied: false })).toContain("Ainda não há evidências válidas");
    expect(presentSubmissionBlock({ online: true, pending: 0, blocked: 0, allRequirementsSatisfied: true })).toBe("");
  });
});

describe("replacement notice", () => {
  it("asks for a new recording without technical codes and shows the deadline", () => {
    const text = presentReplacementNotice("2026-10-22T15:00:00Z");
    expect(text).toContain("Precisamos de uma nova gravação.");
    expect(text).toContain("Envie até 22 de outubro de 2026");
    expect(text).not.toMatch(/SENSITIVE|BLOCKED|PREPARATION|REPLACEMENT/);
  });
  it("omits the deadline when it is missing or invalid", () => {
    expect(presentReplacementNotice()).toBe("Precisamos de uma nova gravação. O vídeo anterior não pôde ser analisado.");
    expect(presentReplacementNotice("invalid")).toBe(presentReplacementNotice(null));
  });
});
