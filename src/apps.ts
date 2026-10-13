import catalog from "../internal/rex/app_catalog.json";
import { siAirtable, siAtlassian, siCloudflare, siClickup, siGithub, siGmail, siGooglecalendar, siGoogledrive, siGooglesheets, siHuggingface, siIntercom, siLinear, siNeon, siNetlify, siNotion, siPaypal, siPosthog,
  siSentry, siStripe, siSupabase, siVercel, siWebflow, siWix, siZapier } from "simple-icons";
import type { MCPConnection } from "./types";

// The Apps gallery: hosted MCP servers that Rex can connect in one step.
// Every "oauth" entry was checked to offer OAuth discovery and dynamic client
// registration, which Rex's sign-in needs; "bearer" ones take a token the
// user creates on the app's site; "none" ones need no account. "google"
// ones are served by Rex itself, which signs in to Google for them.

export type AppCategory = "Work" | "Engineering" | "Design" | "Customers" | "Payments" | "Research";
export const appCategories: AppCategory[] = ["Work", "Engineering", "Design", "Customers", "Payments", "Research"];

export type AppEntry = {
  id: string;
  // Also the connection's name, so it follows the server-name rules.
  name: string;
  description: string;
  category: AppCategory;
  url: string;
  auth: "oauth" | "bearer" | "none" | "google";
  // For bearer apps: where to create the token.
  tokenHint?: string;
  // For Google apps: what Rex may do in the account, in plain words.
  access?: string;
  // A simple-icons path, or a letter on the brand color.
  icon?: { path: string; hex: string };
  color: string;
};

const icon = (item: { path: string; hex: string }) => ({ path: item.path, hex: item.hex });

const catalogIcons: Record<string, { path: string; hex: string }> = { "gmail": icon(siGmail), "google-sheets": icon(siGooglesheets), "google-calendar": icon(siGooglecalendar), "google-drive": icon(siGoogledrive), "notion": icon(siNotion), "linear": icon(siLinear), "atlassian": icon(siAtlassian), "airtable": icon(siAirtable), "clickup": icon(siClickup), "zapier": icon(siZapier), "github": icon(siGithub), "sentry": icon(siSentry), "vercel": icon(siVercel), "netlify": icon(siNetlify), "supabase": icon(siSupabase), "neon": icon(siNeon), "posthog": icon(siPosthog), "webflow": icon(siWebflow), "wix": icon(siWix), "intercom": icon(siIntercom), "stripe": icon(siStripe), "paypal": icon(siPaypal), "huggingface": icon(siHuggingface), "cloudflare-docs": icon(siCloudflare) };
export const appCatalog: AppEntry[] = (catalog as Omit<AppEntry, "icon">[]).map(app => ({ ...app, icon: catalogIcons[app.id] }));

// Connections match a gallery app by URL, ignoring a trailing slash and case.
export function sameServer(a: string, b: string): boolean {
  const normal = (value: string) => value.trim().toLowerCase().replace(/\/+$/, "");
  return normal(a) === normal(b);
}

export function appFor(connection: Pick<MCPConnection, "url">): AppEntry | undefined {
  return appCatalog.find(app => sameServer(app.url, connection.url));
}

export function connectionFor(app: AppEntry, connections: MCPConnection[]): MCPConnection | undefined {
  return connections.find(connection => sameServer(connection.url, app.url));
}

// ready: tools can be used. signin: added but needs (another) sign-in.
// broken: the server couldn't be reached or the token was refused.
export type AppState = "ready" | "signin" | "broken";

export function connectionState(connection: MCPConnection): AppState {
  if ((connection.auth === "oauth" || connection.auth === "google") && !connection.signed_in) return "signin";
  return connection.status.startsWith("Connected") ? "ready" : "broken";
}

export function searchApps(query: string, category?: AppCategory): AppEntry[] {
  const words = query.trim().toLowerCase().split(/\s+/).filter(Boolean);
  return appCatalog.filter(app => (!category || app.category === category)
    && words.every(word => `${app.name} ${app.description} ${app.category}`.toLowerCase().includes(word)));
}

export function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  if (!parts.length) return "?";
  return (parts.length === 1 ? parts[0].slice(0, 2) : parts[0][0] + parts[parts.length - 1][0]).toUpperCase();
}
