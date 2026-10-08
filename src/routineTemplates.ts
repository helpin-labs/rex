import { scheduleFields, type ScheduleDraft } from "./schedule";
import type { Routine } from "./types";

// Starting points for new routines. Each opens the editor filled in; the
// user picks the bot and adjusts the words before saving.

const at = (patch: Partial<ScheduleDraft>) => scheduleFields({ repeat: "daily", time: "08:00", days: [1], dayOfMonth: 1, everyMinutes: 60, cron: "", ...patch });

export type RoutineTemplate = { id: string; icon: "sunrise" | "binoculars" | "bookmarked" | "eye"; fields: Partial<Routine> & { title: string; prompt: string } };

export const routineTemplates: RoutineTemplate[] = [
  { id: "briefing", icon: "sunrise", fields: { title: "Morning briefing", prompt: "Give me a short morning briefing: the most important news in tech and business from the last 24 hours, in five bullet points with links.", ...at({ repeat: "weekdays", time: "08:00" }) } },
  { id: "competitors", icon: "binoculars", fields: { title: "Competitor watch", prompt: "Check what changed this week for the competitors I've told you about: launches, pricing changes and notable posts. Only the highlights, with links.", ...at({ repeat: "weekly", days: [1], time: "09:00" }) } },
  { id: "reading", icon: "bookmarked", fields: { title: "Monthly reading list", prompt: "Find five of the best long-form articles from the past month on the topics I care about, with a one-line summary and a link for each.", ...at({ repeat: "monthly", dayOfMonth: 1, time: "09:00" }) } },
  { id: "page-watch", icon: "eye", fields: { title: "Page watch", prompt: "Open the page I want watched (replace this with its link) and tell me only if something important changed since the last check.", ...at({ repeat: "every", everyMinutes: 120 }) } },
];
