import { describe, expect, it } from "vitest";
import { describeDraft, draftFromRoutine, scheduleFields } from "./schedule";

describe("schedule builder", () => {
  it("turns plain choices into cron and back", () => {
    const weekly = { repeat: "weekly" as const, time: "07:30", days: [5, 1, 3], dayOfMonth: 1, everyMinutes: 60, cron: "" };
    const fields = scheduleFields(weekly);
    expect(fields).toEqual({ schedule_kind: "cron", cron: "30 7 * * 1,3,5", interval_minutes: 0 });
    expect(describeDraft(weekly)).toBe("Every Mon, Wed, Fri at 07:30");
    expect(draftFromRoutine(fields)).toMatchObject({ repeat: "weekly", time: "07:30", days: [1, 3, 5] });
  });
  it("recognises weekdays, monthly and custom expressions", () => {
    expect(draftFromRoutine({ schedule_kind: "cron", cron: "0 8 * * 1-5" })).toMatchObject({ repeat: "weekdays", time: "08:00" });
    expect(describeDraft(draftFromRoutine({ schedule_kind: "cron", cron: "15 9 1 * *" }))).toBe("On the 1st of every month at 09:15");
    expect(draftFromRoutine({ schedule_kind: "cron", cron: "0 9-17 * * 1-5" })).toMatchObject({ repeat: "custom" });
  });
  it("keeps repeating intervals and old daily routines", () => {
    expect(describeDraft(draftFromRoutine({ schedule_kind: "interval", interval_minutes: 120 }))).toBe("Every 2 hours");
    expect(draftFromRoutine({ schedule_kind: "daily", daily_time: "06:45" })).toMatchObject({ repeat: "daily", time: "06:45" });
  });
});
