// Settings → Appearance: the theme gallery and picker (DESIGN.md "Design language").
//
// The mode (System / Light / Dark) and the theme (a named light/dark pair) are chosen
// separately and kept per browser. Every theme is shown in both modes with real components,
// each preview its own [data-theme] subtree, so what you see is what the theme does: a theme
// is data (src/themes/<id>.css), and a preview is just the same tokens scoped to a box.
//
// The server's "theme" setting is the default for browsers that never chose; "Use for new
// browsers" sets it, and "Follow the default" forgets this browser's own choice.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { cn } from "../../lib/cn";
import { systemEndpoints, systemKeys } from "../../lib/system";
import {
  clearThemeName,
  readTheme,
  readThemeName,
  setTheme,
  setThemeName,
  THEME_EVENT,
  THEME_NAME_EVENT,
  themeNameChosen,
  type ThemeMode,
} from "../../lib/theme";
import { DEFAULT_THEME, themes, type ThemeInfo } from "../../themes";
import { Mark } from "../brand/Brand";
import { Button } from "../ui/Button";
import { Chip, StateMark, type WorkState } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";

/** The mode and theme now applied, following changes made anywhere (palette, shortcut, toggle). */
export function useAppearance(): { mode: ThemeMode; theme: string; chosen: boolean } {
  const [state, setState] = useState(() => ({ mode: readTheme(), theme: readThemeName(), chosen: themeNameChosen() }));
  useEffect(() => {
    const follow = () => setState({ mode: readTheme(), theme: readThemeName(), chosen: themeNameChosen() });
    window.addEventListener(THEME_EVENT, follow);
    window.addEventListener(THEME_NAME_EVENT, follow);
    return () => {
      window.removeEventListener(THEME_EVENT, follow);
      window.removeEventListener(THEME_NAME_EVENT, follow);
    };
  }, []);
  return state;
}

const modes: Array<{ mode: ThemeMode; label: string; icon: typeof Sun }> = [
  { mode: "system", label: "System", icon: Monitor },
  { mode: "light", label: "Light", icon: Sun },
  { mode: "dark", label: "Dark", icon: Moon },
];

export function AppearanceSheet({ open, onClose }: { open: boolean; onClose: () => void }) {
  return (
    <Sheet open={open} onClose={onClose} title="Appearance">
      <AppearanceBody />
    </Sheet>
  );
}

/** The sheet's content, also usable on its own (the e2e gallery renders it full-page). */
export function AppearanceBody() {
  const { mode, theme, chosen } = useAppearance();
  const settings = useQuery({ queryKey: systemKeys.settings, queryFn: systemEndpoints.settings });
  const serverDefault = settings.data?.settings.find((setting) => setting.name === "theme");
  const defaultTheme = serverDefault?.value || DEFAULT_THEME;
  const queryClient = useQueryClient();
  const toast = useToast();
  const makeDefault = useMutation({
    mutationFn: (id: string) => systemEndpoints.updateSetting("theme", id === DEFAULT_THEME ? "" : id),
    onSuccess: () => {
      void queryClient.invalidateQueries({ queryKey: systemKeys.settings });
      toast.show({ tone: "success", title: "New browsers will start in this theme" });
    },
    onError: (error) => toast.show({ tone: "danger", title: "Could not set the default", description: (error as Error).message }),
  });
  const locked = serverDefault?.locked;

  return (
    <div className="flex flex-col gap-5 p-4">
      <section aria-labelledby="appearance-mode" className="flex flex-col gap-2">
        <h3 id="appearance-mode" className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted">
          Mode
        </h3>
        <div role="radiogroup" aria-labelledby="appearance-mode" className="inline-flex w-fit overflow-hidden rounded border border-border-strong">
          {modes.map(({ mode: value, label, icon: Icon }) => (
            <button
              key={value}
              type="button"
              role="radio"
              aria-checked={mode === value}
              onClick={() => setTheme(value)}
              className={cn(
                "inline-flex min-h-11 items-center gap-1.5 border-r border-border-strong px-3 text-sm last:border-r-0 md:min-h-8",
                mode === value ? "bg-accent text-accent-fg" : "bg-surface text-body hover:bg-hover",
              )}
            >
              <Icon className="h-4 w-4" aria-hidden />
              {label}
            </button>
          ))}
        </div>
        <p className="text-2xs text-muted">
          System follows this device. <kbd className="font-mono">T</kbd> cycles the theme from anywhere.
        </p>
      </section>

      <section aria-labelledby="appearance-theme" className="flex flex-col gap-2">
        <div className="flex flex-wrap items-baseline justify-between gap-2">
          <h3 id="appearance-theme" className="font-mono text-2xs font-semibold uppercase tracking-wider text-muted">
            Theme
          </h3>
          <p className="text-2xs text-muted">
            {chosen ? "Chosen in this browser." : "Following the default for new browsers."}{" "}
            {chosen && (
              <button type="button" className="text-accent underline underline-offset-2" onClick={() => clearThemeName()}>
                Follow the default
              </button>
            )}
          </p>
        </div>
        <div role="radiogroup" aria-labelledby="appearance-theme" className="grid gap-3">
          {themes.map((info) => (
            <ThemeCard
              key={info.id}
              theme={info}
              selected={info.id === theme}
              isDefault={info.id === defaultTheme}
              onSelect={() => setThemeName(info.id)}
              onMakeDefault={locked ? undefined : () => makeDefault.mutate(info.id)}
              busy={makeDefault.isPending}
            />
          ))}
        </div>
        {locked && <p className="text-2xs text-muted">The default for new browsers: {locked}.</p>}
      </section>
    </div>
  );
}

