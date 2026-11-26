// A small fuzzy matcher for the command palette, scored the way fzf's v2 algorithm scores: every
// matched character earns points, a match at the start of a word (after a space, slash, dash,
// underscore, dot or colon, or a lower-to-upper camel step) earns a bonus, a run of consecutive
// characters keeps that bonus going, and a gap costs a little to open and less to extend. The
// best alignment is found by dynamic programming over (pattern, text), so "jfx" in
// "job: fix the export" picks j, f and x at word starts rather than the first j, f, x it sees.
//
// Matching is case-insensitive. Texts are cut at MAX_TEXT characters and patterns at
// MAX_PATTERN, which keeps the table under 16k cells: the palette scores a few hundred items per
// keystroke, well under a millisecond each.

export interface FuzzyMatch {
  score: number;
  /** Indexes into the text of the matched characters, ascending. */
  positions: number[];
}

const SCORE_MATCH = 16;
const GAP_START = -3;
const GAP_EXTENSION = -1;
const BONUS_BOUNDARY = 8;
const BONUS_CAMEL = 7;
const BONUS_CONSECUTIVE = 4;
const BONUS_FIRST_CHAR_MULTIPLIER = 2;
/** Matching the very first character of the text is worth a little more than any word start. */
const BONUS_TEXT_START = 2;

const MAX_TEXT = 256;
const MAX_PATTERN = 32;

/** The best score one pattern character can earn: a consecutive match at a word boundary. */
export const PERFECT_CHAR_SCORE = SCORE_MATCH + BONUS_BOUNDARY;

const boundaryChars = new Set([" ", "/", "-", "_", ".", ":", ",", "(", "[", "@", "#", "›", "\t"]);

function bonusAt(text: string, index: number): number {
  if (index === 0) return BONUS_BOUNDARY + BONUS_TEXT_START;
  const previous = text[index - 1] ?? "";
  const current = text[index] ?? "";
  if (boundaryChars.has(previous)) return BONUS_BOUNDARY;
  const prevLower = previous.toLowerCase() === previous && previous.toUpperCase() !== previous;
  const curUpper = current.toUpperCase() === current && current.toLowerCase() !== current;
  if (prevLower && curUpper) return BONUS_CAMEL;
  const prevLetter = /[\p{L}]/u.test(previous);
  const curDigit = /[0-9]/.test(current);
  if (prevLetter && curDigit) return BONUS_CAMEL;
  return 0;
}

/**
 * Scores pattern against text. Returns null when the pattern's characters do not all appear in
 * order. An empty pattern matches everything with score 0. Whitespace in the pattern is matched
 * literally; split multi-word queries with fuzzyMatchAll.
 */
