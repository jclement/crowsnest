// The typed API client. Every call goes through api(): it adds the X-Crowsnest header the server
// requires on state-changing requests (DESIGN.md 4.4), unwraps the {"data": ...} envelope, turns
// {"error": ...} into an ApiError, and on a 401 calls the registered handler so the app can send
// the user to the login page. Later milestones add endpoints here, not new fetch calls.
//
// Fixtures mode (development builds only): open the app with `?fixtures=1` and every call is
// answered in memory from lib/fixtures.ts, so the UI can be worked on, and screenshotted,
// without a server. `?fixtures=0` turns it off. The choice is kept for the tab's lifetime
// (sessionStorage) because client-side navigation drops the query string.
import type { AgentScreen, Fleet, KeysResponse, SendKeysRequest } from "./types";

/** An error response from the API, carrying the envelope's code and user-facing message. */
export class ApiError extends Error {
  readonly status: number;
  readonly code: string;

  constructor(status: number, code: string, message: string) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
  }
}

export interface RequestOptions {
  method?: "GET" | "POST" | "PUT" | "PATCH" | "DELETE";
  body?: unknown;
  signal?: AbortSignal;
  /** Do not call the 401 handler: for the login call, where 401 means "wrong password". */
  skipAuthRedirect?: boolean;
}

const API_BASE = "/api/v1";

let unauthorizedHandler: (() => void) | null = null;

/** Registers what happens when any call comes back 401. Returns an unregister function. */
export function onUnauthorized(handler: () => void): () => void {
  unauthorizedHandler = handler;
  return () => {
    if (unauthorizedHandler === handler) unauthorizedHandler = null;
  };
}

const FIXTURES_KEY = "crowsnest-fixtures";

/** True when this page answers API calls from fixtures. Always false in a production build. */
export function fixturesEnabled(): boolean {
  if (!import.meta.env.DEV || typeof window === "undefined") return false;
  try {
    const flag = new URLSearchParams(window.location.search).get("fixtures");
    if (flag === "1") sessionStorage.setItem(FIXTURES_KEY, "1");
    if (flag === "0") sessionStorage.removeItem(FIXTURES_KEY);
    return sessionStorage.getItem(FIXTURES_KEY) === "1";
  } catch {
    return false;
  }
}

/** Calls the API and returns the envelope's data. Throws ApiError on any non-2xx response. */
export async function api<T>(path: string, options: RequestOptions = {}): Promise<T> {
  if (import.meta.env.DEV && fixturesEnabled()) {
    const { fixtureApi } = await import("./fixtures");
    return (await fixtureApi(path, options.method ?? "GET", options.body)) as T;
  }
  const headers: Record<string, string> = { Accept: "application/json", "X-Crowsnest": "1" };
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  let response: Response;
  try {
    response = await fetch(API_BASE + path, {
      method: options.method ?? "GET",
      headers,
      credentials: "same-origin",
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
      signal: options.signal,
    });
  } catch (error) {
    if (error instanceof DOMException && error.name === "AbortError") throw error;
    throw new ApiError(0, "NETWORK_ERROR", "Cannot reach crowsnest. Check the connection.");
  }
  const payload = await readJSON(response);
  if (!response.ok) {
    if (response.status === 401 && !options.skipAuthRedirect) unauthorizedHandler?.();
    const error = (payload as { error?: { code?: string; message?: string } } | null)?.error;
    throw new ApiError(response.status, error?.code ?? "HTTP_" + response.status, error?.message ?? response.statusText);
  }
  return (payload as { data: T }).data;
}

async function readJSON(response: Response): Promise<unknown> {
  try {
    return await response.json();
  } catch {
    return null;
  }
}

// ---- Endpoints ---------------------------------------------------------------

export interface Health {
  status: string;
  version: string;
  update_available: boolean;
}

export interface Features {
  auth: boolean;
  voice: boolean;
  demo: boolean;
  session: string;
}

export interface Me {
  username: string;
  features: Features;
}

export const endpoints = {
  health: () => api<Health>("/health"),
  me: () => api<Me>("/auth/me"),
  login: (username: string, password: string) =>
    api<Me>("/auth/login", { method: "POST", body: { username, password }, skipAuthRedirect: true }),
  logout: () => api<{ signed_out: boolean }>("/auth/logout", { method: "POST" }),
  fleet: () => api<Fleet>("/fleet"),
  agentScreen: (target: string) => api<AgentScreen>(`/agents/${encodeURIComponent(target)}/screen`),
  /** Sends keys to an agent, bound to the screen they answer: 409 CONFLICT if it changed. */
  sendKeys: (target: string, request: SendKeysRequest) =>
    api<KeysResponse>(`/agents/${encodeURIComponent(target)}/keys`, { method: "POST", body: request }),
};

/**
 * TanStack Query keys, in one place so invalidation never misses a spelling. Everything about
 * one agent sits under ["agents", target] so a single invalidation refreshes all of it.
 */
export const queryKeys = {
  health: ["health"] as const,
  me: ["me"] as const,
  fleet: ["fleet"] as const,
  agents: ["agents"] as const,
  agent: (target: string) => ["agents", target] as const,
  agentScreen: (target: string) => ["agents", target, "screen"] as const,
  jobs: ["jobs"] as const,
  questions: ["questions"] as const,
};
