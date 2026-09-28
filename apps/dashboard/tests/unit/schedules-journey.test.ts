import { describe, expect, it } from "vitest";
import {
  formatScheduleDateTime,
  instantToLocalDateTime,
  presentScheduleFrequency,
  scheduleEditValues,
  scheduleFrequencyValue,
  validateScheduleSettings,
} from "@/features/dashboard/schedules-journey";

describe("schedule presentation and form rules", () => {
  it.each([
    ["FREQ=DAILY", "DAILY", "Diariamente"],
    ["FREQ=WEEKLY", "WEEKLY", "Semanalmente"],
    ["FREQ=MONTHLY", "MONTHLY", "Mensalmente"],
    ["FREQ=YEARLY", "YEARLY", "Anualmente"],
  ])("presents %s in Portuguese", (rule, value, label) => {
    expect(scheduleFrequencyValue(rule)).toBe(value);
    expect(presentScheduleFrequency(rule)).toBe(label);
  });

  it("keeps advanced recurrence rules marked as custom", () => {
    expect(scheduleFrequencyValue("FREQ=MONTHLY;BYMONTHDAY=15")).toBeNull();
    expect(presentScheduleFrequency("FREQ=MONTHLY;BYMONTHDAY=15")).toBe("Personalizada");
  });

  it("keeps custom rules and legacy starts unchanged when editing other fields", () => {
    expect(scheduleEditValues({ rrule: "FREQ=MONTHLY;BYMONTHDAY=15", timezone: "UTC", startsAt: "2026-09-26T12:00:00Z" }, "CUSTOM", "2026-09-26T09:00")).toEqual({
      rrule: "FREQ=MONTHLY;BYMONTHDAY=15",
      startsAt: "2026-09-26T12:00:00Z",
    });
  });

  it("sends a selected standard frequency and edited local start for São Paulo schedules", () => {
    expect(scheduleEditValues({ rrule: "FREQ=MONTHLY", timezone: "America/Sao_Paulo", startsAt: "2026-09-26T12:00:00Z" }, "WEEKLY", "2026-09-27T10:30")).toEqual({
      rrule: "FREQ=WEEKLY",
      startsAt: "2026-09-27T10:30",
    });
  });

  it("formats instants in São Paulo regardless of the machine timezone", () => {
    const instant = "2026-09-26T12:00:00.000Z";
    expect(instantToLocalDateTime(instant)).toBe("2026-09-26T09:00");
    expect(formatScheduleDateTime(instant)).toContain("09:00");
  });

  it("accepts zero, one, or several distinct reminder offsets within the deadline", () => {
    expect(validateScheduleSettings("60", [])).toEqual({ deadlineMinutes: 60, reminders: [] });
    expect(validateScheduleSettings("60", ["0", "30"])).toEqual({ deadlineMinutes: 60, reminders: [0, 30] });
  });

  it.each([
    ["0", ["30"], "Informe um prazo inteiro maior que zero."],
    ["60.5", ["30"], "Informe um prazo inteiro maior que zero."],
    ["60", ["30", "30"], "Os horários dos lembretes precisam ser diferentes."],
    ["60", ["61"], "Cada lembrete deve ser um número inteiro entre zero e o prazo."],
    ["60", ["-1"], "Cada lembrete deve ser um número inteiro entre zero e o prazo."],
    ["60", ["1.5"], "Cada lembrete deve ser um número inteiro entre zero e o prazo."],
    ["60", ["1", "2", "3", "4"], "Adicione no máximo três lembretes."], // UT-054
  ])("rejects invalid deadline/reminder combination %#", (deadline, reminders, error) => {
    expect(validateScheduleSettings(deadline, reminders).error).toBe(error);
  });
});
