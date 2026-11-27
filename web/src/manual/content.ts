// The manual's files, bundled: every docs/manual/**/*.md as text and every docs/manual/img/* as a
// fingerprinted asset URL. This module is only ever imported dynamically (lib/manual-queries.ts),
// so the manual is its own chunk, fetched the first time Help, a "?" or a palette search needs it,
// and embedded in the binary with the rest of web/dist.
import { buildIndex, buildManual, type Manual, type SearchEntry } from "../lib/manual";

const files = import.meta.glob<string>("../../../docs/manual/**/*.md", { query: "?raw", import: "default", eager: true });
const images = import.meta.glob<string>("../../../docs/manual/img/*.{png,jpg,svg}", { query: "?url", import: "default", eager: true });

export interface LoadedManual {
  manual: Manual;
  index: SearchEntry[];
}

export function loadManual(): LoadedManual {
  const manual = buildManual(files, images);
  return { manual, index: buildIndex(manual) };
}

/** The raw files, for the tests that keep the manual honest. */
export const manualFiles = files;
export const manualImages = images;
