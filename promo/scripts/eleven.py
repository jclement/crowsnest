"""ElevenLabs access for the promo: where the key comes from, and the two calls we make.

The key is looked for, in order, in ELEVENLABS_API_KEY, promo/.env (ELEVENLABS_API_KEY=...),
~/eleven.env and ~/Downloads/eleven.env (either name, or APIKEY=...). It is read into this
process only; it is never printed, logged or written anywhere.
"""
from __future__ import annotations

import os
from pathlib import Path

import httpx

API = "https://api.elevenlabs.io/v1"
PROMO = Path(__file__).resolve().parent.parent


def api_key() -> str | None:
    if os.environ.get("ELEVENLABS_API_KEY"):
        return os.environ["ELEVENLABS_API_KEY"].strip()
    for path in (PROMO / ".env", Path.home() / "eleven.env", Path.home() / "Downloads" / "eleven.env"):
        if not path.is_file():
            continue
        for line in path.read_text().splitlines():
            name, _, value = line.partition("=")
            if name.strip() in ("ELEVENLABS_API_KEY", "APIKEY") and value.strip():
                return value.strip().strip('"').strip("'")
    return None


def client() -> httpx.Client:
    key = api_key()
    if not key:
        raise SystemExit("no ElevenLabs key: set ELEVENLABS_API_KEY or put it in promo/.env (git ignores it)")
    return httpx.Client(base_url=API, headers={"xi-api-key": key}, timeout=180)


if __name__ == "__main__":
    # `uv run scripts/eleven.py`: the account's voices and the character budget left.
    with client() as c:
        sub = c.get("/user/subscription").json()
        print(f"characters: {sub.get('character_count')} used of {sub.get('character_limit')}")
        for v in c.get("/voices").json()["voices"]:
            labels = ", ".join(f"{k}={val}" for k, val in (v.get("labels") or {}).items())
            print(f"{v['voice_id']}  {v['name']:<40} {v.get('category','')}  {labels}")
