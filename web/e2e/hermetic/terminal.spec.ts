// Terminals in the browser (DESIGN.md 10 and 12) against `crowsnest --demo`, whose terminal routes
// serve a built-in toy (line editing, `size`, `exit`; no process is spawned): it connects, typed
// input reaches it and its output comes back, a resize reaches it, it reconnects after the server
// goes away and comes back, the phone key bar sends the right bytes, and a ninth terminal is
// refused with the cap's message. The real herdr TUI and pane bridges are `mise run e2e:real`.
import type { Page, WebSocket as PageSocket } from "@playwright/test";
import { expect, isPhone, test } from "../support/fixtures";

const rows = (page: Page) => page.locator("[data-terminal] .xterm-rows");

async function openTerminal(page: Page) {
  await page.goto("/agents?session=true");
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
  await expect.poll(() => rows(page).innerText()).toContain("demo terminal");
}

async function run(page: Page, line: string) {
  await page.locator("[data-terminal]").click();
  await page.keyboard.type(line, { delay: 10 });
  await page.keyboard.press("Enter");
}

/** The size the toy last saw, from its `size` command. */
async function reportedSize(page: Page): Promise<{ cols: number; rows: number }> {
  await run(page, "size");
  let found: { cols: number; rows: number } | null = null;
  await expect
    .poll(async () => {
      const all = [...(await rows(page).innerText()).matchAll(/(\d+) columns x (\d+) rows/g)];
      const last = all.at(-1);
      found = last ? { cols: Number(last[1]), rows: Number(last[2]) } : null;
      return found !== null;
    })
    .toBe(true);
  return found as unknown as { cols: number; rows: number };
}

test("the terminal connects, echoes what is typed and follows a resize", async ({ page }) => {
  await openTerminal(page);
  await run(page, "hello crow");
  await expect.poll(() => rows(page).innerText()).toContain("you typed: hello crow");
  const before = await reportedSize(page);
  expect(before.cols).toBeGreaterThan(20);
  const viewport = page.viewportSize() as { width: number; height: number };
  await page.setViewportSize({ width: Math.round(viewport.width * 0.7), height: viewport.height });
  await expect.poll(async () => (await reportedSize(page)).cols, { timeout: 10_000 }).toBeLessThan(before.cols);
});

test("the terminal reconnects by itself after the server goes away and comes back", async ({ page, app }) => {
  test.slow();
  await openTerminal(page);
  await app.kill();
  // The terminal's own status line, and the app's offline banner (the event stream died too).
  await expect(page.getByRole("status").filter({ hasText: /^Connection lost\. Reconnecting in \d+ s$/ })).toBeVisible({ timeout: 10_000 });
  await expect(page.getByRole("status").filter({ hasText: /^Connection lost\. Reconnecting in \d+ s…/ })).toBeVisible();
  await app.start();
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible({ timeout: 40_000 });
  // A fresh redraw from the new server, and input works again.
  await run(page, "after the restart");
  await expect.poll(() => rows(page).innerText()).toContain("you typed: after the restart");
});

