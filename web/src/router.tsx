// Routes (TanStack Router, code-based). /login and /setup are public; everything else sits under
// the "app" layout, whose beforeLoad makes sure there is a session (or --no-auth) before
// rendering, and sends the browser to /login otherwise, or to /setup while crowsnest is locked
// waiting for first-run setup.
import type { QueryClient } from "@tanstack/react-query";
import { Outlet, createRootRouteWithContext, createRoute, createRouter, redirect } from "@tanstack/react-router";
import { AppFrame } from "./components/AppFrame";
import { ApiError, endpoints, isSetupRequired, queryKeys, type Me } from "./lib/api";
import { LoginPage } from "./routes/LoginPage";
import { SetupPage } from "./routes/SetupPage";
import { NotFound } from "./routes/NotFound";
import { FleetView } from "./routes/FleetView";
import { ChatView } from "./routes/ChatView";
import { InboxView } from "./routes/InboxView";
import { JobsView } from "./routes/JobsView";
import { DecideView } from "./routes/DecideView";
import { NowView } from "./routes/NowView";
import { ProjectsView } from "./routes/ProjectsView";
import { TerminalView } from "./routes/views";
import { SystemView } from "./routes/SystemView";
import { DiffView } from "./routes/DiffView";
import { validateDiffSearch } from "./lib/diffSearch";
import { WelcomePage } from "./routes/WelcomePage";

interface RouterContext {
  queryClient: QueryClient;
}

const rootRoute = createRootRouteWithContext<RouterContext>()({
  component: Outlet,
  notFoundComponent: NotFound,
});

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/login",
  validateSearch: (search: Record<string, unknown>): { redirect?: string } => ({
    redirect: typeof search.redirect === "string" ? search.redirect : undefined,
  }),
  // Already signed in, or no login configured: skip the form.
  beforeLoad: async ({ context }) => {
    let setupRequired = false;
    const me = await context.queryClient.fetchQuery({ queryKey: queryKeys.me, queryFn: endpoints.me }).catch((error: unknown) => {
      setupRequired = isSetupRequired(error);
      return null;
    });
    if (setupRequired) throw redirect({ to: "/setup" });
    if (me) throw redirect({ to: "/" });
  },
  component: function LoginRoute() {
    const { redirect: target } = loginRoute.useSearch();
    return <LoginPage redirect={target} />;
  },
});

// First-run setup. Once setup is done (or with a configured login) there is nothing here, and
// the app decides between the login page and the views.
const setupRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/setup",
  loader: async () => {
    const info = await endpoints.setupInfo();
    if (!info.required) throw redirect({ to: "/" });
    return info;
  },
  component: function SetupRoute() {
    const info = setupRoute.useLoaderData();
    return <SetupPage defaultName={info.default_name ?? ""} minLength={info.min_password_length} />;
  },
});

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: "app",
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.ensureQueryData({ queryKey: queryKeys.me, queryFn: endpoints.me });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        throw redirect({ to: "/login", search: { redirect: location.href } });
      }
      if (isSetupRequired(error)) throw redirect({ to: "/setup" });
      throw error;
    }
    // The first-run checklist is unfinished: finish it before the app (DESIGN.md 4.14.1).
    const me = context.queryClient.getQueryData<Me>(queryKeys.me);
    if (me?.features.onboarding) throw redirect({ to: "/welcome" });
  },
  component: AppFrame,
});

// The guided first run: after setup's password step, before the app. Signed in, outside the frame.
const welcomeRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/welcome",
  beforeLoad: async ({ context, location }) => {
    try {
      await context.queryClient.fetchQuery({ queryKey: queryKeys.me, queryFn: endpoints.me });
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) throw redirect({ to: "/login", search: { redirect: location.href } });
      if (isSetupRequired(error)) throw redirect({ to: "/setup" });
      throw error;
    }
  },
  component: WelcomePage,
});

// Now is home (DESIGN.md 22). Links made before it existed opened the board at /?job=j_12 or
// /?project=<slug> (old notifications, bookmarks): those go on to the board, search and all.
const nowRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/",
  validateSearch: (search: Record<string, unknown>): { job?: string; project?: string } => ({
    job: typeof search.job === "string" && search.job ? search.job : undefined,
    project: typeof search.project === "string" && search.project ? search.project : undefined,
  }),
  beforeLoad: ({ search }) => {
    if (search.job || search.project) throw redirect({ to: "/jobs", search: { job: search.job, project: search.project } });
  },
  component: NowView,
});
const jobsRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/jobs",
  // ?job=j_12 selects a job, so a link or a reload opens the same one; ?project=<slug> (or
  // "unfiled", or "all") picks the board's project.
  validateSearch: (search: Record<string, unknown>): { job?: string; project?: string } => ({
    job: typeof search.job === "string" && search.job ? search.job : undefined,
    project: typeof search.project === "string" && search.project ? search.project : undefined,
  }),
  component: JobsView,
});
const chatRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/chat",
  // ?target=orchestrator or lead-<slug>: who the conversation is with.
  validateSearch: (search: Record<string, unknown>): { target?: string } => ({
    target: typeof search.target === "string" && search.target ? search.target : undefined,
  }),
  component: ChatView,
});
const projectsRoute = createRoute({ getParentRoute: () => appRoute, path: "/projects", component: () => <ProjectsView /> });
const projectRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/projects/$slug",
  component: function ProjectRoute() {
    const { slug } = projectRoute.useParams();
    return <ProjectsView slug={slug} />;
  },
});
const inboxRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/inbox",
  // ?q=q_7 (a question) or ?q=dialog:<pane id> (a terminal dialog) selects an item.
  validateSearch: (search: Record<string, unknown>): { q?: string } => ({
    q: typeof search.q === "string" && search.q ? search.q : undefined,
  }),
  component: InboxView,
});
const fleetRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/fleet",
  // ?agent=<pane id> selects an agent, so a link or a reload opens the same one; &view=terminal
  // or conversation picks its tab (the command palette's "Open terminal").
  validateSearch: (search: Record<string, unknown>): { agent?: string; view?: "conversation" | "terminal" } => ({
    agent: typeof search.agent === "string" && search.agent ? search.agent : undefined,
    view: search.view === "terminal" || search.view === "conversation" ? search.view : undefined,
  }),
  component: FleetView,
});
// The decision packet: ?item=<attention item id> (question:q_7, approval:ap_3, dialog:<pane>).
const decideRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/decide",
  validateSearch: (search: Record<string, unknown>): { item?: string } => ({
    item: typeof search.item === "string" && search.item ? search.item : undefined,
  }),
  component: DecideView,
});
const terminalRoute = createRoute({ getParentRoute: () => appRoute, path: "/terminal", component: TerminalView });
const systemRoute = createRoute({ getParentRoute: () => appRoute, path: "/system", component: SystemView });
// The standalone diff (DESIGN.md 6.20): full screen, the app's chrome hidden (AppFrame).
const diffRoute = createRoute({
  getParentRoute: () => appRoute,
  path: "/diff",
  validateSearch: validateDiffSearch,
  component: function DiffRoute() {
    return <DiffView search={diffRoute.useSearch()} />;
  },
});

const routeTree = rootRoute.addChildren([loginRoute, setupRoute, welcomeRoute, appRoute.addChildren([nowRoute, jobsRoute, projectsRoute, projectRoute, chatRoute, inboxRoute, decideRoute, fleetRoute, terminalRoute, systemRoute, diffRoute])]);

export function makeRouter(queryClient: QueryClient) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: "intent" });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
