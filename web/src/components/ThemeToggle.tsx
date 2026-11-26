// The three-state theme button (System → Light → Dark) in the header.
import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { applyTheme, nextTheme, readTheme, type ThemeChoice } from "../lib/theme";

const labels: Record<ThemeChoice, string> = { system: "System theme", light: "Light theme", dark: "Dark theme" };
const icons = { system: Monitor, light: Sun, dark: Moon };

export function ThemeToggle() {
  const [choice, setChoice] = useState<ThemeChoice>(readTheme);

  useEffect(() => {
    applyTheme(choice);
    if (choice !== "system" || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    const follow = () => applyTheme("system");
    media.addEventListener("change", follow);
    return () => media.removeEventListener("change", follow);
  }, [choice]);

  const Icon = icons[choice];
  return (
    <button
      type="button"
      onClick={() => setChoice(nextTheme(choice))}
      title={`${labels[choice]} (click to change)`}
      aria-label={labels[choice]}
      className="inline-flex h-10 w-10 items-center justify-center rounded text-chrome-muted hover:bg-chrome-hover hover:text-chrome-fg"
    >
      <Icon className="h-4 w-4" />
    </button>
  );
}
