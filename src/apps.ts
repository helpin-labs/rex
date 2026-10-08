import { siAirtable, siAtlassian, siCloudflare, siClickup, siGithub, siHuggingface, siIntercom, siLinear, siNeon, siNetlify, siNotion, siPaypal, siPosthog,
  siSentry, siStripe, siSupabase, siVercel, siWebflow, siWix, siZapier } from "simple-icons";
import type { MCPConnection } from "./types";

// The Apps gallery: hosted MCP servers that Rex can connect in one step.
// Every "oauth" entry was checked to offer OAuth discovery and dynamic client
// registration, which Rex's sign-in needs; "bearer" ones take a token the
// user creates on the app's site; "none" ones need no account.

export type AppCategory = "Work" | "Engineering" | "Design" | "Customers" | "Payments" | "Research";
export const appCategories: AppCategory[] = ["Work", "Engineering", "Design", "Customers", "Payments", "Research"];

export type AppEntry = {
  id: string;
  // Also the connection's name, so it follows the server-name rules.
  name: string;
  description: string;
  category: AppCategory;
  url: string;
  auth: "oauth" | "bearer" | "none";
  // For bearer apps: where to create the token.
  tokenHint?: string;
  // A simple-icons path, or a letter on the brand color.
  icon?: { path: string; hex: string };
  color: string;
};

const icon = (item: { path: string; hex: string }) => ({ path: item.path, hex: item.hex });

export const appCatalog: AppEntry[] = [
  { id: "notion", name: "Notion", description: "Search, read and update pages and databases.", category: "Work", url: "https://mcp.notion.com/mcp", auth: "oauth", icon: icon(siNotion), color: "#000000" },
  { id: "linear", name: "Linear", description: "Find, create and update issues, projects and cycles.", category: "Engineering", url: "https://mcp.linear.app/mcp", auth: "oauth", icon: icon(siLinear), color: "#5E6AD2" },
  { id: "atlassian", name: "Atlassian", description: "Jira issues and Confluence pages.", category: "Work", url: "https://mcp.atlassian.com/v1/mcp", auth: "oauth", icon: icon(siAtlassian), color: "#0052CC" },
  { id: "airtable", name: "Airtable", description: "Read and write records in your bases.", category: "Work", url: "https://mcp.airtable.com/mcp", auth: "oauth", icon: icon(siAirtable), color: "#18BFFF" },
  { id: "clickup", name: "ClickUp", description: "Tasks, docs and spaces.", category: "Work", url: "https://mcp.clickup.com/mcp", auth: "oauth", icon: icon(siClickup), color: "#7B68EE" },
  { id: "monday", name: "monday.com", description: "Boards, items and updates.", category: "Work", url: "https://mcp.monday.com/mcp", auth: "oauth", color: "#FF3D57" },
  { id: "granola", name: "Granola", description: "Meeting notes and transcripts.", category: "Work", url: "https://mcp.granola.ai/mcp", auth: "oauth", color: "#2F6B4F" },
  { id: "zapier", name: "Zapier", description: "Run actions across thousands of apps.", category: "Work", url: "https://mcp.zapier.com/api/mcp/mcp", auth: "oauth", icon: icon(siZapier), color: "#FF4F00" },
  { id: "github", name: "GitHub", description: "Repositories, issues and pull requests.", category: "Engineering", url: "https://api.githubcopilot.com/mcp/", auth: "bearer", tokenHint: "A personal access token from GitHub → Settings → Developer settings.", icon: icon(siGithub), color: "#181717" },
  { id: "sentry", name: "Sentry", description: "Look into errors, issues and releases.", category: "Engineering", url: "https://mcp.sentry.dev/mcp", auth: "oauth", icon: icon(siSentry), color: "#362D59" },
  { id: "vercel", name: "Vercel", description: "Projects, deployments and logs.", category: "Engineering", url: "https://mcp.vercel.com", auth: "oauth", icon: icon(siVercel), color: "#000000" },
  { id: "netlify", name: "Netlify", description: "Sites, deploys and settings.", category: "Engineering", url: "https://mcp.netlify.com/mcp", auth: "oauth", icon: icon(siNetlify), color: "#00C7B7" },
  { id: "supabase", name: "Supabase", description: "Databases, tables and SQL.", category: "Engineering", url: "https://mcp.supabase.com/mcp", auth: "oauth", icon: icon(siSupabase), color: "#3FCF8E" },
  { id: "neon", name: "Neon", description: "Serverless Postgres projects and queries.", category: "Engineering", url: "https://mcp.neon.tech/mcp", auth: "oauth", icon: icon(siNeon), color: "#34D59A" },
  { id: "posthog", name: "PostHog", description: "Product analytics, insights and feature flags.", category: "Engineering", url: "https://mcp.posthog.com/mcp", auth: "bearer", tokenHint: "A personal API key from PostHog → Settings → Personal API keys.", icon: icon(siPosthog), color: "#F54E00" },
  { id: "canva", name: "Canva", description: "Find and create designs.", category: "Design", url: "https://mcp.canva.com/mcp", auth: "oauth", color: "#00C4CC" },
  { id: "webflow", name: "Webflow", description: "Sites, pages and CMS collections.", category: "Design", url: "https://mcp.webflow.com/mcp", auth: "oauth", icon: icon(siWebflow), color: "#146EF5" },
  { id: "wix", name: "Wix", description: "Sites, content and business tools.", category: "Design", url: "https://mcp.wix.com/mcp", auth: "oauth", icon: icon(siWix), color: "#0C6EFC" },
  { id: "intercom", name: "Intercom", description: "Conversations, contacts and help articles.", category: "Customers", url: "https://mcp.intercom.com/mcp", auth: "oauth", icon: icon(siIntercom), color: "#1F8DED" },
  { id: "attio", name: "Attio", description: "CRM records, lists and notes.", category: "Customers", url: "https://mcp.attio.com/mcp", auth: "oauth", color: "#1A1D21" },
  { id: "close", name: "Close", description: "Leads, opportunities and activities.", category: "Customers", url: "https://mcp.close.com/mcp", auth: "oauth", color: "#2D8CFF" },
  { id: "stripe", name: "Stripe", description: "Customers, payments and subscriptions.", category: "Payments", url: "https://mcp.stripe.com", auth: "oauth", icon: icon(siStripe), color: "#635BFF" },
  { id: "paypal", name: "PayPal", description: "Invoices, orders and transactions.", category: "Payments", url: "https://mcp.paypal.com/mcp", auth: "oauth", icon: icon(siPaypal), color: "#002991" },
  { id: "huggingface", name: "Hugging Face", description: "Search models, datasets and papers.", category: "Research", url: "https://huggingface.co/mcp", auth: "none", icon: icon(siHuggingface), color: "#FFD21E" },
  { id: "cloudflare-docs", name: "Cloudflare Docs", description: "Search Cloudflare's documentation.", category: "Research", url: "https://docs.mcp.cloudflare.com/mcp", auth: "none", icon: icon(siCloudflare), color: "#F38020" },
];

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
  if (connection.auth === "oauth" && !connection.signed_in) return "signin";
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
