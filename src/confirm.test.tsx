// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { ConfirmHost, confirm } from "./confirm";

afterEach(cleanup);

it("asks in Rex's own dialog and resolves with the answer", async () => {
  render(<ConfirmHost />);
  let answer: Promise<boolean>;
  act(() => { answer = confirm("Bots set to ChatGPT stop working.", { title: "Sign out of ChatGPT?", confirmLabel: "Sign out", danger: true }); });
  expect(screen.getByRole("dialog", { name: "Sign out of ChatGPT?" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "Sign out" }));
  await expect(answer!).resolves.toBe(true);
  expect(screen.queryByRole("dialog")).toBeNull();

  act(() => { answer = confirm("Discard?"); });
  fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
  await expect(answer!).resolves.toBe(false);
});

it("Escape answers no without reaching the editor underneath", async () => {
  let editorHeard = 0;
  const editor = (event: KeyboardEvent) => { if (event.key === "Escape") editorHeard++; };
  window.addEventListener("keydown", editor);
  render(<ConfirmHost />);
  let answer: Promise<boolean>;
  act(() => { answer = confirm("Discard your changes?"); });
  fireEvent.keyDown(window, { key: "Escape" });
  await expect(answer!).resolves.toBe(false);
  expect(editorHeard).toBe(0);
  window.removeEventListener("keydown", editor);
});

it("a newer question answers the unanswered one with no", async () => {
  render(<ConfirmHost />);
  let first: Promise<boolean>, second: Promise<boolean>;
  act(() => { first = confirm("First?"); });
  act(() => { second = confirm("Second?", { confirmLabel: "Yes" }); });
  await expect(first!).resolves.toBe(false);
  fireEvent.click(screen.getByRole("button", { name: "Yes" }));
  await expect(second!).resolves.toBe(true);
});
