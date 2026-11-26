// The command palette's context, apart from the provider component so React Fast Refresh can
// hot-swap PaletteProvider.tsx. The header button and anything else that opens the palette or the
// shortcuts sheet calls usePalette().
import { createContext, useContext } from "react";

export interface PaletteApi {
  /** Opens the palette; call it inside the click (or key) handler so a phone raises its keyboard. */
  openPalette: () => void;
  /** Opens the keyboard shortcuts sheet. */
  openShortcuts: () => void;
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