export function fuzzyMatch(rawPattern: string, rawText: string): FuzzyMatch | null {
  const pattern = rawPattern.slice(0, MAX_PATTERN).toLowerCase();
  if (pattern.length === 0) return { score: 0, positions: [] };
  const text = rawText.slice(0, MAX_TEXT);
  const lower = text.toLowerCase();
  const m = pattern.length;
  const n = lower.length;
  if (m > n) return null;

  // Quick reject, and the window the alignment must fit in: the first index where the pattern
  // can start, and the last index where it can end.
  let first = -1;
  for (let i = 0, j = 0; j < n; j++) {
    if (lower[j] === pattern[i]) {
      if (i === 0) first = j;
      i++;
      if (i === m) break;
    }
    if (j === n - 1) return null;
  }
  let last = n - 1;
  for (let i = m - 1, j = n - 1; j >= 0; j--) {
    if (lower[j] === pattern[i]) {
      if (i === m - 1) last = j;
      i--;
      if (i < 0) break;
    }
  }

  const NONE = -1e9;
  const width = n;
  // score[i*width+j]: best score with pattern[i] matched at text[j]; from[...]: where pattern[i-1] was.
  const score = new Float64Array(m * width).fill(NONE);
  const from = new Int32Array(m * width).fill(-1);
  // run[i*width+j]: the bonus carried by a consecutive run ending at j, so a run keeps its word-start bonus.
  const run = new Float64Array(m * width);
  const bonuses = new Float64Array(n);
  for (let j = first; j <= last; j++) bonuses[j] = bonusAt(text, j);

  for (let i = 0; i < m; i++) {
    const ch = pattern[i];
    // The best "gapped" predecessor for the current j: max over k <= j-2 of score[i-1][k] plus the
    // gap cost to reach j, carried forward one column at a time.
    let gapBest = NONE;
    let gapFrom = -1;
    for (let j = first + i; j <= last; j++) {
      if (i > 0 && j >= 2) {
        const candidate = score[(i - 1) * width + (j - 2)] ?? NONE;
        const extended = gapBest + GAP_EXTENSION;
        const opened = candidate === NONE ? NONE : candidate + GAP_START;
        if (opened >= extended) {
          gapBest = opened;
          gapFrom = j - 2;
        } else {
          gapBest = extended;
        }
      }
      if (lower[j] !== ch) continue;
      const bonus = bonuses[j] ?? 0;
      const cell = i * width + j;
      if (i === 0) {
        score[cell] = SCORE_MATCH + bonus * BONUS_FIRST_CHAR_MULTIPLIER;
        run[cell] = bonus;
        continue;
      }
      let best = NONE;
      let bestFrom = -1;
      let bestRun = 0;
      const diagonal = j >= 1 ? (score[(i - 1) * width + (j - 1)] ?? NONE) : NONE;
      if (diagonal !== NONE) {
        const carried = Math.max(run[(i - 1) * width + (j - 1)] ?? 0, bonus, BONUS_CONSECUTIVE);
        best = diagonal + SCORE_MATCH + carried;
        bestFrom = j - 1;
        bestRun = carried;
      }
      if (gapBest !== NONE) {
        const gapped = gapBest + SCORE_MATCH + bonus;
        if (gapped > best) {
          best = gapped;
          bestFrom = gapFrom;
          bestRun = bonus;
        }
      }
      if (best === NONE) continue;
      score[cell] = best;
      from[cell] = bestFrom;
      run[cell] = bestRun;
    }
  }

  let end = -1;
  let top = NONE;
  for (let j = first + m - 1; j <= last; j++) {
    const value = score[(m - 1) * width + j] ?? NONE;
    if (value > top) {
      top = value;
      end = j;
    }
  }
  if (end < 0) return null;
  const positions = new Array<number>(m);
  for (let i = m - 1, j = end; i >= 0; i--) {
    positions[i] = j;
    j = from[i * width + j] ?? -1;
  }
  return { score: top, positions };
}

/** Splits a query into its words (whitespace-separated, empty words dropped). */
export function queryTokens(query: string): string[] {
  return query.trim().split(/\s+/).filter(Boolean);
}

/** A match of every token against one text: summed score and merged positions, or null. */
export function fuzzyMatchAll(tokens: string[], text: string): FuzzyMatch | null {
  let total = 0;
  const positions = new Set<number>();
  for (const token of tokens) {
    const match = fuzzyMatch(token, text);
    if (!match) return null;
    total += match.score;
    for (const position of match.positions) positions.add(position);
  }
  return { score: total, positions: [...positions].sort((a, b) => a - b) };
}

/** Text cut into plain and matched runs, for rendering highlights. */
export function highlightRuns(text: string, positions: readonly number[]): Array<{ text: string; match: boolean }> {
  if (positions.length === 0) return text ? [{ text, match: false }] : [];
  const marked = new Set(positions);
  const runs: Array<{ text: string; match: boolean }> = [];
  for (let index = 0; index < text.length; index++) {
    const match = marked.has(index);
    const lastRun = runs[runs.length - 1];
    if (lastRun && lastRun.match === match) lastRun.text += text[index];
    else runs.push({ text: text[index] ?? "", match });
  }
  return runs;
}
