// The frame for the pages a visitor meets first, before the app: sign in and first-run setup.
// The first impression (DESIGN.md "Design language"): the chrome strip with the wordmark, then
// the mark large in its lookout's rings above a bordered panel that holds the form, and a
// tagline underneath. No pictures, no gradients: the same lines and tokens as the app.
import type { ReactNode } from "react";
import { cn } from "../../lib/cn";
import { Footer } from "../Footer";
import { ThemeToggle } from "../ThemeToggle";
import { Mark, Wordmark } from "./Brand";

export function FirstLight({ children, tagline, className }: { children: ReactNode; tagline?: string; className?: string }) {
  return (
    <div className="flex min-h-full flex-col">
      <header className="flex h-11 shrink-0 items-center gap-2 border-b border-chrome-border bg-chrome pl-3 pr-1 text-chrome-fg md:h-10">
        <Wordmark />
        <div className="ml-auto">
          <ThemeToggle />
        </div>
      </header>
      <main className="flex flex-1 items-start justify-center px-4 pb-6 pt-8 md:items-center md:pt-4">
        <div className={cn("flex w-full max-w-sm flex-col items-center", className)}>
          <div aria-hidden className="relative mb-5 flex h-16 w-16 items-center justify-center text-heading">
            <span className="absolute -inset-3 rounded-full border border-dashed border-border" />
            <span className="absolute -inset-3 rounded-full border border-transparent border-t-accent [transform:rotate(40deg)]" />
            <span className="absolute inset-0 rounded-full border border-border bg-surface-raised" />
            <Mark className="relative h-9 w-9" accentClassName="text-accent" />
          </div>
          <div className="w-full rounded-md border border-border bg-surface-raised p-4 shadow-[0_1px_0_var(--color-border)]">{children}</div>
          {tagline && <p className="mt-4 max-w-xs text-center text-xs italic text-muted">{tagline}</p>}
        </div>
      </main>
      <Footer className="px-3 py-2 text-center" />
    </div>
  );
}
