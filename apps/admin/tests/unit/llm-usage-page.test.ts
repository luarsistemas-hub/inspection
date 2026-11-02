import { describe, expect, it } from "vitest";
import { formatAudioDuration, usageCostCoverageLabel } from "@/features/llm-usage/llm-usage-page";

describe("Admin LLM usage transcription presentation", () => {
  it("shows clip duration separately from request latency", () => {
    expect(formatAudioDuration(30_000)).toBe("30 s");
    expect(formatAudioDuration(1_250)).toBe("1,25 s");
  });

  it("labels an unavailable clip duration and partial cost totals", () => {
    expect(formatAudioDuration(null)).toBe("Não informado");
    expect(usageCostCoverageLabel(false)).toBe("cobertura de custo parcial");
  });
});
