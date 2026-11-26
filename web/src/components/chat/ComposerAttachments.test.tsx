// The composer's attachment states (DESIGN.md 8.6): a pasted screenshot uploads with progress
// and the send waits for it; a failed upload is retried; a removed one is cancelled; a file over
// the limit never uploads; a long paste becomes a file, with "paste as text" to undo; a dropped
// file becomes a chip.
import { act, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithClient, stubApi, type Call } from "../../test/render";
import { Composer } from "./Composer";

/** A scriptable XMLHttpRequest: the upload's progress and answer are played by the test. */
class FakeXHR {
  static all: FakeXHR[] = [];
  upload: { onprogress: ((event: { lengthComputable: boolean; loaded: number; total: number }) => void) | null } = { onprogress: null };
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  onabort: (() => void) | null = null;
  status = 0;
  statusText = "";
  responseText = "";
  withCredentials = false;
  url = "";
  headers: Record<string, string> = {};
  body: unknown;
  aborted = false;
  open(_method: string, url: string) {
    this.url = url;
  }
  setRequestHeader(name: string, value: string) {
    this.headers[name] = value;
  }
  send(body: unknown) {
    this.body = body;
    FakeXHR.all.push(this);
  }
  abort() {
    this.aborted = true;
    this.onabort?.();
  }
  progress(fraction: number) {
    act(() => this.upload.onprogress?.({ lengthComputable: true, loaded: fraction * 100, total: 100 }));
  }
  respond(status: number, body: unknown) {
    act(() => {
      this.status = status;
      this.responseText = JSON.stringify(body);
      this.onload?.();
    });
  }
}

let nextId = 0;
function stored(name: string, type = "image/png") {
  const id = `a_${String(++nextId).padStart(24, "0")}`;
  return { data: { id, name, type, size: 10, sha256: "", image: true, thumb: true, inline: true, origin: "human", created_at: "2026-11-26T10:00:00Z" } };
}

const sent = { data: { pane_id: "w1:p1", status: "working", queued: false, started: true } };
const limits = { max_bytes: 1000, max_files: 3 };

function setup() {
  const calls: Call[] = stubApi((call) => {
    if (call.path === "/auth/me") return { body: { data: { username: "", features: { attachments: limits } } } };
    if (call.method === "POST") return { body: sent };
    if (call.method === "DELETE") return { status: 204, body: null };
    return { body: { data: {} } };
  });
  renderWithClient(<Composer target="orchestrator" agentName="the orchestrator" state="idle" />);
  return calls;
}

function paste(target: HTMLElement, data: { files?: File[]; text?: string }) {
  fireEvent.paste(target, { clipboardData: { files: data.files ?? [], items: [], types: data.files ? ["Files"] : ["text/plain"], getData: () => data.text ?? "" } });
}

const posts = (calls: Call[]) => calls.filter((call) => call.method === "POST");

