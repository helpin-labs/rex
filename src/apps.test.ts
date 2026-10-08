import { describe, expect, it } from "vitest";
import { appCatalog, appFor, connectionState, initials, sameServer, searchApps } from "./apps";
import type { MCPConnection } from "./types";

const connection = (patch: Partial<MCPConnection>): MCPConnection =>
  ({ id: "c1", name: "Linear", url: "https://mcp.linear.app/mcp", auth: "oauth", has_token: false, signed_in: true, tools: [], status: "Connected · 3 tools", ...patch });

describe("apps catalog", () => {
  it("has unique ids and valid connection names", () => {
    expect(new Set(appCatalog.map(app => app.id)).size).toBe(appCatalog.length);
    // The host's server-name rule.
    for (const app of appCatalog) expect(app.name).toMatch(/^[A-Za-z0-9 _.-]{1,40}$/);
  });
  it("uses https (or names a Google app) and gives token apps a hint", () => {
    for (const app of appCatalog) {
      if (app.auth === "google") { expect(app.url).toMatch(/^google:[a-z]+$/); expect(app.access).toBeTruthy(); continue; }
      expect(app.url.startsWith("https://")).toBe(true);
      if (app.auth === "bearer") expect(app.tokenHint).toBeTruthy();
    }
  });
  it("matches connections by URL, ignoring a trailing slash and case", () => {
    expect(sameServer("https://API.githubcopilot.com/mcp/", "https://api.githubcopilot.com/mcp")).toBe(true);
    expect(appFor({ url: "https://mcp.linear.app/mcp/" })?.id).toBe("linear");
    expect(appFor({ url: "https://example.com/mcp" })).toBeUndefined();
  });
  it("searches names, descriptions and categories", () => {
    expect(searchApps("lin").map(app => app.id)).toContain("linear");
    expect(searchApps("", "Payments").map(app => app.id)).toEqual(["stripe", "paypal"]);
    expect(searchApps("issues pull")).toEqual([appCatalog.find(app => app.id === "github")]);
    expect(searchApps("zzzz")).toEqual([]);
  });
});

describe("connection state", () => {
  it("is ready when connected", () => expect(connectionState(connection({}))).toBe("ready"));
  it("needs sign-in when an OAuth app is signed out", () => expect(connectionState(connection({ signed_in: false, status: "Sign in to connect" }))).toBe("signin"));
  it("is broken when the server can't be reached", () => expect(connectionState(connection({ auth: "none", status: "Could not reach the server: 404" }))).toBe("broken"));
});

describe("initials", () => {
  it("uses first and last names", () => expect(initials("Azhar Khan")).toBe("AK"));
  it("uses two letters of a single name", () => expect(initials("dev")).toBe("DE"));
  it("falls back for an empty name", () => expect(initials("  ")).toBe("?"));
});
