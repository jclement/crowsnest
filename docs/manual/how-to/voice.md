# Talk and listen

With an ElevenLabs API key, crowsnest has **talk mode**: you speak to the orchestrator or a lead,
and they answer in a sentence you hear. Without a key nothing here appears. The key stays on the
server; the browser can only ask to hear texts the server holds.

## Turn it on

Start crowsnest with the key in the environment (not the flag, which `ps` shows), or from a file:

```sh
ELEVENLABS_API_KEY=… crowsnest
# or: crowsnest --elevenlabs-api-key-file ~/.config/crowsnest/elevenlabs (chmod 600)
```

The microphone needs a secure context (HTTPS or localhost): on a phone, use `tailscale serve`
([Reach it from your phone](phone.md)). Spoken replies work over plain HTTP too.

## Speak mode vs the mic

Two separate things, often confused:

| | What it does | Where |
|---|---|---|
| **The mic** | **You talk.** What you say is transcribed and sent as a message. | The mic button beside **Send** in Chat, and on Now's job box |
| **Speak mode** | **They talk.** Each new reply's one-line summary is spoken aloud, wherever you are in the app. | The speaker switch in the header (**Speak replies: on/off**), remembered per browser |

You can use either without the other: dictate with speak mode off, or listen with the mic unused.

## Talking: the mic

1. In Chat (to the orchestrator or a lead), **hold the mic** and speak; or tap it to lock
   recording on, and tap again to stop. On a desktop, **hold Space** anywhere outside a text field,
   or **Option+Space** (Ctrl+Space off a Mac) while typing in the box.
2. The elapsed time shows as you speak; a recording can run five minutes (a warning comes in the
   last 30 seconds).
3. Let go. The transcript shows for a moment with **Cancel**, then is sent, marked as voice.

The first press asks for microphone permission. Giving a job by voice from Now is described in
[Give a job](give-a-job.md#by-voice).

## Listening: speak mode

Every reply from a standing agent starts with `SAY: <one sentence>`; Chat shows that sentence as
the reply's caption with a play button. With speak mode on, each new reply's sentence (and
anything a job agent says with `crowsnest say`) is played, with a now-playing strip under the
header naming the speaker, with **Skip** and **Stop**. Lines queue, so two leads never talk at
once.

- **Per agent**: each agent's **Speak** switch in its Chat header chooses whose replies you hear.
  Job agents share one switch, **Job agents (say)**. Turning an agent on turns the header's switch
  on.
- **Voices**: account menu → **Settings** → **Voice** ([Open Voice settings](app:/settings#voice)): the instance's
  voice, a voice per agent (so you can tell leads apart by ear), the speed (0.8× to 1.5×) and the
  model (fast or higher quality).

Speech is made only when a browser asks to hear it, so with speak mode off nothing is spent.

## Sounds

With speak mode on, a short sound plays when the attention queue grows, so you hear that
something needs you without looking. The tray has its own **Arrival sound** switch.

## What can go wrong

- **The mic says it needs HTTPS**: you are on plain HTTP. Use `tailscale serve` or localhost.
- **Nothing is spoken**: is the header switch on, and is that agent's **Speak** switch on? A reply
  with `SAY: -` is deliberately silent.
- **iOS stops listening when the screen locks**: a web page cannot hold the microphone with the
  screen locked on iOS. For a long hands-free session, the Claude app's own voice mode on a Remote
  Control session may suit better ([Remote Control](remote-control.md)).