function ThemeCard({
  theme,
  selected,
  isDefault,
  onSelect,
  onMakeDefault,
  busy,
}: {
  theme: ThemeInfo;
  selected: boolean;
  isDefault: boolean;
  onSelect: () => void;
  onMakeDefault?: () => void;
  busy: boolean;
}) {
  return (
    <div className={cn("rounded-md border", selected ? "border-accent ring-1 ring-accent" : "border-border")}>
      <button
        type="button"
        role="radio"
        aria-checked={selected}
        aria-label={`${theme.name} theme`}
        onClick={onSelect}
        className="flex w-full flex-col gap-2 p-2 text-left hover:bg-hover"
      >
        <span className="flex w-full items-center gap-2 px-1">
          <span className="text-sm font-semibold text-heading">{theme.name}</span>
          {theme.aaa && <Chip mono>AAA</Chip>}
          {isDefault && <Chip>default</Chip>}
          {selected && (
            <span className="ml-auto inline-flex items-center gap-1 text-xs font-medium text-accent">
              <Check className="h-3.5 w-3.5" aria-hidden />
              In use
            </span>
          )}
        </span>
        <span className="px-1 text-xs text-muted">{theme.description}</span>
        <span className="grid w-full gap-2 sm:grid-cols-2">
          <ThemePreview theme={theme.id} dark={false} />
          <ThemePreview theme={theme.id} dark />
        </span>
      </button>
      {onMakeDefault && !isDefault && (
        <div className="flex justify-end border-t border-border px-2 py-1">
          <Button compact variant="ghost" disabled={busy} onClick={onMakeDefault}>
            Use for new browsers
          </Button>
        </div>
      )}
    </div>
  );
}

const sampleRows: Array<{ state: WorkState; title: string; meta: string }> = [
  { state: "needs", title: "Apply the fleet cache refactor?", meta: "j_11 · blocking" },
  { state: "stuck", title: "Profile the fleet cache", meta: "j_6 · 35 min" },
  { state: "running", title: "Render widgets at three sizes", meta: "j_23 · $3.10" },
  { state: "held", title: "Land the rate limiter", meta: "j_13 · gate" },
  { state: "done", title: "Tidy the install guide", meta: "j_2 · today" },
];

/**
 * One theme in one mode, drawn with the app's own pieces: the chrome strip, the state marks in
 * rows, chips, the two button kinds and a line of highlighted code. Decorative (aria-hidden): the
 * card around it names the theme.
 */
export function ThemePreview({ theme, dark, className }: { theme: string; dark: boolean; className?: string }) {
  return (
    <span
      aria-hidden
      data-theme={theme}
      className={cn("block overflow-hidden rounded border border-border bg-surface text-body", dark && "dark", className)}
    >
      <span className="flex h-7 items-center gap-1.5 bg-chrome px-2 text-chrome-fg">
        <Mark className="h-3.5 w-3.5" />
        <span className="font-mono text-2xs font-semibold">
          crows<span className="text-chrome-accent">nest</span>
        </span>
        <span className="ml-auto inline-flex items-center gap-1 font-mono text-2xs tabular-nums text-chrome-muted">
          <svg width="28" height="10" viewBox="0 0 28 10" className="text-chrome-accent">
            <polyline points="0,8 5,7 9,8 13,3 17,6 21,4 28,5" fill="none" stroke="currentColor" strokeWidth="1.5" />
          </svg>
          <span className="text-chrome-fg">23%</span>
        </span>
      </span>
      <span className="block divide-y divide-border">
        {sampleRows.map((row, index) => (
          <span key={row.state} className={cn("flex items-center gap-2 px-2 py-1", index === 2 && "bg-selected")}>
            <StateMark state={row.state} />
            <span className="min-w-0 flex-1 truncate text-2xs text-heading">{row.title}</span>
            <span className="shrink-0 font-mono text-[10px] tabular-nums text-muted">{row.meta}</span>
          </span>
        ))}
      </span>
      <span className="flex items-center gap-1.5 border-t border-border bg-surface-raised px-2 py-1.5">
        <span className="inline-flex h-5 items-center rounded bg-accent px-2 text-[10px] font-medium text-accent-fg">Give job</span>
        <span className="inline-flex h-5 items-center rounded border border-border-strong px-2 text-[10px] text-heading">Nudge</span>
        <Chip tone="danger" className="h-4 px-1 text-[10px]">
          Check failed
        </Chip>
        <Chip tone="success" className="h-4 px-1 text-[10px]">
          Accepted
        </Chip>
      </span>
      <span className="block truncate border-t border-border bg-surface-sunken px-2 py-1 font-mono text-[10px]">
        <span className="hljs-keyword">const</span> <span className="hljs-title">ok</span> = <span className="hljs-number">42</span>;{" "}
        <span className="hljs-comment">{"// all green"}</span>
      </span>
    </span>
  );
}
