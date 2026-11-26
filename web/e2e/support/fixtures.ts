// The hermetic suite's Playwright fixtures (`mise run e2e`, playwright.config.ts). Every test
// gets its own `crowsnest --demo` on a random port (`app`), started in about a third of a second,
// so tests never see each other's answers, jobs or messages and can run in parallel; `baseURL`
// points at it. A spec changes what it gets with `test.use({ appOptions: … })`: the login on, or
// talk mode against the fake ElevenLabs.
//
// Also the checks every view shares: no sideways scroll, 44 px tap targets, and axe.
import AxeBuilder from "@axe-core/playwright";
import { test as base, expect, type Page, type TestInfo } from "@playwright/test";
import { startInstance, type Instance, type InstanceOptions } from "./instance";

export { expect };

/** The login the auth specs turn on. Test-only; not a secret. */
export const TEST_LOGIN = { username: "crow", password: "e2e-correct-horse-battery" } as const;

interface Fixtures {
  appOptions: InstanceOptions;
  app: Instance;
}

export const test = base.extend<Fixtures>({
  appOptions: [{ demo: true }, { option: true }],
  app: async ({ appOptions }, use, testInfo) => {
    const instance = await startInstance(appOptions);
    await use(instance);
    if (testInfo.status !== testInfo.expectedStatus) await testInfo.attach("crowsnest.log", { body: instance.log(), contentType: "text/plain" });
    await instance.stop();
  },
  baseURL: async ({ app }, use) => use(app.baseURL),
});

/** True in the phone projects (375 px wide; the sheet and bottom-nav layouts). */
export function isPhone(testInfo: TestInfo): boolean {
  return testInfo.project.name.startsWith("mobile");
}

/** The detail region of a list/detail view: a full-screen sheet on a phone, a panel from md. */
export function detailRegion(page: Page, testInfo: TestInfo, panelName: string) {
  return isPhone(testInfo) ? page.getByRole("dialog") : page.getByRole("region", { name: panelName });
}

/** The page never scrolls sideways. */
export async function expectNoSidewaysScroll(page: Page) {
  const overflow = await page.evaluate(() => document.documentElement.scrollWidth - document.documentElement.clientWidth);
  expect(overflow, "the page scrolls sideways").toBeLessThanOrEqual(0);
}

/**
 * Interactive elements under `scope` smaller than 44 px in either direction, described for the
 * failure message. Diff tables (full-width rows) and xterm's hidden helper textarea are skipped.
 */
export async function smallTapTargets(page: Page, scope = "body"): Promise<string[]> {
  return page.evaluate((selector) => {
    const root = document.querySelector(selector) ?? document.body;
    return [...root.querySelectorAll<HTMLElement>("button, a[href], input:not([type=hidden]), select, textarea, [role=tab], [role=menuitem]")]
      .filter((element) => {
        const box = element.getBoundingClientRect();
        if (box.width === 0 || box.height === 0) return false;
        if (element.closest("table, .xterm, [aria-hidden=true]")) return false;
        const style = getComputedStyle(element);
        if (style.visibility === "hidden" || style.opacity === "0") return false;
        return box.width < 43.5 || box.height < 43.5;
      })
      .map((element) => `${element.tagName.toLowerCase()} "${(element.getAttribute("aria-label") ?? element.textContent ?? "").trim().slice(0, 40)}" ${Math.round(element.getBoundingClientRect().width)}x${Math.round(element.getBoundingClientRect().height)}`);
  }, scope);
}

/** No serious or critical axe violations on the page as it stands. */
export async function expectAccessible(page: Page, testInfo: TestInfo, what: string) {
  // A sheet sliding in or a toast fading is half-transparent: let animations finish first, or axe
  // measures the contrast of a blend that no one reads.
  await page.waitForFunction(() => document.getAnimations().every((animation) => animation.playState !== "running" || animation.effect?.getTiming().iterations === Infinity));
  const results = await new AxeBuilder({ page })
    // xterm draws its own grid of spans and a hidden textarea; axe's rules do not fit a terminal
    // emulator (its accessibility comes from the screen-reader mode, not the visible rows).
    .exclude(".xterm")
    .analyze();
  const bad = results.violations.filter((violation) => violation.impact === "serious" || violation.impact === "critical");
  if (bad.length > 0) await testInfo.attach(`axe-${what}.json`, { body: JSON.stringify(bad, null, 2), contentType: "application/json" });
  expect(
    bad.map((violation) => `${violation.id} (${violation.impact}): ${violation.help} — ${violation.nodes.map((node) => node.target.join(" ")).slice(0, 5).join(" | ")}`),
    `axe on ${what}`,
  ).toEqual([]);
}

/** The headers a same-origin non-GET call needs (DESIGN.md 4.4). */
export function sameOriginHeaders(app: Instance): Record<string, string> {
  return { Origin: app.baseURL, "X-Crowsnest": "1", "Content-Type": "application/json" };
}

/**
 * Waits until the demo orchestrator has taken the job seeded for it (j_10, queued with no
 * directory) and finished that turn. Since standing agents take their waiting job as soon as a
 * lead reports ready, the orchestrator starts a 2.5 s turn of its own a moment after start-up;
 * a test that times its own sends (queued while working, never scrolls while held) must not
 * overlap it.
 */
export async function waitForOrchestratorSettled(page: Page) {
  await expect
    .poll(
      () =>
        page.evaluate(() =>
          fetch("/api/v1/jobs")
            .then((response) => response.json())
            .then((body: { data: Array<{ dir: string; project_id?: number; state: string; agent_name: string }> }) =>
              body.data.filter((job) => !job.dir && !job.project_id && job.state === "queued" && !job.agent_name).length,
            ),
        ),
      { timeout: 15_000 },
    )
    .toBe(0);
  await expect(page.getByTestId("chat-state")).toHaveText("Idle", { timeout: 15_000 });
  // The turn's reply lands in the transcript just after the state goes idle.
  await page.waitForTimeout(500);
}