test("an exited terminal stays ended until Reconnect is pressed", async ({ page }) => {
  await openTerminal(page);
  await run(page, "exit");
  await expect(page.getByRole("status").filter({ hasText: "Ended: exited" })).toBeVisible();
  await page.waitForTimeout(2_000);
  await expect(page.getByRole("status").filter({ hasText: "Ended: exited" })).toBeVisible();
  await page.getByRole("button", { name: "Reconnect" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
});

test("the phone key bar sends Esc, Ctrl+C and the arrows as the right bytes", async ({ page }, testInfo) => {
  test.skip(!isPhone(testInfo), "the key bar is for phones and touch screens");
  const sent: string[] = [];
  page.on("websocket", (ws: PageSocket) => {
    ws.on("framesent", (frame) => {
      if (typeof frame.payload !== "string") sent.push(frame.payload.toString("latin1"));
      else if (!frame.payload.startsWith("{")) sent.push(frame.payload);
    });
  });
  await openTerminal(page);
  // Focus the terminal as a tap on it does; the bar must keep it there.
  await page.locator("[data-terminal]").click();
  const bar = page.getByRole("toolbar", { name: "Terminal keys" });
  await expect(bar).toBeVisible();
  const press = async (label: string, expected: string | RegExp) => {
    sent.length = 0;
    await bar.getByRole("button", { name: label, exact: true }).click();
    await expect.poll(() => sent.join("")).toMatch(expected instanceof RegExp ? expected : new RegExp(`^${expected.replace(/[[\]\\^$.|?*+()]/g, "\\$&")}$`));
  };
  await press("Esc (Escape)", "\x1b");
  await press("Tab", "\t");
  await press("Up", /^\x1b[[O]A$/);
  await press("Down", /^\x1b[[O]B$/);
  await press("Right", /^\x1b[[O]C$/);
  await press("Left", /^\x1b[[O]D$/);
  await press("Enter", "\r");
  // Sticky Ctrl: the next typed key goes out as its control code, then Ctrl lets go.
  const ctrl = bar.getByRole("button", { name: "Ctrl (applies to the next key)" });
  await ctrl.click();
  await expect(ctrl).toHaveAttribute("aria-pressed", "true");
  sent.length = 0;
  await page.keyboard.type("c");
  await expect.poll(() => sent.join("")).toBe("\x03");
  await expect(ctrl).toHaveAttribute("aria-pressed", "false");
  await expect.poll(() => rows(page).innerText()).toContain("^C");
  // Tapping the bar never takes focus from the terminal (or the phone keyboard would close).
  expect(await page.evaluate(() => document.activeElement?.classList.contains("xterm-helper-textarea"))).toBe(true);
});

test("a ninth terminal is refused with the cap's message and does not retry by itself", async ({ page, context }) => {
  // Eight sockets held open by another tab (each one starts a herdr process on a real server).
  const holder = await context.newPage();
  await holder.goto("/");
  const opened = await holder.evaluate(async () => {
    const sockets: WebSocket[] = [];
    (window as unknown as { held: WebSocket[] }).held = sockets;
    const url = `${location.origin.replace(/^http/, "ws")}/api/v1/term/tui`;
    await Promise.all(
      Array.from({ length: 8 }, () => new Promise<void>((resolve) => {
        const socket = new WebSocket(url);
        socket.onopen = () => {
          socket.send(JSON.stringify({ type: "resize", cols: 80, rows: 24 }));
          resolve();
        };
        socket.onerror = () => resolve();
        sockets.push(socket);
      })),
    );
    return sockets.filter((socket) => socket.readyState === WebSocket.OPEN).length;
  });
  expect(opened).toBe(8);
  await page.goto("/agents?session=true");
  await expect(page.getByRole("status").filter({ hasText: /too many terminals open \(8\)/ })).toBeVisible();
  await expect(page.getByRole("button", { name: "Reconnect" })).toBeVisible();
  // Closing one frees a slot; Reconnect then works.
  await holder.evaluate(() => (window as unknown as { held: WebSocket[] }).held[0]?.close());
  await page.waitForTimeout(500);
  await page.getByRole("button", { name: "Reconnect" }).click();
  await expect(page.getByRole("status").filter({ hasText: "Connected" })).toBeVisible();
});

test("a right click stays in the terminal; Shift+right click still gives the browser's menu", async ({ page }) => {
  await page.goto("/agents?session=true");
  await expect(rows(page)).toContainText("demo", { timeout: 15_000 });
  // The page's context menu is what a right click opens unless the event is cancelled.
  const prevented = (shiftKey: boolean) =>
    page.evaluate((shift) => {
      const target = document.querySelector("[data-terminal] .xterm-screen") ?? document.querySelector("[data-terminal]");
      const event = new MouseEvent("contextmenu", { bubbles: true, cancelable: true, button: 2, shiftKey: shift });
      target?.dispatchEvent(event);
      return event.defaultPrevented;
    }, shiftKey);
  expect(await prevented(false)).toBe(true);
  expect(await prevented(true)).toBe(false);
});
