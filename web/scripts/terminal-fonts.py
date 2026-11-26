#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.12"
# dependencies = ["fonttools[woff]==4.69.1"]
# ///
"""Builds the terminal's fallback webfonts: web/src/fonts/terminal/ (DESIGN.md 12, "Terminal font").

The terminal's font stack copies Jeff's Ghostty chain: JetBrains Mono for text, then the Nerd
Font symbols, DejaVu Sans Mono, Noto Sans Math and Noto Sans Symbols 2 for whatever the one
before lacks. All of it has to be self-hosted (the CSP is default-src 'self') and a phone must
not pay for it until a glyph is actually on screen, so this script:

1. reads which code points each font has, in chain order, and gives each code point to the
   first font that has it, skipping what JetBrains Mono already draws, control characters, and
   characters whose default presentation is emoji (those are left to the system's colour emoji
   font, as Ghostty does, rather than drawn as a monochrome symbol);
2. cuts each font's share into chunks of at most MAX_GLYPHS_PER_CHUNK glyphs, one 256-code-point
   page or several adjacent pages per chunk, and writes each chunk as a woff2;
3. writes terminal-fonts.css: one @font-face per chunk whose unicode-range is exactly the code
   points in it, so the browser downloads a chunk only when one of those characters is drawn.

Inputs are pinned: the Nerd Fonts symbols-only release and the Unicode emoji data are downloaded
and checked against a SHA-256; DejaVu and the two Noto fonts come from the exact-pinned
@fontsource packages in web/node_modules (devDependencies; run `mise run setup` first).

Run it with `mise run fonts:terminal` after changing a pin. The output is committed.
"""

from __future__ import annotations

import hashlib
import io
import logging
import shutil
import sys
import tarfile
import urllib.request
from dataclasses import dataclass
from pathlib import Path

from fontTools import subset
from fontTools.ttLib import TTFont

WEB = Path(__file__).resolve().parent.parent
MODULES = WEB / "node_modules"
OUT = WEB / "src" / "fonts" / "terminal"

NERD_VERSION = "v3.5.1"
NERD_URL = f"https://github.com/ryanoasis/nerd-fonts/releases/download/{NERD_VERSION}/NerdFontsSymbolsOnly.tar.xz"
NERD_SHA256 = "01172f37db8543edb102e5cb5c64101c9f4686630804d49b419aa07b23a69996"
EMOJI_URL = "https://www.unicode.org/Public/16.0.0/ucd/emoji/emoji-data.txt"
EMOJI_SHA256 = "f1365a5173eee18e1f98b240cdc492e84a25f1ce7e0c9d1094eb29c41a22696a"

# A chunk this size is a few KB to about 60 KB as woff2 (devicons are the heavy end): small enough
# that one stray icon costs little, large enough that a prompt full of them is a few files.
MAX_GLYPHS_PER_CHUNK = 200

# Name records kept in the subsets: the names, plus copyright (0), licence (13) and licence URL
# (14), which the OFL wants to travel with the font.
NAME_IDS = [0, 1, 2, 3, 4, 5, 6, 13, 14, 16, 17]


@dataclass
class Face:
    """One font in the chain: its CSS family, a file-name slug, and its source files."""

    family: str
    slug: str
    sources: list[Path]
    licence: Path


# ---- Inputs -------------------------------------------------------------------


def download(url: str, sha256: str) -> bytes:
    """Fetches url and refuses it unless it hashes to sha256 (the pins above)."""
    with urllib.request.urlopen(url, timeout=120) as response:
        body = response.read()
    digest = hashlib.sha256(body).hexdigest()
    if digest != sha256:
        sys.exit(f"{url}: sha256 {digest}, expected {sha256}. Update the pin only after checking the new release.")
    return body


def nerd_font(work: Path) -> tuple[Path, Path]:
    """Unpacks SymbolsNerdFontMono-Regular.ttf and the release's LICENSE into work."""
    archive = tarfile.open(fileobj=io.BytesIO(download(NERD_URL, NERD_SHA256)), mode="r:xz")
    wanted = {"SymbolsNerdFontMono-Regular.ttf", "LICENSE", "README.md"}
    for member in archive.getmembers():
        if member.isfile() and member.name in wanted:
            (work / member.name).write_bytes(archive.extractfile(member).read())  # type: ignore[union-attr]
    return work / "SymbolsNerdFontMono-Regular.ttf", work / "LICENSE"


