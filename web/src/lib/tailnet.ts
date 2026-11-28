// Publish to your tailnet (DESIGN.md 3.5.1): mirrors internal/tsserve's View and the
// /tailscale/serve routes (internal/httpd/tsserve.go).
import { api } from "./api";

export type PublishState = "unavailable" | "off" | "refused" | "starting" | "publishing" | "failed" | "stopped" | "external";

export interface PublishView {
  show: boolean;
  state: PublishState;
  reason?: string;
  fix?: string;
  fix_url?: string;
  url?: string;
  port?: number;
  port_note?: string;
  target?: string;
  name?: string;
  version?: string;
  binary?: string;
  enabled: boolean;
  since?: string;
  retry_at?: string;
  note?: string;
  locked?: string;
  qr?: string;
  /** Publishing together with Sign in with Tailscale (R9-S3): the risk, in words. */
  local_risk?: string;
}

export const tailnetEndpoints = {
  view: () => api<PublishView>("/tailscale/serve"),
  publish: () => api<PublishView>("/tailscale/serve", { method: "POST", body: {} }),
  stop: () => api<PublishView>("/tailscale/serve", { method: "DELETE" }),
};

export const tailnetKeys = { view: ["tailscale-serve"] as const };

/** How often the card asks again: quickly while it settles, slowly once it has. */
export function publishPoll(state: PublishState | undefined): number {
  switch (state) {
    case "starting":
      return 1_000;
    case "publishing":
    case "failed":
      return 5_000;
    default:
      return 15_000;
  }
}

/** Only Tailscale's own admin pages are ever linked from the card. */
export function safeTailscaleLink(url: string | undefined): string | undefined {
  return url && url.startsWith("https://login.tailscale.com/") ? url : undefined;
}
