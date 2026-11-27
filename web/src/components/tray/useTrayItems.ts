// The tray's rows and its snooze, shared by the desktop panel, the phone bar and the Now view
// (DESIGN.md 8.7, 22). Both come from the server: the rows are the attention queue, and a snooze
// is kept on the server so it holds on every device.
import { useMemo } from "react";
import { trayItems, type TrayItem } from "../../lib/needsYou";
import { useAttention, useAttentionAction } from "../../lib/queries";

/** The tray's rows, kept live by the `attention` event. */
export function useTrayItems(): TrayItem[] {
  const attention = useAttention();
  return useMemo(() => trayItems(attention.data), [attention.data]);
}

/** Snoozes an item for 10 minutes on every device. */
export function useSnooze(): (id: string) => void {
  const action = useAttentionAction();
  return (id: string) => action.mutate({ action: "snooze", id });
}
