// What happens on screen in each recorded scene of script.toml, by scene id.
//
// setup(ctx) runs before the capture starts (navigation, anything the viewer should not watch);
// run(ctx) is the recording. run waits on the narration with ctx.line(i, plus), which resolves when
// line i starts (plus seconds); the capture stops at the scene's length whatever run is doing.
// Flags: voice (talk mode against a fake ElevenLabs), folded: false (start with the needs-you
// tray open; it starts folded so it does not cover the page).
//
// Everything here drives the real app (./bin/crowsnest --demo) with the locators its own e2e
// suite uses; nothing on screen is mocked.

const card = (page, name) => page.getByRole("button", { name }).first();
const nav = (page, name) => page.getByRole("link", { name: new RegExp(name) }).filter({ visible: true }).first();
const account = (page) => page.getByRole("button", { name: "Account" }).filter({ visible: true }).first();

export const SCENES = {
  // ---- 1. Give it a job ---------------------------------------------------------------------
  give: {
    async setup({ page, base }) {
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.1);
      await ctx.type(page.getByLabel("What needs doing?"), "Add a health check endpoint to the webhook sender", 26);
      await ctx.line(2, 0.0);
      await ctx.click(page.getByRole("radiogroup", { name: "Project" }).getByRole("radio", { name: /Crowsnest/ }));
      await ctx.wait(0.4);
      await ctx.click(page.getByRole("radio", { name: "As a job" }));
      await ctx.wait(0.3);
      await ctx.spot(page.getByText(/^A job agent in/), { pad: 6 });
      await ctx.wait(0.9);
      await ctx.unspot();
      await ctx.click(page.getByRole("button", { name: "Give job" }));
      await ctx.wait(0.8);
      const fresh = card(page, /Add a health check endpoint/);
      await ctx.spot(fresh, { pad: 4 });
      await ctx.line(3, -0.2);
      await ctx.unspot();
      await ctx.click(page.getByRole("radiogroup", { name: "Project" }).getByRole("radio", { name: /^All/ }));
      await ctx.wait(0.4);
      await ctx.spot(page.getByRole("region", { name: "Needs you or stuck" }), { pad: 2 });
      await ctx.wait(1.6);
      await ctx.scrollTo(page.getByRole("region", { name: "In progress" }), 0.1, 900);
      await ctx.spot(page.getByRole("region", { name: "In progress" }), { pad: 2 });
      await ctx.wait(1.4);
      await ctx.scrollTo(page.getByRole("region", { name: "Finished today" }), 0.4, 900);
      await ctx.spot(page.getByRole("region", { name: "Finished today" }), { pad: 2 });
    },
  },

  // ---- 2. It never silently stops ----------------------------------------------------------
  nudge: {
    async setup({ page, base }) {
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.3);
      await ctx.click(card(page, /Tidy the README install section/));
      await ctx.wait(0.7);
      await ctx.spot(page.getByText(/^\d+ nudges$/).last(), { pad: 6 });
      await ctx.line(1, 3.6);
      await ctx.unspot();
      const timeline = page.getByRole("list", { name: "Timeline" });
      await ctx.scrollTo(timeline, 0.25, 1100);
      await ctx.line(2, 0);
      await ctx.spot(await timeline.getByRole("listitem").filter({ hasText: /Nudged/ }).all(), { pad: 6 });
    },
  },

  stuck: {
    async setup({ page, base }) {
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.2);
      await ctx.click(card(page, /Migrate the settings page/));
      await ctx.wait(0.6);
      const retry = page.getByRole("button", { name: "Retry now" });
      await ctx.spot(page.locator("div").filter({ has: retry, hasText: "usage or rate limit" }).last(), { pad: 4 });
      await ctx.move(retry, 700);
      await ctx.line(1, 3.9);
      await ctx.unspot();
      await ctx.click(card(page, /Profile the fleet cache/));
      await ctx.wait(0.6);
      const nudge = page.getByRole("button", { name: "Nudge", exact: true });
      await ctx.spot(page.locator("div").filter({ has: nudge, hasText: "Stopped three times" }).last(), { pad: 4 });
      await ctx.move(nudge, 700);
    },
  },

  // ---- 3. It asks without stopping ---------------------------------------------------------
  question: {
    async setup({ page, base }) {
      await page.goto(`${base}/inbox`);
      await page.getByText("Apply the fleet cache refactor?").first().waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.4);
      await ctx.click(page.getByText("Apply the fleet cache refactor?").first());
      await ctx.line(2, 0.1);
      const diagram = page.locator("main svg[id^='mermaid'], main .mermaid svg, [aria-label*='iagram'] svg").first();
      await ctx.spot(diagram.locator("xpath=..")).catch(() => {});
      await ctx.wait(1.6);
      await ctx.unspot();
      const diff = page.getByText("internal/fleet/cache.go").last();
      await ctx.scrollTo(diff, 0.25, 1000);
      await ctx.spot(diff.locator("xpath=ancestor::*[self::section or self::figure or self::div][2]"), { pad: 4 });
    },
  },

  approval: {
    async setup({ page, base }) {
      await page.goto(`${base}/inbox`);
      await page.getByText("Bash: git push --force origin main").first().waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.2);
      await ctx.click(page.getByText("Bash: git push --force origin main").first());
      await ctx.wait(0.5);
      await ctx.spot([page.getByText("reviewer wants to run a command"), page.getByText(/Force push: rewrites/)], { pad: 10 });
      await ctx.move(page.getByText(/Force push: rewrites/), 800);
      await ctx.line(2, -0.3);
      await ctx.unspot();
      await ctx.click(page.getByText(/Bash: go test/).first());
      await ctx.wait(0.4);
      await ctx.move(page.getByPlaceholder("e.g. use printf instead"), 500);
      await ctx.wait(0.3);
      await ctx.click(page.getByRole("button", { name: /Allow once/ }));
    },
  },

  tray: {
    async setup({ page, base }) {
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.0);
      await ctx.click(page.getByRole("button", { name: /Needs you/ }).last());
      await ctx.wait(0.7);
      await ctx.spot(page.getByRole("list", { name: "Waiting on you" }).filter({ visible: true }).first().locator("xpath=..")).catch(() => {});
      await ctx.wait(1.3);
      await ctx.unspot();
      await ctx.click(nav(page, "Fleet"));
      await ctx.wait(0.9);
      await ctx.spot(page.getByRole("list", { name: "Waiting on you" }).filter({ visible: true }).first().locator("xpath=..")).catch(() => {});
    },
  },

  // ---- 4. Many projects --------------------------------------------------------------------
  projects: {
    async setup({ page, base }) {
      await page.goto(`${base}/projects`);
      await page.getByRole("list", { name: "Projects" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.2);
      await ctx.click(page.getByRole("list", { name: "Projects" }).getByText("Crowsnest", { exact: true }));
      await ctx.wait(0.7);
      await ctx.spot(page.getByText(/The lead is a standing Claude/).locator("xpath=ancestor::section[1]")).catch(() => {});
      await ctx.wait(1.6);
      await ctx.unspot();
      await ctx.line(2, -0.2);
      await ctx.click(nav(page, "Chat"));
      await ctx.wait(0.6);
      const picker = page.getByLabel("Chat with", { exact: true });
      await ctx.move(picker, 500);
      await ctx.wait(0.2);
      await page.mouse.down();
      await page.mouse.up();
      await picker.selectOption({ label: /crowsnest/i }).catch(async () => {
        const options = await picker.locator("option").allTextContents();
        const lead = options.find((o) => /lead|crowsnest/i.test(o) && !/orchestrator/i.test(o));
        if (lead) await picker.selectOption({ label: lead });
      });
      await ctx.line(3, 0.1);
      await page.keyboard.press("Meta+k");
      await ctx.wait(0.4);
      for (const ch of "fleet cache") { await page.keyboard.type(ch); await ctx.wait(0.07); }
      await ctx.wait(0.7);
      await ctx.click(page.getByRole("dialog").getByText("Profile the fleet cache").last());
    },
  },

  // ---- 5. From your phone ------------------------------------------------------------------
  phone: {
    async setup({ page, base }) {
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.3);
      await ctx.scrollTo(page.getByRole("region", { name: "In progress" }), 0.2, 1000);
      await ctx.wait(0.4);
      await ctx.tap(nav(page, "Inbox"));
      await ctx.wait(0.7);
      await ctx.tap(page.getByText("Where should the release notes live?").first());
      await ctx.wait(0.9);
      await ctx.tap(page.getByRole("button", { name: /CHANGELOG\.md in the repo/ }));
      await ctx.wait(0.9);
      await ctx.tap(page.getByText(/Bash: go test/).first());
      await ctx.wait(0.8);
      await ctx.tap(page.getByRole("button", { name: /Allow once/ }));
      await ctx.wait(1.4);
      await ctx.tap(page.getByRole("button", { name: "Close" }).filter({ visible: true }).first());
      await ctx.line(2, -0.6);
      await ctx.tap(account(page));
      await ctx.wait(0.5);
      await ctx.tap(page.getByRole("menuitem", { name: "Notifications" }));
    },
  },

  talk: {
    voice: true,
    async setup({ page, base }) {
      await page.goto(`${base}/chat`);
      await page.getByRole("list", { name: "Conversation" }).waitFor();
      await page.waitForTimeout(800);
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.2);
      const mic = page.getByRole("button", { name: /talk|record|microphone/i }).last();
      const c = await mic.boundingBox();
      const x = c.x + c.width / 2, y = c.y + c.height / 2;
      await page.mouse.move(x, y);
      await page.mouse.down();
      for (let i = 0; i < 6; i++) { await page.evaluate(({ x, y }) => window.__promo.tap(x, y), { x, y }); await ctx.wait(0.3); }
      await page.mouse.up();
      await ctx.wait(0.6);
      await ctx.tap(page.getByRole("button", { name: "Send now" })).catch(() => {});
      await page.getByText("Neither blocks").waitFor({ timeout: 8000 }).catch(() => {});
      await ctx.wait(0.3);
      await ctx.spot(page.getByRole("button", { name: "Play this reply aloud" }).last().locator("xpath=..")).catch(() => {});
      await ctx.line(2, -0.9);
      await ctx.unspot();
      await ctx.tap(account(page));
      await ctx.wait(0.5);
      await ctx.tap(page.getByRole("menuitem", { name: "Voice" }));
      await ctx.wait(0.9);
      await ctx.scrollTo(page.getByText("Give each agent its own voice", { exact: false }), 0.15, 900);

    },
  },

  // ---- 6. See and steer everything ---------------------------------------------------------
  fleet: {
    async setup({ page, base }) {
      await page.goto(`${base}/fleet`);
      await page.getByText("builder", { exact: true }).first().waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.1);
      await ctx.spot(page.getByPlaceholder(/Filter agents/).locator("xpath=ancestor::*[3]")).catch(() => {});
      await ctx.wait(1.4);
      await ctx.unspot();
      await ctx.click(page.getByText("builder", { exact: true }).first());
      await ctx.wait(1.2);
      await ctx.click(page.getByRole("tab", { name: "Terminal" }).or(page.getByRole("button", { name: "Terminal", exact: true })).last());
      await ctx.wait(1.0);
      await ctx.click(page.locator(".xterm").filter({ visible: true }).first(), { ms: 450 });
      await ctx.wait(0.3);
      for (const ch of "colors") { await page.keyboard.type(ch); await ctx.wait(0.09); }
      await page.keyboard.press("Enter");
    },
  },

  spend: {
    async setup({ page, base }) {
      // Give the journal something to show: answer a question, allow a command, nudge a job.
      await page.goto(`${base}/inbox`);
      await page.getByText("Where should the release notes live?").first().click();
      await page.getByRole("button", { name: /CHANGELOG\.md in the repo/ }).click();
      await page.waitForTimeout(500);
      await page.getByText(/Bash: go test/).first().click();
      await page.getByRole("button", { name: /Allow once/ }).click();
      await page.waitForTimeout(500);
      await page.goto(`${base}/`);
      await page.getByRole("button", { name: /Profile the fleet cache/ }).first().click();
      await page.getByRole("button", { name: "Nudge", exact: true }).click();
      await page.waitForTimeout(1500);
      await page.goto(`${base}/`);
      await page.getByRole("region", { name: "Needs you or stuck" }).waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, 0.1);
      await ctx.click(page.getByRole("button", { name: /^Spend/ }).first());
      await ctx.wait(1.4);
      await ctx.click(page.getByRole("button", { name: "By day" }).or(page.getByRole("tab", { name: "By day" })).first());
      await ctx.wait(1.0);
      await page.keyboard.press("Escape");
      await ctx.wait(0.3);
      await ctx.click(page.getByRole("button", { name: "Schedule", exact: true }).last());
      await ctx.wait(1.6);
      await page.keyboard.press("Escape");
      await ctx.wait(0.3);
      await ctx.click(page.getByRole("button", { name: "Journal", exact: true }).last());
      await ctx.line(2, -0.4);
      await page.keyboard.press("Escape");
      await ctx.wait(0.3);
      await ctx.spot(page.getByTestId("machine-strip"), { pad: 10 });
    },
  },

  // ---- 7. Getting started ------------------------------------------------------------------
  setup: {
    async setup({ page, demo }) {
      for (let i = 0; i < 30 && !demo.setupLink(); i++) await page.waitForTimeout(100);
      await page.goto(demo.setupLink());
      await page.getByLabel("Username").waitFor();
    },
    async run(ctx) {
      const { page } = ctx;
      await ctx.line(1, -0.2);
      await ctx.type(page.getByLabel("Username"), "jeff", 16);
      const pw = "harbour lantern compass gull";
      await ctx.click(page.getByLabel("Password", { exact: true }), { ms: 400, pause: 80 });
      await page.keyboard.type(pw, { delay: 18 });
      await ctx.click(page.getByLabel("Password again"), { ms: 400, pause: 80 });
      await page.keyboard.type(pw, { delay: 14 });
      await ctx.click(page.getByRole("button", { name: "Set up crowsnest" }));
      await page.getByText("Almost there").waitFor({ timeout: 8000 });
      await ctx.wait(0.4);
      await ctx.spot([page.getByText("Almost there"), page.getByRole("button", { name: "Re-check" })], { pad: 16 });
      await ctx.move(page.getByRole("button", { name: "Start" }), 900);
    },
  },
};

