"""Assembly: the recorded scenes, the narration, the music bed and the captions, into the film.

    uv run scripts/assemble.py           -> out/crowsnest-tour.mp4, out/crowsnest-tour.srt,
                                            contact-sheet.png, and a copy in ~/Downloads
    uv run scripts/assemble.py --no-copy  without the copy to ~/Downloads

Needs out/timeline.json (voice.py), out/clips/*.mp4 and out/captions/ (recorder/record.mjs),
and out/audio/bed.wav (music.py). The mix places every narration clip at its start, ducks the
bed under the voice with a smoothed gate, and normalises the whole to -16 LUFS. Captions are
burned in from the plates the recorder drew in the app's own font (this ffmpeg has no libass),
and written beside the film as an .srt.
"""
from __future__ import annotations

import argparse
import json
import shutil
import subprocess
import wave
from pathlib import Path

import numpy as np

PROMO = Path(__file__).resolve().parent.parent
OUT = PROMO / "out"
RATE = 48000
NAME = "crowsnest-tour"

MUSIC_DB = -16.0     # the bed under silence, relative to its own peak-normalised level
DUCK_DB = -9.0       # and this much lower again while the narrator speaks
VOICE_DB = 0.0


def ff(*args: str) -> None:
    subprocess.run(["ffmpeg", "-hide_banner", "-loglevel", "error", "-y", *args], check=True)


def decode(path: Path) -> np.ndarray:
    raw = subprocess.run(["ffmpeg", "-v", "error", "-i", str(path), "-f", "f32le", "-ac", "1", "-ar", str(RATE), "-"],
                         capture_output=True, check=True).stdout
    return np.frombuffer(raw, dtype="<f4").astype(np.float64)


def read_wav(path: Path) -> np.ndarray:
    with wave.open(str(path)) as w:
        data = np.frombuffer(w.readframes(w.getnframes()), dtype="<i2").astype(np.float64) / 32768
        return data.reshape(-1, w.getnchannels())


def write_wav(path: Path, data: np.ndarray) -> None:
    pcm = (np.clip(data, -1, 1) * 32767).astype("<i2")
    with wave.open(str(path), "wb") as w:
        w.setnchannels(data.shape[1])
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(pcm.tobytes())


def smooth_gate(voice: np.ndarray, attack: float = 0.12, release: float = 0.6) -> np.ndarray:
    """1 where the narrator speaks (with a lead-in and a slow release), 0 elsewhere."""
    hop = RATE // 100
    frames = len(voice) // hop + 1
    padded = np.pad(np.abs(voice), (0, frames * hop - len(voice)))
    level = padded.reshape(frames, hop).max(axis=1)
    on = (level > 0.01).astype(np.float64)
    # Hold through short pauses between words and lines.
    hold = int(0.5 * 100)
    held = np.convolve(on, np.ones(hold), mode="same") > 0
    g = np.zeros(frames)
    a, r = 1 / (attack * 100), 1 / (release * 100)
    for i in range(1, frames):
        target = 1.0 if held[i] else 0.0
        g[i] = min(target, g[i - 1] + a) if target > g[i - 1] else max(target, g[i - 1] - r)
    # Start ducking slightly before the voice: shift the curve earlier by the attack time.
    shift = int(attack * 100)
    g = np.concatenate([g[shift:], np.zeros(shift)])
    return np.repeat(g, hop)[: len(voice)]


def mix(timeline: dict) -> Path:
    total = int(timeline["duration"] * RATE) + RATE
    voice = np.zeros(total)
    for scene in timeline["scenes"]:
        for line in scene["lines"]:
            clip = decode(PROMO / line["clip"])
            s = int(line["start"] * RATE)
            voice[s:s + len(clip)] += clip[: total - s]
    voice *= 10 ** (VOICE_DB / 20)
    bed = read_wav(OUT / "audio" / "bed.wav")
    bed = np.pad(bed, ((0, max(0, total - len(bed))), (0, 0)))[:total]
    gain_db = MUSIC_DB + DUCK_DB * smooth_gate(voice)
    bed *= (10 ** (gain_db / 20))[:, None]
    out = bed + voice[:, None]
    path = OUT / "audio" / "mix.wav"
    write_wav(path, out[: int(timeline["duration"] * RATE)])
    return path


def srt_time(t: float) -> str:
    ms = int(round(t * 1000))
    return f"{ms // 3600000:02d}:{ms // 60000 % 60:02d}:{ms // 1000 % 60:02d},{ms % 1000:03d}"