def emoji_presentation() -> set[int]:
    """Code points whose default presentation is emoji (Unicode's Emoji_Presentation property)."""
    points: set[int] = set()
    for line in download(EMOJI_URL, EMOJI_SHA256).decode().splitlines():
        line = line.split("#", 1)[0].strip()
        if not line or ";" not in line:
            continue
        field, prop = (part.strip() for part in line.split(";", 1))
        if prop != "Emoji_Presentation":
            continue
        first, _, last = field.partition("..")
        points.update(range(int(first, 16), int(last or first, 16) + 1))
    return points


def fontsource(package: str, pattern: str) -> list[Path]:
    """The woff2 files of an installed @fontsource package matching pattern."""
    files = sorted((MODULES / package / "files").glob(pattern))
    if not files:
        sys.exit(f"no {pattern} in {package}: run `mise run setup` first")
    return files


def cmap(paths: list[Path]) -> dict[int, Path]:
    """Every code point the files draw, mapped to the first file that has it."""
    points: dict[int, Path] = {}
    for path in paths:
        for point in TTFont(path).getBestCmap():
            points.setdefault(point, path)
    return points


# ---- Chunks -------------------------------------------------------------------


def chunks(points: list[int]) -> list[list[int]]:
    """Cuts code points (all from one source file) into chunks of adjacent 256-point pages, at
    most MAX_GLYPHS_PER_CHUNK each; a page larger than that is split on its own. Pages that are
    not neighbours never share a chunk, so the powerline separators (U+E0A0-E0D7, the glyphs
    every prompt uses) cost a 3 KB file rather than riding with a page of unrelated icons."""
    pages: dict[int, list[int]] = {}
    for point in sorted(points):
        pages.setdefault(point >> 8, []).append(point)
    result: list[list[int]] = []
    current: list[int] = []
    previous_page = -2
    for page in sorted(pages):
        members = pages[page]
        if current and page != previous_page + 1:
            result.append(current)
            current = []
        previous_page = page
        for start in range(0, len(members), MAX_GLYPHS_PER_CHUNK):
            piece = members[start : start + MAX_GLYPHS_PER_CHUNK]
            if current and len(current) + len(piece) > MAX_GLYPHS_PER_CHUNK:
                result.append(current)
                current = []
            current.extend(piece)
    if current:
        result.append(current)
    return result


def unicode_range(points: list[int]) -> str:
    """The CSS unicode-range for a set of code points, runs collapsed (U+E0A0-E0A3)."""
    runs: list[str] = []
    ordered = sorted(points)
    start = previous = ordered[0]
    for point in [*ordered[1:], None]:
        if point is not None and point == previous + 1:
            previous = point
            continue
        runs.append(f"U+{start:X}" if start == previous else f"U+{start:X}-{previous:X}")
        if point is not None:
            start = previous = point
    return ",".join(runs)


def write_subset(source: Path, points: list[int], target: Path) -> None:
    """Writes the glyphs for points, taken from source, as a woff2 at target."""
    options = subset.Options()
    options.flavor = "woff2"
    options.hinting = False  # browsers on macOS and iOS ignore it; it is a third of DejaVu's size
    options.name_IDs = NAME_IDS
    options.notdef_outline = False
    options.drop_tables += ["FFTM", "PfEd"]  # FontForge's private tables: no use to a browser
    options.layout_features = ["ccmp", "locl", "mark", "mkmk"]  # combining marks; no ligatures in a terminal
    font = subset.load_font(str(source), options)
    subsetter = subset.Subsetter(options)
    subsetter.populate(unicodes=points)
    subsetter.subset(font)
    subset.save_font(font, str(target), options)


# ---- CSS ----------------------------------------------------------------------

