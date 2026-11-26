// The Notifications sheet (DESIGN.md 8.5), opened from the account menu: turn Web Push on for this
// device (or read exactly why it cannot be), send a test, choose which kinds are pushed, and see
// and remove the devices that get them.
//
// When this device cannot get notifications the sheet says what is missing (not HTTPS, iOS
// without the Home Screen app, an unsupported browser, notifications blocked) rather than showing
// a button that does nothing. The browser parts come in as props so the tests can play every
// device.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Bell, BellOff, Send, Smartphone, Trash2 } from "lucide-react";
import { useState } from "react";
import { endpoints } from "../../lib/api";
import { cn } from "../../lib/cn";
import { relativeTime } from "../../lib/format";
import {
  browserPushEnvironment,
  currentEndpoint,
  describeAgent,
  disablePush,
  enablePush,
  endpointHash,
  kindLabels,
  pushSupport,
  type PushDevice,
  type PushEnvironment,
  type PushKind,
  type PushSettings,
} from "../../lib/push";
import { Banner } from "../ui/Banner";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

const settingsKey = ["push", "settings"] as const;
const deviceKey = ["push", "this-device"] as const;

/** Why this device cannot get notifications, in words, with what to do. */
const supportMessages: Record<Exclude<ReturnType<typeof pushSupport>, "available">, { title: string; detail: string }> = {
  insecure: {
    title: "Not a secure connection",
    detail:
      "Browsers offer notifications only over HTTPS or on localhost. On a tailnet, serve crowsnest with `tailscale serve` (or start it with --tls-cert and --tls-key) and open the https:// address.",
  },
  "ios-install": {
    title: "Add crowsnest to your Home Screen first",
    detail: "On iPhone and iPad, notifications work only for an app on the Home Screen (iOS 16.4 or later). In Safari tap Share, then Add to Home Screen, and open crowsnest from there.",
  },
  unsupported: {
    title: "This browser cannot receive push notifications",
    detail: "It has no service worker or Push API. A current Chrome, Edge, Firefox or Safari can.",
  },
  denied: {
    title: "Notifications are blocked for this site",
    detail: "Allow notifications for this address in the browser's site settings, then reopen this sheet.",
  },
};

interface NotificationSettingsProps {
  open: boolean;
  onClose: () => void;
  /** The browser; tests pass a fake. */
  env?: PushEnvironment;
}

export function NotificationSettings({ open, onClose, env }: NotificationSettingsProps) {
  return (
    <Sheet open={open} onClose={onClose} title="Notifications" size="md">
      {open && <NotificationSettingsBody env={env ?? browserPushEnvironment()} />}
    </Sheet>
  );
}

function NotificationSettingsBody({ env }: { env: PushEnvironment }) {
  const queryClient = useQueryClient();
  const settings = useQuery({ queryKey: settingsKey, queryFn: endpoints.pushSettings });
  // This browser's own subscription, if any, and its hash to find it in the device list.
  const thisDevice = useQuery({
    queryKey: deviceKey,
    queryFn: async () => {
      const endpoint = await currentEndpoint(env).catch(() => undefined);
      return { endpoint, hash: endpoint ? await endpointHash(endpoint) : undefined };
    },
  });
  const refreshEndpoint = () => queryClient.invalidateQueries({ queryKey: deviceKey });
  const { endpoint, hash } = thisDevice.data ?? {};
  const support = pushSupport(env);

  const subscribedHere = !!hash && !!settings.data?.devices.some((device) => device.endpoint_hash === hash);

  return (
    <div className="flex flex-col gap-5 overflow-y-auto p-4 text-sm">
      <section aria-labelledby="push-this-device" className="flex flex-col gap-2">
        <h3 id="push-this-device" className="text-xs font-semibold uppercase tracking-wide text-muted">
          This device
        </h3>
        {support === "available" ? (
          <ThisDevice env={env} subscribed={subscribedHere} endpoint={endpoint} onChanged={refreshEndpoint} />
        ) : (
          <Banner tone={support === "denied" ? "danger" : "warning"} role="status" className="items-start rounded-md py-2">
            <span className="flex flex-col gap-0.5">
              <strong className="font-semibold">{supportMessages[support].title}</strong>
              <span>{supportMessages[support].detail}</span>
            </span>
          </Banner>
        )}
      </section>
      {settings.isError && (
        <Banner tone="danger" role="alert" className="rounded-md">
          Could not load the notification settings: {settings.error.message}
        </Banner>
      )}
      {settings.data && <Kinds settings={settings.data} />}
      {settings.data && <Devices settings={settings.data} currentHash={hash} onRemoved={refreshEndpoint} />}
    </div>
  );
}

