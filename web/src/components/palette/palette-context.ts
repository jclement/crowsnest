// The command palette's context, apart from the provider component so React Fast Refresh can
// hot-swap PaletteProvider.tsx. The header button and anything else that opens the palette or the
// shortcuts sheet calls usePalette().
import { createContext, useContext } from "react";
import type { PaletteSheet } from "../../lib/palette";

export interface PaletteApi {
  /** Opens the palette; call it inside the click (or key) handler so a phone raises its keyboard. */
  openPalette: () => void;
  /** Opens the keyboard shortcuts sheet. */
  openShortcuts: () => void;
  /** Opens one of the palette's sheets (Settings, Schedule, …): the manual's "open" buttons. */
  openSheet: (sheet: PaletteSheet) => void;
  /** Opens a manual section in the help sheet, over the current view ("concepts/jobs#held"). */
  openHelp: (topic: string) => void;
  /** ⌘K on a Mac, Ctrl K elsewhere. */
  chord: string;
  mac: boolean;
}

export const PaletteContext = createContext<PaletteApi | null>(null);

export function usePalette(): PaletteApi {
  const palette = useContext(PaletteContext);
  if (!palette) throw new Error("usePalette must be used inside <PaletteProvider>");
  return palette;
}

/** The palette's API when there is one (the manual renders outside the app frame in tests). */
export function usePaletteMaybe(): PaletteApi | null {
  return useContext(PaletteContext);
}
