# The crowsnest walkthrough film

A narrated, captioned product tour (just under four minutes), recorded from the real app
running in `--demo`, and rebuilt from this folder.

```sh
mise run setup && mise run promo:setup   # once
mise run promo                           # build, voice, record, music, assemble
```

The film lands in `out/crowsnest-tour.mp4` (H.264 + AAC, 1920x1080, 30 fps), with
`out/crowsnest-tour.srt` beside it; a copy goes to `~/Downloads/crowsnest-tour.mp4`.
`contact-sheet.png` (committed) is a frame or two from every scene, for reviewing without playing
anything; `crowsnest-tour.srt` here is the committed copy of the captions.

## What is where

| File | What it is |
|---|---|
| `script.toml` | The film: every scene's narration lines, what the picture shows, and its timing |
| `recorder/scenes.mjs` | What happens on screen in each recorded scene, paced on the narration |
| `recorder/record.mjs` | Starts the demo, drives Chromium, captures the screencast, lays it on a 30 fps clock |
| `recorder/stage.mjs` | The visible cursor, click and tap ripples, and the spotlight drawn over the app |
| `recorder/cards.mjs` | Title, section, install and closing cards; caption plates; the phone frame |
| `recorder/server.mjs` | `./bin/crowsnest --demo --no-auth` on a free port, all state in a throwaway directory |
| `scripts/voice.py` | ElevenLabs narration, cached per line, and `out/timeline.json` |
| `scripts/music.py` | The music bed, synthesised in code (nothing to license) |
| `scripts/assemble.py` | Mix with ducking, burned-in captions, encode, `.srt`, contact sheet |
| `voice/` | The approved narration clips (MP3), one per line; committed, about 2 MB |

## Stages

| Task | Does |
|---|---|
| `mise run promo:voice` | Performs only lines with no cached clip, then writes the timeline. `-- --fake` makes silent stand-ins with no key |
| `mise run promo:record [-- give nudge]` | Records all scenes, or the named ones (the rest keep their clips) |
| `mise run promo:music` | The bed, as long as the film |
| `mise run promo:assemble [-- --no-copy]` | The film, the captions, the contact sheet |

These are heavy (Chromium and ffmpeg): on Jeff's machines run them as `heavy mise run …`.

## Changing it

- **A line's words**: edit it in `script.toml`, run `promo:voice` (only that line is performed
  again, a few hundred characters), then `promo:record` for that scene (its length may change)
  and `promo:assemble`. A respelling for the voice goes in `say`; the caption keeps `text`.
- **Timing**: `after = 1.5` on a line holds the picture that long after it; `min` on a scene sets
  a floor. Neither re-performs anything.
- **What the picture does**: the scene's function in `recorder/scenes.mjs`. It waits for
  narration with `ctx.line(i, plus)`; `ctx.click`, `ctx.type`, `ctx.spot` (dim everything else),
  `ctx.scrollTo`. Run `PROMO_DEBUG=1 node recorder/record.mjs <scene>` to see when each click
  lands; a step still running when the scene ends is reported as "Target page … closed".
- **The voice**: `voice_id`, `model` and the settings in `[film]`. Changing any of them performs
  every line again (about 2,200 characters).

## The ElevenLabs key

Needed only when a line has no cached clip. It is read, in order, from `ELEVENLABS_API_KEY`,
`promo/.env` (`ELEVENLABS_API_KEY=…`, ignored by git), `~/eleven.env` or `~/Downloads/eleven.env`
(`APIKEY=…` also accepted), into the one process that calls the API. It is never printed or
written anywhere. `uv run scripts/eleven.py` lists the account's voices and characters left.

## What is real and what is drawn

Every app shot is the real web app served by `./bin/crowsnest --demo` (the in-memory fake herdr
and its seeded fleet, jobs, questions and approvals); talk mode runs against the e2e suite's fake
ElevenLabs, so it costs nothing. The recorder adds only a cursor, ripples and spotlights on top,
and rewrites the demo's throwaway state paths (`/tmp/cnpromo-…`) to `~/src/…` in visible text.
The cards, captions and phone frame are drawn here in the app's own palette and fonts.
