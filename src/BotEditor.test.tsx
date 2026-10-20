// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { BotEditor, newBot } from "./BotEditor";
import { api } from "./api";
import type { Bot, Status } from "./types";

vi.mock("./api", () => ({ api: { skills: vi.fn(), connections: vi.fn(), bots: vi.fn(), models: vi.fn(), saveBot: vi.fn() } }));
afterEach(() => { cleanup(); vi.clearAllMocks(); });
beforeEach(() => {
  vi.mocked(api.skills).mockResolvedValue([]);
  vi.mocked(api.connections).mockResolvedValue([]);
  vi.mocked(api.bots).mockResolvedValue([]);
  vi.mocked(api.models).mockResolvedValue([]);
  vi.mocked(api.saveBot).mockImplementation(async bot => bot);
});

it.each(["ask", "auto", "allow_all"] as const)("preserves %s when shell is enabled and saves it", async mode => {
  const bot: Bot = { ...newBot(null), id: "ops", name: "Ops", approval_mode: mode };
  render(<BotEditor initial={bot} settings={null} status={{write_tools: true} as Status} onClose={vi.fn()} onSaved={vi.fn()} onDeleted={vi.fn()} />);
  fireEvent.click(screen.getByRole("tab", {name: "Abilities"}));
  fireEvent.click(screen.getByRole("switch", {name: /Run shell commands/}));
  const label = {ask: "Ask", auto: "Auto (Jev)", allow_all: "Allow all"}[mode];
  expect(screen.getByRole("radio", {name: new RegExp(`^${label.replace(/[()]/g, "\\$&")}`)}).getAttribute("aria-checked")).toBe("true");
  fireEvent.click(screen.getByRole("button", {name: "Save changes"}));
  await waitFor(() => expect(api.saveBot).toHaveBeenCalledWith(expect.objectContaining({approval_mode: mode, capabilities: expect.objectContaining({shell:true,files_read:true})})));
});

it("allows Auto without a key, explains fallback, and allows switching to Allow all with shell", async () => {
  const bot: Bot = {...newBot(null), id:"ops", name:"Ops", capabilities:{...newBot(null).capabilities,shell:true,files_read:true}};
  render(<BotEditor initial={bot} settings={null} status={{write_tools:true} as Status} onClose={vi.fn()} onSaved={vi.fn()} onDeleted={vi.fn()} />);
  fireEvent.click(screen.getByRole("tab", {name:"Abilities"}));
  fireEvent.click(screen.getByRole("radio", {name:/^Auto/}));
  expect(screen.getByText(/Until then, protected actions ask/)).toBeTruthy();
  fireEvent.click(screen.getByRole("radio", {name:/^Allow all/}));
  fireEvent.click(screen.getByRole("button", {name:"Save changes"}));
  await waitFor(() => expect(api.saveBot).toHaveBeenCalledWith(expect.objectContaining({approval_mode:"allow_all"})));
});


it("offers and persists the expanded blob set while retaining a saved face", async () => {
  const bot: Bot = {...newBot(null), id:"ops", name:"Ops", emoji:"chef-hat", color:"#62b89c"};
  render(<BotEditor initial={bot} settings={null} status={null} onClose={vi.fn()} onSaved={vi.fn()} onDeleted={vi.fn()} />);
  expect(screen.getByRole("button", {name:"Food"}).getAttribute("aria-pressed")).toBe("true");
  expect(document.querySelectorAll(".face-choice")).toHaveLength(20);
  fireEvent.click(screen.getByRole("button", {name:"Security"}));
  expect(screen.getByRole("button", {name:"Security"}).getAttribute("aria-pressed")).toBe("true");
  fireEvent.click(screen.getByRole("button", {name:"Save changes"}));
  await waitFor(() => expect(api.saveBot).toHaveBeenCalledWith(expect.objectContaining({emoji:"security",color:"#62b89c"})));
});
