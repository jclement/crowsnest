// A fake Web Push service for the hermetic suite (push.spec.ts), and the browser side of a push
// subscription that the test controls.
//
// Headless Chromium cannot subscribe to a real push service (pushManager.subscribe needs Google's
// FCM connection), so the spec replaces PushManager.subscribe in the page with one that returns a
// subscription whose endpoint is this server, on loopback (--demo accepts http:// to loopback for
// exactly this), and whose keys the test made. The server then encrypts for those keys and POSTs
// here, and decrypt() opens it as the browser would (RFC 8291): an end-to-end proof that what
// crowsnest sends is readable by the subscriber, short of a real push service.
import { createDecipheriv, createECDH, createHmac, randomBytes, type ECDH } from "node:crypto";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";

export interface PushRequest {
  path: string;
  headers: Record<string, string | string[] | undefined>;
  body: Buffer;
}

export interface FakePush {
  /** The endpoint the fake subscription carries. */
  endpoint: string;
  /** p256dh and auth, base64url, as PushSubscription.toJSON() gives them. */
  keys: { p256dh: string; auth: string };
  requests: PushRequest[];
  /** Decrypts a request body with the subscription's private key; returns the plaintext. */
  decrypt: (body: Buffer) => string;
  close: () => Promise<void>;
}

const b64url = (bytes: Buffer) => bytes.toString("base64url");

export async function startFakePush(): Promise<FakePush> {
  const ecdh: ECDH = createECDH("prime256v1");
  ecdh.generateKeys();
  const auth = randomBytes(16);
  const requests: PushRequest[] = [];
  const server: Server = createServer((request, response) => {
    const chunks: Buffer[] = [];
    request.on("data", (chunk: Buffer) => chunks.push(chunk));
    request.on("end", () => {
      requests.push({ path: request.url ?? "", headers: request.headers, body: Buffer.concat(chunks) });
      response.writeHead(201).end();
    });
  });
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const { port } = server.address() as AddressInfo;
  return {
    endpoint: `http://127.0.0.1:${port}/push/e2e-device`,
    keys: { p256dh: b64url(ecdh.getPublicKey()), auth: b64url(auth) },
    requests,
    decrypt: (body) => decrypt(ecdh, auth, body),
    close: () => new Promise((resolve) => server.close(() => resolve())),
  };
}

const hmac = (key: Buffer, data: Buffer) => createHmac("sha256", key).update(data).digest();
/** HKDF-Expand for one block (every length here is at most 32 bytes). */
const expand = (prk: Buffer, info: Buffer, length: number) => hmac(prk, Buffer.concat([info, Buffer.from([1])])).subarray(0, length);

function decrypt(ecdh: ECDH, auth: Buffer, body: Buffer): string {
  const salt = body.subarray(0, 16);
  const idLength = body[20]!;
  const serverKey = body.subarray(21, 21 + idLength);
  const ciphertext = body.subarray(21 + idLength);
  const shared = ecdh.computeSecret(serverKey);
  const ikm = expand(hmac(auth, shared), Buffer.concat([Buffer.from("WebPush: info\0"), ecdh.getPublicKey(), serverKey]), 32);
  const prk = hmac(salt, ikm);
  const key = expand(prk, Buffer.from("Content-Encoding: aes128gcm\0"), 16);
  const nonce = expand(prk, Buffer.from("Content-Encoding: nonce\0"), 12);
  const decipher = createDecipheriv("aes-128-gcm", key, nonce);
  decipher.setAuthTag(ciphertext.subarray(ciphertext.length - 16));
  let plain = Buffer.concat([decipher.update(ciphertext.subarray(0, ciphertext.length - 16)), decipher.final()]);
  let end = plain.length;
  while (end > 0 && plain[end - 1] === 0) end--;
  if (plain[end - 1] !== 2) throw new Error("missing the last-record delimiter");
  plain = plain.subarray(0, end - 1);
  return plain.toString("utf8");
}

/**
 * A script for page.addInitScript: PushManager.subscribe and getSubscription return a subscription
 * with `endpoint` and `keys`, kept per page; unsubscribe drops it. Records the subscribe options on
 * window.__pushSubscribeOptions so the test can check the server key was passed.
 */
export function mockPushManagerScript(endpoint: string, keys: { p256dh: string; auth: string }): string {
  return `(() => {
    const endpoint = ${JSON.stringify(endpoint)};
    const keys = ${JSON.stringify(keys)};
    let current = null;
    const make = (options) => ({
      endpoint,
      expirationTime: null,
      options: { applicationServerKey: options.applicationServerKey ? new Uint8Array(options.applicationServerKey).buffer : null, userVisibleOnly: true },
      toJSON() { return { endpoint, expirationTime: null, keys }; },
      async unsubscribe() { current = null; return true; },
    });
    PushManager.prototype.subscribe = async function (options) {
      window.__pushSubscribeOptions = { userVisibleOnly: options.userVisibleOnly, keyLength: new Uint8Array(options.applicationServerKey).length };
      current = make(options);
      return current;
    };
    PushManager.prototype.getSubscription = async function () { return current; };
  })();`;
}
