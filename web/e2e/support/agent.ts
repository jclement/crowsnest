// Playing the agents' side of a --demo instance from a test: asking a question over the agent
// API's unix socket (what `crowsnest ask` does), and appending Claude-shaped records to the demo
// orchestrator's transcript (what Claude does), so the Chat view gets as many messages as a test
// needs without waiting on the scripted replies. Also a raw HTTP request with a forged Host
// header, which fetch and Playwright's request context will not send.
import { appendFileSync, readdirSync } from "node:fs";
import { request as httpRequest } from "node:http";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { demoDir, type Instance } from "./instance";

interface RawResponse {
  status: number;
  headers: Record<string, string | string[] | undefined>;
  body: string;
}

/** One HTTP request over TCP or a unix socket, with exactly the headers given. */
export function rawRequest(options: {
  socketPath?: string;
  port?: number;
  method: string;
  path: string;
  headers?: Record<string, string>;
  body?: string;
}): Promise<RawResponse> {
  return new Promise((resolve, reject) => {
    const req = httpRequest(
      {
        socketPath: options.socketPath,
        host: options.socketPath ? undefined : "127.0.0.1",
        port: options.port,
        method: options.method,
        path: options.path,
        headers: options.headers,
        setHost: false,
      },
      (res) => {
        let body = "";
        res.setEncoding("utf8");
        res.on("data", (chunk: string) => (body += chunk));
        res.on("end", () => resolve({ status: res.statusCode ?? 0, headers: res.headers, body }));
      },
    );
    req.on("error", reject);
    if (options.body) req.write(options.body);
    req.end();
  });
}

/** Asks a question as an agent would (POST /api/v1/questions on the agent socket); returns its id. */
export async function askAsAgent(instance: Instance, question: Record<string, unknown>, paneId?: string): Promise<string> {
  const body = JSON.stringify(question);
  const headers: Record<string, string> = { "Content-Type": "application/json", Host: "crowsnest", "Content-Length": String(Buffer.byteLength(body)) };
  if (paneId) headers["X-Crowsnest-Pane"] = paneId;
  const response = await rawRequest({ socketPath: instance.agentSocket(), method: "POST", path: "/api/v1/questions", headers, body });
  if (response.status >= 300) throw new Error(`asking failed: ${response.status} ${response.body}`);
  return (JSON.parse(response.body) as { data: { id: string } }).data.id;
}

/** The demo orchestrator's Claude session (internal/herdr/fake/demo.go). */
const DEMO_ORCHESTRATOR_SESSION = "4d1f6c2a-0b7e-4a51-9a63-2f1d8c0e7b11";

/** The demo orchestrator's transcript file. */
function orchestratorTranscript(instance: Instance): string {
  const projects = join(demoDir(instance), "state", "demo-claude", "projects");
  for (const dir of readdirSync(projects)) {
    const file = join(projects, dir, `${DEMO_ORCHESTRATOR_SESSION}.jsonl`);
    if (readdirSync(join(projects, dir)).includes(`${DEMO_ORCHESTRATOR_SESSION}.jsonl`)) return file;
  }
  throw new Error("the demo orchestrator's transcript was not found");
}

/** Appends turns to the demo orchestrator's transcript: one user line and one reply each. */
export function appendTurns(instance: Instance, turns: Array<{ user: string; reply: string }>) {
  const file = orchestratorTranscript(instance);
  const lines: string[] = [];
  for (const turn of turns) {
    const base = () => ({ uuid: randomUUID(), timestamp: new Date().toISOString(), sessionId: DEMO_ORCHESTRATOR_SESSION, cwd: "~/.local/share/crowsnest/orchestrator", isSidechain: false });
    lines.push(JSON.stringify({ ...base(), type: "user", message: { role: "user", content: turn.user } }));
    lines.push(
      JSON.stringify({
        ...base(),
        type: "assistant",
        message: { id: `msg_e2e_${randomUUID().slice(0, 8)}`, role: "assistant", model: "claude-demo", content: [{ type: "text", text: turn.reply }] },
      }),
    );
  }
  appendFileSync(file, lines.join("\n") + "\n");
}