function ThisDevice({ env, subscribed, endpoint, onChanged }: { env: PushEnvironment; subscribed: boolean; endpoint?: string; onChanged: () => Promise<void> }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const [busy, setBusy] = useState(false);

  const turnOn = () => {
    // No await before enablePush: its permission prompt must stay inside this click.
    setBusy(true);
    enablePush(endpoints, env)
      .then(async (outcome) => {
        if (outcome.ok) toast.show({ tone: "success", title: "Notifications are on for this device" });
        else if (outcome.reason === "denied") toast.show({ tone: "danger", title: "Notifications were blocked", description: supportMessages.denied.detail });
        else if (outcome.reason === "dismissed") toast.show({ tone: "warning", title: "Notifications stay off", description: "The permission prompt was dismissed." });
        else toast.show({ tone: "danger", title: supportMessages.unsupported.title });
      })
      .catch((error: unknown) => toast.show({ tone: "danger", title: "Could not turn notifications on", description: error instanceof Error ? error.message : String(error) }))
      .finally(async () => {
        await onChanged();
        await queryClient.invalidateQueries({ queryKey: settingsKey });
        setBusy(false);
      });
  };
  const turnOff = () => {
    setBusy(true);
    disablePush(endpoints, env)
      .then(() => toast.show({ tone: "info", title: "Notifications are off for this device" }))
      .catch((error: unknown) => toast.show({ tone: "danger", title: "Could not turn notifications off", description: error instanceof Error ? error.message : String(error) }))
      .finally(async () => {
        await onChanged();
        await queryClient.invalidateQueries({ queryKey: settingsKey });
        setBusy(false);
      });
  };
  const test = useMutation({
    mutationFn: () => endpoints.pushTest(endpoint),
    onSuccess: (result) =>
      toast.show(
        result.sent > 0
          ? { tone: "success", title: "Test sent", description: "It should arrive in a few seconds." }
          : { tone: "warning", title: "The test was not delivered", description: result.pruned > 0 ? "The push service says this subscription is gone; turn notifications on again." : "The push service refused it; try again later." },
      ),
    onError: (error) => toast.show({ tone: "danger", title: "Could not send a test", description: error.message }),
  });

  if (!subscribed) {
    return (
      <div className="flex flex-col gap-2">
        <p className="text-muted">Get a notification when an agent needs you, even with no crowsnest tab open.</p>
        <Button variant="primary" icon={Bell} onClick={turnOn} disabled={busy} className="self-start">
          {busy ? "Turning on…" : "Turn on for this device"}
        </Button>
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-2">
      <p className="flex items-center gap-2 text-heading">
        <Chip tone="success">On</Chip> This device gets notifications.
      </p>
      <div className="flex flex-wrap gap-2">
        <Button icon={Send} onClick={() => test.mutate()} disabled={test.isPending}>
          {test.isPending ? "Sending…" : "Send a test"}
        </Button>
        <Button variant="ghost" icon={BellOff} onClick={turnOff} disabled={busy}>
          Turn off for this device
        </Button>
      </div>
    </div>
  );
}

function Kinds({ settings }: { settings: PushSettings }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const update = useMutation({
    mutationFn: (change: Partial<Record<PushKind, boolean>>) => endpoints.updatePushSettings(change),
    onSuccess: (next) => queryClient.setQueryData(settingsKey, next),
    onError: (error) => toast.show({ tone: "danger", title: "Could not save", description: error.message }),
  });
  return (
    <section aria-labelledby="push-kinds" className="flex flex-col gap-2">
      <h3 id="push-kinds" className="text-xs font-semibold uppercase tracking-wide text-muted">
        What to send
      </h3>
      <p className="text-xs text-muted">For every device. Low-urgency questions are never pushed.</p>
      <ul className="flex flex-col gap-1.5">
        {settings.order.map((kind) => {
          const on = settings.kinds[kind];
          return (
            <li key={kind}>
              <button
                type="button"
                role="switch"
                aria-checked={on}
                onClick={() => update.mutate({ [kind]: !on })}
                disabled={update.isPending}
                className={cn(
                  "flex min-h-11 w-full items-start gap-3 rounded-md border px-3 py-2 text-left transition-colors",
                  on ? "border-accent bg-selected" : "border-border-strong bg-surface hover:bg-hover",
                )}
              >
                <span aria-hidden className={cn("mt-0.5 flex h-5 w-9 shrink-0 items-center rounded-full p-0.5 transition-colors", on ? "bg-accent" : "bg-border-strong")}>
                  <span className={cn("h-4 w-4 rounded-full bg-surface shadow transition-transform", on && "translate-x-4")} />
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block font-medium text-heading">{kindLabels[kind].label}</span>
                  <span className="block text-xs text-muted">{kindLabels[kind].detail}</span>
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function Devices({ settings, currentHash, onRemoved }: { settings: PushSettings; currentHash?: string; onRemoved: () => Promise<void> }) {
  const queryClient = useQueryClient();
  const toast = useToast();
  const remove = useMutation({
    mutationFn: (device: PushDevice) => endpoints.pushRemoveDevice(device.id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: settingsKey });
      await onRemoved();
    },
    onError: (error) => toast.show({ tone: "danger", title: "Could not remove the device", description: error.message }),
  });
  return (
    <section aria-labelledby="push-devices" className="flex flex-col gap-2">
      <h3 id="push-devices" className="text-xs font-semibold uppercase tracking-wide text-muted">
        Devices ({settings.devices.length} of {settings.max_devices})
      </h3>
      {settings.devices.length === 0 ? (
        <p className="text-xs text-muted">No device gets notifications yet.</p>
      ) : (
        <ul aria-label="Devices" className="flex flex-col divide-y divide-border rounded-md border border-border">
          {settings.devices.map((device) => (
            <li key={device.id} className="flex items-center gap-3 px-3 py-2">
              <Smartphone className="h-4 w-4 shrink-0 text-muted" aria-hidden />
              <div className="min-w-0 flex-1">
                <p className="flex items-center gap-2 truncate font-medium text-heading">
                  {describeAgent(device.user_agent)}
                  {device.endpoint_hash === currentHash && <Chip tone="accent">This device</Chip>}
                </p>
                <p className="truncate text-xs text-muted">
                  {device.service} · {deliveryLine(device)}
                </p>
              </div>
              <Button variant="ghost" icon={Trash2} onClick={() => remove.mutate(device)} disabled={remove.isPending} aria-label={`Remove ${describeAgent(device.user_agent)}`}>
                Remove
              </Button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function deliveryLine(device: PushDevice): string {
  if (device.failures > 0 && device.last_failure_at) return `failing since ${relativeTime(device.last_failure_at)}`;
  if (device.last_success_at) return `last delivered ${relativeTime(device.last_success_at)}`;
  return `added ${relativeTime(device.created_at)}`;
}
