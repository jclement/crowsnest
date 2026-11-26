// A fake ElevenLabs API for the voice Playwright spec (test harness only; crowsnest is pointed at
// it with CROWSNEST_ELEVENLABS_URL). It answers the endpoints crowsnest calls:
//
//   POST /v1/speech-to-text                    multipart (model_id, file) → {"text": TRANSCRIPT}
//   POST /v1/text-to-speech/{voice}/stream     JSON {text, model_id, voice_settings} → a short WAV tone
//   GET  /v2/voices                            two voices, in the real API's shape
//
// and refuses any call without the expected xi-api-key. GET /__requests lists what it received
// (paths, the key header, the uploaded file's type and size, TTS text), so the spec can assert
// crowsnest sent the key upstream and what it asked for. The WAV is honest about its type
// (audio/wav); crowsnest passes an upstream audio/* type through.
import { createServer } from "node:http";

const port = Number(process.env.FAKE_ELEVENLABS_PORT ?? 27699);
const key = process.env.FAKE_ELEVENLABS_KEY ?? "sk_fake_e2e_key";
const transcript = process.env.FAKE_ELEVENLABS_TRANSCRIPT ?? "what is the status";
const requests = [];

/** 0.4 s of a quiet 440 Hz tone, 8 kHz mono 16-bit PCM WAV: real audio Chromium can play to the end. */
function tone() {
  const rate = 8000;
  const samples = Math.floor(rate * 0.4);
  const buffer = Buffer.alloc(44 + samples * 2);
  buffer.write("RIFF", 0);
  buffer.writeUInt32LE(36 + samples * 2, 4);
  buffer.write("WAVEfmt ", 8);
  buffer.writeUInt32LE(16, 16);
  buffer.writeUInt16LE(1, 20);
  buffer.writeUInt16LE(1, 22);
  buffer.writeUInt32LE(rate, 24);
  buffer.writeUInt32LE(rate * 2, 28);
  buffer.writeUInt16LE(2, 32);
  buffer.writeUInt16LE(16, 34);
  buffer.write("data", 36);
  buffer.writeUInt32LE(samples * 2, 40);
  for (let i = 0; i < samples; i++) buffer.writeInt16LE(Math.round(Math.sin((2 * Math.PI * 440 * i) / rate) * 3000), 44 + i * 2);
  return buffer;
}

/** The uploaded file part's Content-Type and size, from a multipart body. */
function filePart(body, contentType) {
  const boundary = /boundary=([^;]+)/.exec(contentType ?? "")?.[1];
  if (!boundary) return {};
  const text = body.toString("latin1");
  for (const part of text.split("--" + boundary)) {
    if (!part.includes('name="file"')) continue;
    const [head, ...rest] = part.split("\r\n\r\n");
    return { fileType: /Content-Type: ([^\r\n]+)/i.exec(head)?.[1], fileBytes: rest.join("\r\n\r\n").length - 2 };
  }
  return {};
}

createServer((req, res) => {
  const chunks = [];
  req.on("data", (chunk) => chunks.push(chunk));
  req.on("end", () => {
    const body = Buffer.concat(chunks);
    if (req.method === "GET" && req.url === "/__requests") {
      res.writeHead(200, { "Content-Type": "application/json" });
      res.end(JSON.stringify(requests));
      return;
    }
    if (req.method === "GET" && req.url === "/health") {
      res.writeHead(200).end("ok");
      return;
    }
    const record = { method: req.method, path: req.url, keyOk: req.headers["xi-api-key"] === key, keyPresent: !!req.headers["xi-api-key"] };
    if (req.url === "/v1/speech-to-text") {
      Object.assign(record, filePart(body, req.headers["content-type"]), { model: /name="model_id"\r\n\r\n([^\r\n]+)/.exec(body.toString("latin1"))?.[1] });
    } else {
      try {
        record.json = JSON.parse(body.toString("utf8") || "null");
      } catch {
        record.json = null;
      }
    }
    requests.push(record);
    if (!record.keyOk) {
      res.writeHead(401, { "Content-Type": "application/json" }).end('{"detail":{"status":"invalid_api_key"}}');
      return;
    }
    if (req.method === "POST" && req.url === "/v1/speech-to-text") {
      res.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ language_code: "en", text: transcript, words: [] }));
      return;
    }
    if (req.method === "GET" && (req.url ?? "").startsWith("/v2/voices")) {
      const voices = [
        { voice_id: "21m00Tcm4TlvDq8ikWAM", name: "Rachel", category: "premade", labels: { accent: "american", gender: "female", age: "young" } },
        { voice_id: "nPczCjzI2devNBz1zQrb", name: "Brian", category: "premade", labels: { accent: "american", gender: "male", age: "middle_aged" } },
      ];
      res.writeHead(200, { "Content-Type": "application/json" }).end(JSON.stringify({ voices, has_more: false, next_page_token: null }));
      return;
    }
    if (req.method === "POST" && /^\/v1\/text-to-speech\/[^/]+\/stream/.test(req.url ?? "")) {
      res.writeHead(200, { "Content-Type": "audio/wav" }).end(tone());
      return;
    }
    res.writeHead(404).end();
  });
}).listen(port, "127.0.0.1");
