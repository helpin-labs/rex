// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Onboarding } from "./Onboarding";
import { api } from "./api";
import type { Bot, Settings } from "./types";

afterEach(() => { cleanup(); vi.restoreAllMocks(); });

const base = {
  local: { base_url: "http://127.0.0.1:1234/v1" }, anthropic: {}, openai: {}, openrouter: {},
  default_provider: "local", default_model: "", memory: { enabled: false, provider: "local", embedding_model: "" },
} as unknown as Settings;

it("signs in with a ChatGPT plan and makes it the bots' default", async () => {
  vi.spyOn(api, "models").mockImplementation(async provider => provider === "chatgpt" ? [{ id: "gpt-6.1-sol" }, { id: "gpt-6-luna" }] as never : []);
  const save = vi.spyOn(api, "saveSettings").mockImplementation(async settings => settings);
  vi.spyOn(api, "bots").mockResolvedValue([{ id: "rex", name: "Rex", model: "" } as Bot]);
  const saveBot = vi.spyOn(api, "saveBot").mockImplementation(async bot => bot as Bot);
  const done = vi.fn();
  render(<Onboarding initial={{ ...base, chatgpt: { connected: true } }} onDone={done} />);

  fireEvent.click(screen.getByRole("button", { name: "ChatGPT plan" }));
  expect(screen.getByText("ChatGPT subscription")).toBeTruthy();
  await waitFor(() => expect(screen.getByText("gpt-6.1-sol")).toBeTruthy());
  fireEvent.click(screen.getByRole("button", { name: /Start using Rex/ }));

  await waitFor(() => expect(done).toHaveBeenCalled());
  expect(save).toHaveBeenCalledWith(expect.objectContaining({ default_provider: "chatgpt", default_model: "gpt-6.1-sol", onboarded: true }));
  expect(saveBot).toHaveBeenCalledWith(expect.objectContaining({ id: "rex", provider: "chatgpt", model: "gpt-6.1-sol" }));
});

it("does not finish with ChatGPT before the user signs in", () => {
  vi.spyOn(api, "models").mockResolvedValue([]);
  render(<Onboarding initial={{ ...base, chatgpt: { connected: false } }} onDone={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "ChatGPT plan" }));
  expect(screen.getByRole("button", { name: "Sign in with ChatGPT" })).toBeTruthy();
  expect((screen.getByRole("button", { name: /Start using Rex/ }) as HTMLButtonElement).disabled).toBe(true);
});
