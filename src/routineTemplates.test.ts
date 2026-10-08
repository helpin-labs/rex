import { describe, expect, it } from "vitest";
import { routineTemplates } from "./routineTemplates";
import { describeRoutine } from "./schedule";
import type { Routine } from "./types";

describe("routine templates", () => {
  it("describe their schedules in plain words", () => {
    expect(routineTemplates.map(template => describeRoutine(template.fields as Routine))).toEqual([
      "Every weekday at 08:00", "Every Mon at 09:00", "On the 1st of every month at 09:00", "Every 2 hours",
    ]);
  });
  it("have unique ids, a title and instructions", () => {
    expect(new Set(routineTemplates.map(template => template.id)).size).toBe(routineTemplates.length);
    for (const template of routineTemplates) expect(template.fields.title && template.fields.prompt).toBeTruthy();
  });
});
