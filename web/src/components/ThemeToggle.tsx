// The three-state theme button (System → Light → Dark) in the header.
import { Monitor, Moon, Sun } from "lucide-react";
import { useEffect, useState } from "react";
import { applyTheme, nextTheme, readTheme, type ThemeChoice } from "../lib/theme";
import { IconButton } from "./ui/Button";

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

  return (
    <IconButton
      icon={icons[choice]}
      tone="chrome"
      onClick={() => setChoice(nextTheme(choice))}
      label={labels[choice]}
      title={`${labels[choice]} (click to change)`}
    />
  );
}
