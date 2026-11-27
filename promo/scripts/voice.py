"""Narration: one ElevenLabs clip per line of script.toml, cached, and the film's timeline.

    uv run scripts/voice.py            perform the lines that have no cached clip, write out/timeline.json
    uv run scripts/voice.py --fake     no API: silent clips timed at a narrator's pace (for pipeline work)
    uv run scripts/voice.py --prune    also delete cached clips no line uses

A clip is cached in voice/<line id>.<hash>.mp3, the hash covering the spoken text, the voice,
the model and the voice settings: editing one line re-performs that line only, and nothing else
ever calls the API. The timeline places every line in the film (scene starts, line starts and
lengths); the recorder paces each scene by it and the assembly mixes and captions by it.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import subprocess
import sys
import tomllib
from pathlib import Path

PROMO = Path(__file__).resolve().parent.parent
VOICE = PROMO / "voice"
OUT = PROMO / "out"
FAKE = OUT / "fake-voice"


def load_script() -> dict:
    return tomllib.loads((PROMO / "script.toml").read_text())


def lines_of(scene: dict) -> list[dict]:
    """A scene's lines as {id, text, say}: a plain string is both caption and speech."""
    out = []
    for i, line in enumerate(scene.get("lines", [])):
        if isinstance(line, str):
            line = {"text": line}
        out.append({"id": f"{scene['id']}.{i + 1}", "text": line["text"], "say": line.get("say", line["text"]),
                    "after": line.get("after", 0.0)})
    return out


def fingerprint(film: dict, say: str) -> str:
    payload = [say, film["voice_id"], film["model"], film["stability"], film["similarity_boost"],
               film["style"], film["output_format"]]
    return hashlib.sha256(json.dumps(payload).encode()).hexdigest()[:12]


def duration(path: Path) -> float:
    out = subprocess.run(["ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", str(path)],
                         capture_output=True, text=True, check=True).stdout
    return float(out.strip())


def perform(film: dict, say: str) -> bytes:
    sys.path.insert(0, str(PROMO / "scripts"))
    from eleven import client  # imported here so --fake and a full cache never look for a key

    body = {"text": say, "model_id": film["model"],
            "voice_settings": {"stability": film["stability"], "similarity_boost": film["similarity_boost"],
                               "style": film["style"], "use_speaker_boost": True}}
    with client() as c:
        r = c.post(f"/text-to-speech/{film['voice_id']}", json=body, params={"output_format": film["output_format"]})
    if r.status_code != 200:
        raise SystemExit(f"ElevenLabs {r.status_code}: {r.text[:300]}")
    return r.content


def fake_clip(say: str, path: Path) -> None:
    seconds = max(1.0, len(say.split()) / 150 * 60)
    path.parent.mkdir(parents=True, exist_ok=True)
    subprocess.run(["ffmpeg", "-v", "error", "-y", "-f", "lavfi", "-i", f"anullsrc=r=44100:cl=mono",
                    "-t", f"{seconds:.2f}", "-c:a", "libmp3lame", "-b:a", "64k", str(path)], check=True)


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--fake", action="store_true")
    ap.add_argument("--prune", action="store_true")
    args = ap.parse_args()
    script = load_script()
    film = script["film"]
    directory = FAKE if args.fake else VOICE
    directory.mkdir(parents=True, exist_ok=True)

    performed = cached = chars = 0
    used: set[Path] = set()
    scenes = []
    t = 0.0
    for scene in script["scene"]:
        lines = []
        offset = film["lead"] if scene.get("lines") else 0.0
        for line in lines_of(scene):
            clip = directory / f"{line['id']}.{fingerprint(film, line['say'])}.mp3"
            used.add(clip)
            if not clip.exists():
                if args.fake:
                    fake_clip(line["say"], clip)
                else:
                    print(f"  performing {line['id']}: {line['say']}", flush=True)
                    data = perform(film, line["say"])
                    clip.with_suffix(".part").write_bytes(data)
                    clip.with_suffix(".part").replace(clip)
                    chars += len(line["say"])
                performed += 1
            else:
                cached += 1
            length = duration(clip)
            lines.append({**line, "clip": str(clip.relative_to(PROMO)), "offset": round(offset, 3),
                          "start": round(t + offset, 3), "duration": round(length, 3)})
            offset += length + film["gap"] + line["after"]
        if lines:
            offset += film["tail"] - film["gap"]
        length = max(offset, scene.get("min", 0.0))
        scenes.append({"id": scene["id"], "kind": scene["kind"], "start": round(t, 3), "duration": round(length, 3),
                       "lines": lines, **{k: v for k, v in scene.items() if k not in ("id", "kind", "lines", "min")}})
        t += length

    OUT.mkdir(exist_ok=True)
    timeline = {"title": film["title"], "fps": film["fps"], "duration": round(t, 3), "fake_voice": args.fake,
                "scenes": scenes}
    (OUT / "timeline.json").write_text(json.dumps(timeline, indent=2) + "\n")
    if args.prune:
        for stale in sorted(set(directory.glob("*.mp3")) - used):
            stale.unlink()
            print(f"  pruned {stale.name}")
    words = sum(len(l["say"].split()) for s in scenes for l in s["lines"])
    print(f"{cached} line(s) cached, {performed} {'faked' if args.fake else 'performed'}"
          f"{f' ({chars} characters sent to ElevenLabs)' if chars else ''}; "
          f"{words} words; film {t:.1f} s ({int(t // 60)}:{t % 60:04.1f})")


if __name__ == "__main__":
    main()
