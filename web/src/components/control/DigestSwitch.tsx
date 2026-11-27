// DigestSwitch: the daily digest's row in the Schedule sheet (DESIGN.md 7.13): one switch and a
// time, off by default.
import { useState } from "react";
import { useDigestSettings, useSaveDigestSettings } from "../../lib/queries";
import { Switch } from "../ui/Switch";
import { useToast } from "../ui/toast-context";

export function DigestSwitch({ onOpenList }: { onOpenList?: () => void }) {
  const settings = useDigestSettings();
  const save = useSaveDigestSettings();
  const toast = useToast();
  const [atEdit, setAt] = useState<string | null>(null);
  if (!settings.data) return null;
  const at = atEdit ?? settings.data.at;
  const store = (enabled: boolean, time: string) =>
    save.mutate({ enabled, at: time }, { onError: (error) => toast.show({ tone: "danger", title: "Not saved", description: error.message }) });
  return (
    <div className="flex flex-wrap items-center gap-2 border-b border-border px-3 py-2 text-sm" aria-label="Daily digest">
      <Switch checked={settings.data.enabled} onChange={(enabled: boolean) => store(enabled, at)} label="Daily digest" />
      <span className="font-medium text-heading">Daily digest</span>
      <span className="text-xs text-muted">at</span>
      <input aria-label="Digest time" value={at} onChange={(event) => setAt(event.target.value)} onBlur={() => at !== settings.data?.at && store(settings.data?.enabled ?? false, at)}
        className="h-8 w-20 rounded border border-border bg-surface px-2 text-sm" />
      <span className="min-w-0 flex-1 text-xs text-muted">What finished, what waits on you, each lead's day, the spend, what is next: in the Inbox.</span>
      {onOpenList && (
        <button type="button" className="text-xs text-accent underline" onClick={onOpenList}>
          Past digests
        </button>
      )}
    </div>
  );
}
