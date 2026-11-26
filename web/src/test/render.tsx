// Test helpers: render inside a fresh QueryClient (no retries, so failures show at once) and the
// toast provider, and a fetch stub that routes by method and path.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render } from "@testing-library/react";
import type { ReactElement } from "react";
import { vi } from "vitest";
import { ToastProvider } from "../components/ui/Toast";

export function renderWithClient(ui: ReactElement) {
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const result = render(
    <QueryClientProvider client={queryClient}>
      <ToastProvider>{ui}</ToastProvider>
    </QueryClientProvider>,
  );
  return { ...result, queryClient };
}

export interface Call {
  method: string;
  path: string;
  body: unknown;
}

type Route = (call: Call) => { status?: number; body: unknown } | undefined;

/**
 * Stubs fetch with a router: each call goes to `route`, which returns a status and JSON body.
 * Unrouted calls fail the test loudly with a 599. Returns the list of calls made.
 */
export function stubApi(route: Route): Call[] {
  const calls: Call[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      const call: Call = {
        method: init?.method ?? "GET",
        path: url.replace(/^\/api\/v1/, ""),
        body: init?.body ? JSON.parse(String(init.body)) : undefined,
      };
      calls.push(call);
      const answer = route(call) ?? { status: 599, body: { error: { code: "UNROUTED", message: `${call.method} ${call.path}` } } };
      return new Response(JSON.stringify(answer.body), {
        status: answer.status ?? 200,
        headers: { "Content-Type": "application/json" },
      });
    }),
  );
  return calls;
}
