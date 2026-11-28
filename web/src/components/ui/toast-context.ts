// The toast context and hook, apart from the provider component so React Fast Refresh can
// hot-swap Toast.tsx. Views call useToast().show(...) after a mutation.
import { createContext, useContext } from "react";

export type ToastTone = "info" | "success" | "warning" | "danger";

export interface ToastInput {
  tone?: ToastTone;
  title: string;
  description?: string;
  /** One follow-up, shown as a link-style button ("Open j_12"); the toast closes when it is used. */
  action?: { label: string; onClick: () => void };
  /** A newer toast with the same key replaces this one instead of stacking under it (the T key's theme toasts, R10). */
  key?: string;
}

export interface ToastApi {
  show: (toast: ToastInput) => void;
}

export const ToastContext = createContext<ToastApi | null>(null);

export function useToast(): ToastApi {
  const toast = useContext(ToastContext);
  if (!toast) throw new Error("useToast must be used inside <ToastProvider>");
  return toast;
}
