import type { Routine } from "./types";

// The routine editor builds schedules from plain choices (daily, weekdays,
// chosen days, monthly, every N minutes) and stores them as cron
// expressions in local time. Anything else is edited as raw cron.

export type Repeat = "every" | "daily" | "weekdays" | "weekly" | "monthly" | "custom";
export type ScheduleDraft = { repeat: Repeat; time: string; days: number[]; dayOfMonth: number; everyMinutes: number; cron: string };

export const weekdayNames = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

const clock = (time: string) => { const [h, m] = time.split(":").map(Number); return { h: h || 0, m: m || 0 }; };
const pad = (value: number) => String(value).padStart(2, "0");

export function draftFromRoutine(routine: Partial<Routine>): ScheduleDraft {
  const draft: ScheduleDraft = { repeat: "daily", time: routine.daily_time || "08:00", days: [1], dayOfMonth: 1, everyMinutes: routine.interval_minutes || 60, cron: routine.cron || "" };
  if (routine.schedule_kind === "interval") return { ...draft, repeat: "every" };
  if (routine.schedule_kind !== "cron" || !routine.cron) return draft;
  const match = /^(\d{1,2}) (\d{1,2}) (\S+) \* (\S+)$/.exec(routine.cron.trim());
  if (!match) return { ...draft, repeat: "custom" };
  const [, minute, hour, dom, dow] = match;
  const time = `${pad(Number(hour))}:${pad(Number(minute))}`;
  if (dom === "*" && dow === "*") return { ...draft, repeat: "daily", time };
  if (dom === "*" && dow === "1-5") return { ...draft, repeat: "weekdays", time };
  if (dom === "*" && /^[0-6](,[0-6])*$/.test(dow)) return { ...draft, repeat: "weekly", time, days: dow.split(",").map(Number) };
  if (/^\d{1,2}$/.test(dom) && dow === "*" && Number(dom) <= 28) return { ...draft, repeat: "monthly", time, dayOfMonth: Number(dom) };
  return { ...draft, repeat: "custom" };
}

/** The routine fields a draft sets. */
export function scheduleFields(draft: ScheduleDraft): Pick<Routine, "schedule_kind" | "cron" | "interval_minutes"> {
  if (draft.repeat === "every") return { schedule_kind: "interval", cron: "", interval_minutes: draft.everyMinutes };
  const { h, m } = clock(draft.time);
  const cron = {
    daily: `${m} ${h} * * *`,
    weekdays: `${m} ${h} * * 1-5`,
    weekly: `${m} ${h} * * ${[...new Set(draft.days)].sort().join(",") || "1"}`,
    monthly: `${m} ${h} ${draft.dayOfMonth} * *`,
    custom: draft.cron.trim(),
  }[draft.repeat];
  return { schedule_kind: "cron", cron, interval_minutes: 0 };
}

function ordinal(value: number) {
  const suffix = value % 10 === 1 && value !== 11 ? "st" : value % 10 === 2 && value !== 12 ? "nd" : value % 10 === 3 && value !== 13 ? "rd" : "th";
  return `${value}${suffix}`;
}

function everyLabel(minutes: number) {
  if (minutes % 60 === 0) return minutes === 60 ? "Every hour" : `Every ${minutes / 60} hours`;
  return `Every ${minutes} minutes`;
}

/** A plain-language summary, e.g. "Every weekday at 08:00". */
export function describeDraft(draft: ScheduleDraft): string {
  switch (draft.repeat) {
    case "every": return everyLabel(draft.everyMinutes);
    case "daily": return `Every day at ${draft.time}`;
    case "weekdays": return `Every weekday at ${draft.time}`;
    case "weekly": {
      const days = [...new Set(draft.days)].sort().map(day => weekdayNames[day]);
      return `Every ${days.length ? days.join(", ") : "Mon"} at ${draft.time}`;
    }
    case "monthly": return `On the ${ordinal(draft.dayOfMonth)} of every month at ${draft.time}`;
    default: return draft.cron.trim() ? `Custom: ${draft.cron.trim()}` : "Custom schedule";
  }
}

/** The summary shown for a saved routine. */
export function describeRoutine(routine: Routine): string {
  if (routine.schedule_kind === "daily") return `Every day at ${routine.daily_time}`;
  return describeDraft(draftFromRoutine(routine));
}
