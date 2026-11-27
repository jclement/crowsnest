"""The music bed: a quiet, original pad written in code, so there is nothing to license.

    uv run scripts/music.py            -> out/audio/bed.wav, as long as the film

A slow four-chord loop (Am, F, C, G; eight seconds a chord) of soft detuned pads, a low sine
root, and a sparse plucked arpeggio, low-passed and gentle, so it sits under the voice. A
fixed seed: the same film length gives the same bed, sample for sample. The mix ducks it under
the narration (assemble.py).
"""
from __future__ import annotations

import json
import wave
from pathlib import Path

import numpy as np

PROMO = Path(__file__).resolve().parent.parent
OUT = PROMO / "out"
RATE = 48000

# A minor, F, C, G (MIDI notes): voiced low and close so the bed stays out of the voice's range.
CHORDS = [[45, 57, 60, 64], [41, 53, 57, 60], [48, 55, 60, 64], [43, 55, 59, 62]]
CHORD_SECONDS = 8.0


def hz(note: float) -> float:
    return 440.0 * 2 ** ((note - 69) / 12)


def lowpass(x: np.ndarray, cutoff: float) -> np.ndarray:
    """A gentle 4th-order-style low-pass, applied in the frequency domain."""
    spectrum = np.fft.rfft(x)
    freqs = np.fft.rfftfreq(len(x), 1 / RATE)
    spectrum *= 1 / np.sqrt(1 + (freqs / cutoff) ** 8)
    return np.fft.irfft(spectrum, len(x))


def pad(seconds: float, rng: np.random.Generator) -> np.ndarray:
    n = int(seconds * RATE)
    t = np.arange(n) / RATE
    out = np.zeros(n)
    chord_n = int(CHORD_SECONDS * RATE)
    fade = int(1.6 * RATE)
    for k in range(int(np.ceil(seconds / CHORD_SECONDS)) + 1):
        start = k * chord_n - fade
        notes = CHORDS[k % len(CHORDS)]
        s0, s1 = max(0, start), min(n, start + chord_n + 2 * fade)
        if s0 >= s1:
            continue
        tt = t[s0:s1]
        local = np.arange(s1 - s0) + (s0 - start)
        env = np.clip(np.minimum(local / fade, (chord_n + 2 * fade - local) / fade), 0, 1)
        env = np.sin(env * np.pi / 2) ** 2
        seg = np.zeros(s1 - s0)
        for note in notes:
            for detune in (-0.06, 0.0, 0.07):
                f = hz(note + detune)
                phase = rng.uniform(0, 2 * np.pi)
                # A few harmonics with falling weight: a soft, string-like pad.
                seg += sum(np.sin(2 * np.pi * f * h * tt + phase * h) / (h ** 1.6) for h in (1, 2, 3, 4))
        # The root an octave down, as a pure sine.
        seg += 1.4 * np.sin(2 * np.pi * hz(notes[0] - 12) * tt)
        out[s0:s1] += seg * env
    return out


def plucks(seconds: float, rng: np.random.Generator) -> np.ndarray:
    n = int(seconds * RATE)
    out = np.zeros(n)
    step = CHORD_SECONDS / 8  # eighth of a chord: one pluck a second, some skipped
    length = int(1.8 * RATE)
    tt = np.arange(length) / RATE
    env = np.exp(-tt * 3.2) * (1 - np.exp(-tt * 300))
    i = 0
    while i * step < seconds:
        if rng.random() < 0.7:
            chord = CHORDS[int(i * step // CHORD_SECONDS) % len(CHORDS)]
            note = chord[1:][int(rng.integers(0, 3))] + 12
            f = hz(note)
            tone = (np.sin(2 * np.pi * f * tt) + 0.3 * np.sin(4 * np.pi * f * tt)) * env
            s = int(i * step * RATE)
            e = min(n, s + length)
            out[s:e] += tone[: e - s] * rng.uniform(0.6, 1.0)
        i += 1
    return out


def bed(seconds: float) -> np.ndarray:
    rng = np.random.default_rng(1729)
    p = pad(seconds, rng)
    p = p / (np.abs(p).max() + 1e-9)
    k = plucks(seconds, rng)
    k = k / (np.abs(k).max() + 1e-9)
    mix = 0.75 * p + 0.22 * k
    mix = lowpass(mix, 2400.0)
    # A slow swell in, and a fade out over the last four seconds.
    n = len(mix)
    t = np.arange(n) / RATE
    mix *= np.clip(t / 3.0, 0, 1) * np.clip((seconds - t) / 4.0, 0, 1)
    # Stereo: a little width from a short delay on one side.
    d = int(0.011 * RATE)
    left = mix
    right = np.concatenate([np.zeros(d), mix[:-d]])
    stereo = np.stack([left, right], axis=1)
    return stereo / (np.abs(stereo).max() + 1e-9) * 0.5


def write_wav(path: Path, data: np.ndarray) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    pcm = (np.clip(data, -1, 1) * 32767).astype("<i2")
    with wave.open(str(path), "wb") as w:
        w.setnchannels(pcm.shape[1] if pcm.ndim > 1 else 1)
        w.setsampwidth(2)
        w.setframerate(RATE)
        w.writeframes(pcm.tobytes())


def main() -> None:
    seconds = json.loads((OUT / "timeline.json").read_text())["duration"]
    write_wav(OUT / "audio" / "bed.wav", bed(seconds))
    print(f"bed: {seconds:.1f} s -> out/audio/bed.wav")


if __name__ == "__main__":
    main()
