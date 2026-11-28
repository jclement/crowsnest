import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiError, api, endpoints, onUnauthorized } from "./api";

function mockFetch(status: number, body: unknown) {
  const fetchMock = vi.fn().mockResolvedValue(
    new Response(body === undefined ? null : JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    }),
  );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("api", () => {
  let unregister: (() => void) | undefined;
  afterEach(() => {
    unregister?.();
    vi.unstubAllGlobals();
  });

  it("unwraps the data envelope and sends the X-Crowsnest header", async () => {
    const fetchMock = mockFetch(200, { data: { status: "ok", version: "v1", update_available: false } });
    await expect(endpoints.health()).resolves.toEqual({ status: "ok", version: "v1", update_available: false });
    const [url, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(url).toBe("/api/v1/health");
    expect((init.headers as Record<string, string>)["X-Crowsnest"]).toBe("1");
    expect(init.credentials).toBe("same-origin");
  });

  it("a transcription without its text is a failure, not a TypeError (R10 Fable: Transcribing… forever)", async () => {
    mockFetch(200, { data: {} });
    await expect(endpoints.transcribe(new Blob(["x"], { type: "audio/webm" }))).rejects.toMatchObject({ code: "BAD_RESPONSE" });
    mockFetch(200, "<html>proxy error</html>");
    await expect(endpoints.transcribe(new Blob(["x"], { type: "audio/webm" }))).rejects.toBeInstanceOf(ApiError);
    mockFetch(200, { data: { text: "ship it" } });
    await expect(endpoints.transcribe(new Blob(["x"], { type: "audio/webm" }))).resolves.toEqual({ text: "ship it" });
  });

  it("posts JSON bodies", async () => {
    const fetchMock = mockFetch(200, { data: { username: "jeff", features: {} } });
    await endpoints.login("jeff", "pw");
    const [, init] = fetchMock.mock.calls[0] as [string, RequestInit];
    expect(init.method).toBe("POST");
    expect(init.body).toBe(JSON.stringify({ username: "jeff", password: "pw" }));
    expect((init.headers as Record<string, string>)["Content-Type"]).toBe("application/json");
  });

  it("calls the unauthorized handler on a 401 and throws an ApiError", async () => {
    const handler = vi.fn();
    unregister = onUnauthorized(handler);
    mockFetch(401, { error: { code: "UNAUTHORIZED", message: "Sign in first." } });
    const error = await endpoints.me().catch((caught: unknown) => caught);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 401, code: "UNAUTHORIZED", message: "Sign in first." });
    expect(handler).toHaveBeenCalledTimes(1);
  });

  it("does not redirect on a failed login (401 means wrong password there)", async () => {
    const handler = vi.fn();
    unregister = onUnauthorized(handler);
    mockFetch(401, { error: { code: "UNAUTHORIZED", message: "Wrong username or password." } });
    await expect(endpoints.login("jeff", "bad")).rejects.toMatchObject({ status: 401 });
    expect(handler).not.toHaveBeenCalled();
  });

  it("does not call the handler for other errors", async () => {
    const handler = vi.fn();
    unregister = onUnauthorized(handler);
    mockFetch(429, { error: { code: "RATE_LIMITED", message: "Try again in 60 seconds." } });
    await expect(api("/auth/login", { method: "POST", body: {} })).rejects.toMatchObject({
      status: 429,
      code: "RATE_LIMITED",
    });
    expect(handler).not.toHaveBeenCalled();
  });

  it("copes with a non-JSON error body", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response("oops", { status: 502, statusText: "Bad Gateway" })));
    await expect(endpoints.health()).rejects.toMatchObject({ status: 502, code: "HTTP_502" });
  });

  it("reports a network failure as an ApiError with status 0", async () => {
    vi.stubGlobal("fetch", vi.fn().mockRejectedValue(new TypeError("Failed to fetch")));
    await expect(endpoints.health()).rejects.toMatchObject({ status: 0, code: "NETWORK_ERROR" });
  });

  it("unregistering stops the handler", async () => {
    const handler = vi.fn();
    onUnauthorized(handler)();
    mockFetch(401, { error: { code: "UNAUTHORIZED", message: "x" } });
    await endpoints.me().catch(() => undefined);
    expect(handler).not.toHaveBeenCalled();
  });
});