describe("Composer attachments", () => {
  beforeEach(() => {
    FakeXHR.all = [];
    vi.stubGlobal("XMLHttpRequest", FakeXHR);
    vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true, addEventListener: () => {}, removeEventListener: () => {} })));
    URL.createObjectURL = vi.fn(() => "blob:preview");
    URL.revokeObjectURL = vi.fn();
  });
  afterEach(() => vi.unstubAllGlobals());

  it("uploads a pasted screenshot with progress and holds the send until it is done", async () => {
    const calls = setup();
    const input = await screen.findByRole("textbox", { name: /Message to the orchestrator/ });
    await waitFor(() => expect(screen.getByRole("button", { name: "Attach files" })).toBeInTheDocument());
    paste(input, { files: [new File(["png"], "image.png", { type: "image/png" })] });

    const upload = FakeXHR.all[0]!;
    expect(upload.url).toBe("/api/v1/attachments");
    expect(upload.headers["X-Crowsnest"]).toBe("1");
    expect(decodeURIComponent(upload.headers["X-Filename"] ?? "")).toMatch(/^pasted-\d{8}-\d{6}\.png$/);
    upload.progress(0.4);
    expect(screen.getByRole("progressbar")).toHaveAttribute("aria-valuenow", "40");
    expect(screen.getByText("Waiting for uploads to finish before sending.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

    const answer = stored("pasted.png");
    upload.respond(201, answer);
    await waitFor(() => expect(screen.getByRole("button", { name: "Send" })).toBeEnabled());
    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    // A file alone is a send: no words needed.
    await userEvent.setup().click(screen.getByRole("button", { name: "Send" }));
    await waitFor(() => expect(posts(calls)).toHaveLength(1));
    expect(posts(calls)[0]?.body).toEqual({ text: "", force_over_draft: false, attachments: [answer.data.id] });
    await waitFor(() => expect(screen.queryByRole("list", { name: "Attachments" })).not.toBeInTheDocument());
  });

  it("retries a failed upload, and removing a running one cancels it", async () => {
    setup();
    const input = await screen.findByRole("textbox", { name: /Message to the orchestrator/ });
    await waitFor(() => expect(screen.getByRole("button", { name: "Attach files" })).toBeInTheDocument());
    paste(input, { files: [new File(["a"], "a.log", { type: "text/plain" }), new File(["b"], "b.log", { type: "text/plain" })] });
    const [first, second] = FakeXHR.all;
    first!.respond(507, { error: { code: "QUOTA_FULL", message: "Attachment storage is full." } });
    expect(await screen.findByRole("alert")).toHaveTextContent("Attachment storage is full.");
    expect(screen.getByText("Waiting for uploads to finish before sending.")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "Remove b.log" }));
    expect(second!.aborted).toBe(true);
    expect(screen.queryByText("b.log")).not.toBeInTheDocument();
    expect(screen.getByText("Retry or remove the failed file to send.")).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "Retry a.log" }));
    expect(FakeXHR.all).toHaveLength(3);
    FakeXHR.all[2]!.respond(201, stored("a.log", "text/plain"));
    await waitFor(() => expect(screen.queryByRole("alert")).not.toBeInTheDocument());
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
  });

  it("refuses a file over the limit without uploading it", async () => {
    setup();
    await waitFor(() => expect(screen.getByRole("button", { name: "Attach files" })).toBeInTheDocument());
    const picker = screen.getByTestId("attach-input");
    fireEvent.change(picker, { target: { files: [new File([new Uint8Array(1001)], "huge.bin")] } });
    expect(await screen.findByRole("alert")).toHaveTextContent("Larger than 1000 B");
    expect(FakeXHR.all).toHaveLength(0);
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();
  });

  it("turns a long paste into a file, and puts it back as text on request", async () => {
    setup();
    const input = await screen.findByRole("textbox", { name: /Message to the orchestrator/ });
    await waitFor(() => expect(screen.getByRole("button", { name: "Attach files" })).toBeInTheDocument());
    const log = Array.from({ length: 60 }, (_, i) => `line ${i}`).join("\n");
    paste(input, { text: log });
    expect(input).toHaveValue("");
    expect(FakeXHR.all).toHaveLength(1);
    expect(decodeURIComponent(FakeXHR.all[0]!.headers["X-Filename"] ?? "")).toMatch(/^pasted-.*\.txt$/);
    expect(await screen.findByText(/Long paste attached as pasted-/)).toBeInTheDocument();

    await userEvent.setup().click(screen.getByRole("button", { name: "Paste as text instead" }));
    expect(input).toHaveValue(log);
    expect(screen.queryByRole("list", { name: "Attachments" })).not.toBeInTheDocument();
    expect(FakeXHR.all[0]!.aborted).toBe(true);

    // A short paste is just text.
    paste(input, { text: "short" });
    expect(FakeXHR.all).toHaveLength(1);
  });

  it("takes a dropped file, with a drop target while dragging", async () => {
    setup();
    await waitFor(() => expect(screen.getByRole("button", { name: "Attach files" })).toBeInTheDocument());
    const form = screen.getByRole("form", { name: /Message the orchestrator/ });
    const file = new File(["%PDF"], "report.pdf", { type: "application/pdf" });
    fireEvent.dragEnter(form, { dataTransfer: { types: ["Files"], files: [file], items: [] } });
    expect(screen.getByTestId("drop-overlay")).toHaveTextContent("Drop files to attach");
    fireEvent.drop(form, { dataTransfer: { types: ["Files"], files: [file], items: [] } });
    expect(screen.queryByTestId("drop-overlay")).not.toBeInTheDocument();
    expect(screen.getByText("report.pdf")).toBeInTheDocument();
    expect(FakeXHR.all).toHaveLength(1);
  });

  it("shows no attachment controls when the server has them off", async () => {
    stubApi((call) => (call.path === "/auth/me" ? { body: { data: { username: "", features: {} } } } : { body: { data: {} } }));
    renderWithClient(<Composer target="orchestrator" agentName="the orchestrator" state="idle" />);
    await screen.findByRole("textbox", { name: /Message to the orchestrator/ });
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(screen.queryByRole("button", { name: "Attach files" })).not.toBeInTheDocument();
  });
});
