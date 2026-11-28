// Characters that change how text reads without being seen (R7-S3: "Trojan Source"). In the views a
// human uses to approve work (the diff, its file list, a task file's raw text, a restored layer),
// a right-to-left override can make `if (isAdmin)` read as a comment, a zero-width space can hide a
// word, and a Cyrillic letter "a" can make a file name read as a harmless one. Nothing here is silent:
// each such character is drawn as a visible chip (⟨U+202E⟩), each word that mixes Latin with
// Cyrillic, Greek or Armenian letters is marked, and the views show a warning that says how many.
//
// "Invisible" is Unicode's format category (Cf: the bidi embeddings, overrides and isolates, the
// marks LRM/RLM/ALM, zero-width space and joiners, word joiner, BOM, soft hyphen, tag characters),
// plus the line and paragraph separators and the fillers that render as nothing (Hangul fillers,
// the combining grapheme joiner, the Mongolian vowel separator). Emoji variation selectors are
// left alone: they are everywhere in ordinary text and change nothing a reviewer reads.

// The combining grapheme joiner (U+034F) is outside the class: inside one it reads as joined to
// the character before it.
const INVISIBLE = /[\p{Cf}\u2028\u2029\u115F\u1160\u3164\uFFA0\u180E]|\u034F/u;
const INVISIBLE_ALL = /[\p{Cf}\u2028\u2029\u115F\u1160\u3164\uFFA0\u180E]|\u034F/gu;
const BIDI = /[\u061C\u200E\u200F\u202A-\u202E\u2066-\u2069]/u;
const WORD = /[\p{L}\p{M}\p{N}_]+/gu;
const OTHER_SCRIPTS = /[\p{Script=Cyrillic}\p{Script=Greek}\p{Script=Armenian}]/u;
const LATIN = /\p{Script=Latin}/u;

/** What a text hides: how many invisible characters, whether any changes direction, and the words that mix scripts. */
export interface HiddenReport {
  invisible: number;
  bidi: boolean;
  mixedWords: string[];
}

/** A piece of text for display: plain text, one invisible character (drawn as a chip), or a mixed-script word. */
export type HiddenPiece = { kind: "text"; text: string } | { kind: "invisible"; text: string; code: string } | { kind: "mixed"; text: string };

/**
 * Cyrillic and Greek letters that draw as Latin ones (the same set as questions.GrantingText's
 * confusables). A word written only in these reads as a Latin word that is not one: `рауроl`.
 */
const LATIN_LOOKALIKES = /^[аеорсухіјѕԁӏАЕОРСХІЈЅМНТВКοαεικνρτυΟΑΒΕΗΙΚΜΝΡΤΧ]+$/u;

/**
 * True for a word that mixes Latin with Cyrillic, Greek or Armenian letters, or one written wholly
 * in Cyrillic or Greek letters that look Latin (R9-S11: an all-Cyrillic `раураl` has no Latin
 * letter to mix with, and reads as a Latin file name all the same).
 */
function mixesScripts(word: string): boolean {
  if (LATIN.test(word)) return OTHER_SCRIPTS.test(word);
  return word.length > 1 && LATIN_LOOKALIKES.test(word);
}

/** The code point of a one-character string as U+XXXX. */
export function codePointLabel(char: string): string {
  return `U+${(char.codePointAt(0) ?? 0).toString(16).toUpperCase().padStart(4, "0")}`;
}

/** Scans texts (a file's path, its diff, …) for what they hide. */
export function findHidden(...texts: (string | null | undefined)[]): HiddenReport {
  const report: HiddenReport = { invisible: 0, bidi: false, mixedWords: [] };
  for (const text of texts) {
    if (!text) continue;
    report.invisible += text.match(INVISIBLE_ALL)?.length ?? 0;
    report.bidi ||= BIDI.test(text);
    for (const word of text.match(WORD) ?? []) {
      if (mixesScripts(word) && !report.mixedWords.includes(word) && report.mixedWords.length < 20) report.mixedWords.push(word);
    }
  }
  return report;
}

/** True when the report found anything. */
export function hidesSomething(report: HiddenReport): boolean {
  return report.invisible > 0 || report.mixedWords.length > 0;
}

/** One sentence for a warning ("2 invisible characters, one of them changes the reading direction; 1 word mixes alphabets: paypal with a Cyrillic p"). */
export function describeHidden(report: HiddenReport): string {
  const parts: string[] = [];
  if (report.invisible > 0) {
    parts.push(
      `${report.invisible} invisible character${report.invisible === 1 ? "" : "s"}${report.bidi ? ", which can change the reading direction" : ""} (shown as ⟨U+…⟩)`,
    );
  }
  if (report.mixedWords.length > 0) {
    const n = report.mixedWords.length;
    parts.push(`${n} word${n === 1 ? "" : "s"} written with look-alike letters from another alphabet (${report.mixedWords.slice(0, 3).join(", ")})`);
  }
  return parts.join("; ");
}

/** Splits text into pieces, each invisible character on its own and each mixed-script word marked. */
export function revealHidden(text: string): HiddenPiece[] {
  if (!INVISIBLE.test(text) && !OTHER_SCRIPTS.test(text)) return text ? [{ kind: "text", text }] : [];
  const pieces: HiddenPiece[] = [];
  let plain = "";
  const flush = () => {
    if (plain) pieces.push({ kind: "text", text: plain });
    plain = "";
  };
  // Words first (so a mixed word stays whole), then each character of the rest.
  let last = 0;
  for (const match of text.matchAll(WORD)) {
    const start = match.index ?? 0;
    // Not `plain += revealChars(…)`: that reads plain before the call, which may flush it.
    const before = revealChars(text.slice(last, start), pieces, flush);
    plain += before;
    if (mixesScripts(match[0])) {
      flush();
      pieces.push({ kind: "mixed", text: match[0] });
    } else {
      plain += match[0];
    }
    last = start + match[0].length;
  }
  const after = revealChars(text.slice(last), pieces, flush);
  plain += after;
  flush();
  // Neighbouring plain pieces (split around a word or a chip) are one piece.
  return pieces.reduce<HiddenPiece[]>((merged, piece) => {
    const previous = merged[merged.length - 1];
    if (piece.kind === "text" && previous?.kind === "text") previous.text += piece.text;
    else merged.push(piece);
    return merged;
  }, []);
}

// revealChars moves the invisible characters of a run into pieces (flushing the plain text before
// each) and returns the plain text left after the last one.
function revealChars(run: string, pieces: HiddenPiece[], flush: () => void): string {
  let rest = "";
  for (const char of run) {
    if (INVISIBLE.test(char)) {
      // The caller's buffered text comes before this run's, which comes before the chip.
      flush();
      if (rest) pieces.push({ kind: "text", text: rest });
      rest = "";
      pieces.push({ kind: "invisible", text: char, code: codePointLabel(char) });
    } else {
      rest += char;
    }
  }
  return rest;
}
