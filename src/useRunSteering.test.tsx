// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { act, renderHook, waitFor } from "@testing-library/react";
import { ApiError, api } from "./api";
import { steeringReceiptText, useRunSteering } from "./useRunSteering";
import type { RunSteeringCommand } from "./types";

const command = (revision: number, state: string, content = "x"): RunSteeringCommand =>
  ({ command_id: `c${revision}`, run_id: "run-1", revision, state, content, created_at: "2026-11-08T00:00:00Z" });

afterEach(() => vi.restoreAllMocks());

describe("useRunSteering", () => {
  it("sends at the latest revision and keeps the command ID when a send is retried", async () => {
    vi.spyOn(api, "runSteering").mockResolvedValue([command(2, "applied")]);
    const steer = vi.spyOn(api, "steerRun")
      .mockRejectedValueOnce(new Error("network"))
      .mockResolvedValueOnce(command(3, "accepted", "Use April"));
    const { result } = renderHook(() => useRunSteering("conv", "run-1", true));
    await waitFor(() => expect(result.current.canSteer).toBe(true));

    await act(async () => { await expect(result.current.send("Use April")).rejects.toThrow("network"); });
    await act(async () => { await result.current.send("Use April"); });

    expect(steer).toHaveBeenCalledTimes(2);
    const [first, second] = steer.mock.calls.map(call => call[2]);
    expect(first).toEqual({ command_id: second.command_id, expected_revision: 2, content: "Use April" });
    expect(result.current.receipt?.revision).toBe(3);
    expect(steeringReceiptText(result.current.receipt!)).toMatch(/next step/);
  });

  it("starts over with a fresh read after a revision conflict", async () => {
    const read = vi.spyOn(api, "runSteering").mockResolvedValue([]);
    const steer = vi.spyOn(api, "steerRun").mockRejectedValueOnce(new ApiError("conflict", 409)).mockResolvedValueOnce(command(2, "accepted"));
    const { result } = renderHook(() => useRunSteering("conv", "run-1", true));
    await waitFor(() => expect(result.current.canSteer).toBe(true));

    await act(async () => { await expect(result.current.send("Stop the search")).rejects.toThrow("conflict"); });
    read.mockResolvedValue([command(1, "applied")]);
    await act(async () => { await result.current.send("Stop the search"); });

    const [first, second] = steer.mock.calls.map(call => call[2]);
    expect(second.command_id).not.toBe(first.command_id);
    expect(second.expected_revision).toBe(1);
  });

  it("refuses to steer a run that is not working", async () => {
    vi.spyOn(api, "runSteering").mockResolvedValue([]);
    const { result } = renderHook(() => useRunSteering("conv", "run-1", false));
    expect(result.current.canSteer).toBe(false);
    await expect(result.current.send("hi")).rejects.toThrow(/finished/);
  });
});