CSS_HEADER = """/* GENERATED by web/scripts/terminal-fonts.py (mise run fonts:terminal); do not edit.

   The terminal's font chain (DESIGN.md 12, "Terminal font"), copying Jeff's Ghostty config:
   JetBrainsMono Nerd Font Mono when it is installed (local() only: nothing is downloaded for it),
   else the self-hosted JetBrains Mono Variable (index.css), then the faces below for the glyphs
   it lacks. Each face is cut into chunks whose unicode-range lists exactly the code points in
   them, so a chunk is fetched only when one of its characters is drawn. Imported by XTerm.tsx
   alone: the terminal is the only place this stack is used. Licences: LICENSE-*.txt here. */

@font-face {
  font-family: "JetBrainsMono Nerd Font Mono";
  font-style: normal;
  font-weight: 400;
  src: local("JetBrainsMono NFM Regular"), local("JetBrainsMonoNFM-Regular"), local("JetBrainsMono Nerd Font Mono");
}

@font-face {
  font-family: "JetBrainsMono Nerd Font Mono";
  font-style: normal;
  font-weight: 700;
  src: local("JetBrainsMono NFM Bold"), local("JetBrainsMonoNFM-Bold");
}
"""


def face_rule(family: str, file_name: str, points: list[int]) -> str:
    return (
        "\n@font-face {\n"
        f'  font-family: "{family}";\n'
        "  font-style: normal;\n"
        "  font-weight: 400;\n"
        "  font-display: swap;\n"
        f'  src: url("./{file_name}") format("woff2");\n'
        f"  unicode-range: {unicode_range(points)};\n"
        "}\n"
    )


# ---- Main ---------------------------------------------------------------------


def main() -> None:
    logging.getLogger("fontTools").setLevel(logging.ERROR)
    work = OUT.parent / ".terminal-fonts-work"
    shutil.rmtree(work, ignore_errors=True)
    work.mkdir(parents=True)
    try:
        build(work)
    finally:
        shutil.rmtree(work, ignore_errors=True)


def build(work: Path) -> None:
    nerd, nerd_licence = nerd_font(work)
    faces = [
        Face("Symbols Nerd Font Mono", "nerd", [nerd], nerd_licence),
        Face("DejaVu Sans Mono", "dejavu", fontsource("@fontsource/dejavu-mono", "*-latin-400-normal.woff2"), MODULES / "@fontsource/dejavu-mono/LICENSE"),
        Face("Noto Sans Math", "math", fontsource("@fontsource/noto-sans-math", "*-400-normal.woff2"), MODULES / "@fontsource/noto-sans-math/LICENSE"),
        Face("Noto Sans Symbols 2", "symbols2", fontsource("@fontsource/noto-sans-symbols-2", "*-400-normal.woff2"), MODULES / "@fontsource/noto-sans-symbols-2/LICENSE"),
    ]
    taken = set(cmap(fontsource("@fontsource-variable/jetbrains-mono", "*-wght-normal.woff2")))
    taken |= set(range(0x00, 0x20)) | set(range(0x7F, 0xA0)) | {0xFEFF}
    taken |= emoji_presentation()

    for stale in OUT.glob("*.woff2"):
        stale.unlink()
    OUT.mkdir(parents=True, exist_ok=True)
    css = [CSS_HEADER]
    report: list[str] = []
    for face in faces:
        points = {point: path for point, path in cmap(face.sources).items() if point not in taken}
        taken |= set(points)
        by_source: dict[Path, list[int]] = {}
        for point, source in points.items():
            by_source.setdefault(source, []).append(point)
        total = count = 0
        for source, source_points in by_source.items():
            for chunk in chunks(source_points):
                name = f"{face.slug}-{min(chunk):04x}.woff2"
                write_subset(source, chunk, OUT / name)
                total += (OUT / name).stat().st_size
                count += 1
                css.append(face_rule(face.family, name, chunk))
        report.append(f"{face.family}: {len(points)} glyphs, {count} chunks, {total / 1024:.0f} KB")
        shutil.copyfile(face.licence, OUT / f"LICENSE-{face.slug}.txt")
    (OUT / "terminal-fonts.css").write_text("".join(css))
    shutil.copyfile(work / "README.md", OUT / "README-nerd-fonts.md")
    print("\n".join(report))


if __name__ == "__main__":
    main()