def captions(plates: list[dict], duration: float) -> Path:
    """One RGBA stream of caption plates (blank between them), as a concat list."""
    blank = OUT / "captions" / "blank.png"
    ff("-f", "lavfi", "-i", "color=c=black@0.0:s=1920x1080,format=rgba", "-frames:v", "1", str(blank))
    lines, t = [], 0.0
    for p in plates:
        # A plate shows from 0.1 s before its words until the next one (or 0.5 s after them).
        start = max(t, p["start"] - 0.1)
        if start > t + 0.01:
            lines += [f"file '{blank}'", f"duration {start - t:.3f}"]
        nxt = next((q["start"] - 0.1 for q in plates if q["start"] > p["start"]), duration)
        end = min(p["end"] + 0.4, nxt)
        if nxt - end < 0.5:  # no flicker of nothing between two plates close together
            end = nxt
        lines += [f"file '{p['file']}'", f"duration {end - start:.3f}"]
        t = end
    lines += [f"file '{blank}'", f"duration {max(0.04, duration - t):.3f}", f"file '{blank}'"]
    path = OUT / "captions" / "track.txt"
    path.write_text("\n".join(lines) + "\n")
    return path


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--no-copy", action="store_true")
    args = ap.parse_args()
    timeline = json.loads((OUT / "timeline.json").read_text())
    plates = json.loads((OUT / "captions" / "plates.json").read_text())
    duration = timeline["duration"]

    concat = OUT / "clips" / "film.txt"
    concat.write_text("".join(f"file '{OUT / 'clips' / (s['id'] + '.mp4')}'\n" for s in timeline["scenes"]))
    audio = mix(timeline)
    track = captions(plates, duration)

    film = OUT / f"{NAME}.mp4"
    ff("-f", "concat", "-safe", "0", "-i", str(concat),
       "-f", "concat", "-safe", "0", "-i", str(track),
       "-i", str(audio),
       "-filter_complex", f"[1:v]fps={timeline['fps']},format=rgba[cap];[0:v][cap]overlay=0:0:format=auto,format=yuv420p[v];"
                          "[2:a]loudnorm=I=-16:TP=-1.5:LRA=11,aresample=48000[a]",
       "-map", "[v]", "-map", "[a]", "-t", f"{duration:.3f}",
       "-c:v", "libx264", "-preset", "slow", "-crf", "18", "-profile:v", "high", "-pix_fmt", "yuv420p",
       "-c:a", "aac", "-b:a", "192k", "-movflags", "+faststart", str(film))

    srt = OUT / f"{NAME}.srt"
    srt.write_text("".join(f"{i}\n{srt_time(p['start'])} --> {srt_time(p['end'] + 0.3)}\n{p['text']}\n\n"
                           for i, p in enumerate(plates, 1)))
    shutil.copy(srt, PROMO / f"{NAME}.srt")

    # Contact sheet: one frame from each scene (two from long ones), captions and all.
    times = []
    for s in timeline["scenes"]:
        n = 2 if s["duration"] > 10 else 1
        times += [s["start"] + s["duration"] * (k + 1) / (n + 1) for k in range(n)]
    sheet_dir = OUT / "sheet"
    shutil.rmtree(sheet_dir, ignore_errors=True)
    sheet_dir.mkdir()
    for i, t in enumerate(times):
        ff("-ss", f"{t:.2f}", "-i", str(film), "-frames:v", "1", "-vf", "scale=480:270", str(sheet_dir / f"{i:03d}.png"))
    cols = 6
    rows = -(-len(times) // cols)
    ff("-framerate", "1", "-i", str(sheet_dir / "%03d.png"), "-vf", f"tile={cols}x{rows}:padding=6:color=0x0b1220",
       "-frames:v", "1", str(PROMO / "contact-sheet.png"))

    if not args.no_copy:
        target = Path.home() / "Downloads" / f"{NAME}.mp4"
        shutil.copy(film, target)
    probe = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration,size", "-of", "csv=p=0", str(film)],
                           capture_output=True, text=True).stdout.strip()
    print(f"film: {film.relative_to(PROMO)} ({probe}); captions: {len(plates)} plates; "
          f"contact sheet: contact-sheet.png ({len(times)} frames)"
          + ("" if args.no_copy else "; copied to ~/Downloads"))


if __name__ == "__main__":
    main()
