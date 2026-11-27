// The manual's keyboard shortcuts page (docs/manual/reference/keyboard-shortcuts.md), made from the
// app's own table (lib/shortcuts.ts) so it cannot drift: manual.test.ts compares the committed page
// with this and, with UPDATE_MANUAL=1, writes it.
import { shortcutSections } from "../lib/shortcuts";

/** `g` sequences read "`g` then `j`"; other pairs are alternatives ("`↑` / `↓`"). */
function plain(list: string[]): string {
  return list.map((key) => "`" + key + "`").join(list[0] === "g" ? " then " : " / ");
}

export function shortcutsPage(): string {
  const mac = shortcutSections(true);
  const other = shortcutSections(false);
  const lines: string[] = [
    "# Keyboard shortcuts",
    "",
    "<!-- Generated from web/src/lib/shortcuts.ts, the table the app's own shortcuts sheet shows. Do not edit by hand: run `UPDATE_MANUAL=1 npx vitest run src/manual`. -->",
    "",
    "Press `?` anywhere in the app (outside a text field or terminal) for this list as a sheet: [Open the shortcuts sheet](app:sheet/shortcuts). " +
      "Two keys written \"`g` then `j`\" are a sequence: press `g`, then the second key within a second and a half.",
    "",
    "The `g` sequences and `?` do nothing while you type in a field or a terminal, so they never steal a letter from a shell or a message.",
    "",
  ];
  mac.forEach((section, index) => {
    const others = other[index]!;
    lines.push(`## ${section.title}`, "", "| On a Mac | Elsewhere | What it does |", "|---|---|---|");
    section.shortcuts.forEach((shortcut, row) => {
      const elsewhere = others.shortcuts[row]!;
      const what = shortcut.what === elsewhere.what ? shortcut.what : `${shortcut.what} (elsewhere: ${elsewhere.what})`;
      lines.push(`| ${plain(shortcut.keys)} | ${plain(elsewhere.keys)} | ${what.replace(/\|/g, "\\|")} |`);
    });
    lines.push("");
  });
  return lines.join("\n");
}
