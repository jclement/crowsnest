// Routes (TanStack Router, code-based). /login is public; everything else sits under the "app"
// layout, whose beforeLoad makes sure there is a session (or --no-auth) before rendering, and
// sends the browser to /login otherwise.
import type { QueryClient } from "@tanstack/react-query";
import { Outlet, createRootRouteWithContext, createRoute, createRouter, redirect } from "@tanstack/react-router";
import { AppFrame } from "./components/AppFrame";
import { ApiError, endpoints, queryKeys } from "./lib/api";
import { LoginPage } from "./routes/LoginPage";
import { NotFound } from "./routes/NotFound";
import { FleetView } from "./routes/FleetView";
import { ChatView } from "./routes/ChatView";
import { InboxView } from "./routes/InboxView";
import { JobsView, TerminalView } from "./routes/views";

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
    const me = await context.queryClient.fetchQuery({ queryKey: queryKeys.me, queryFn: endpoints.me }).catch(() => null);
    if (me) throw redirect({ to: "/" });
  },
  component: function LoginRoute() {
    const { redirect: target } = loginRoute.useSearch();
    return <LoginPage redirect={target} />;
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
      throw error;
    }
  },
  component: AppFrame,
});

const jobsRoute = createRoute({ getParentRoute: () => appRoute, path: "/", component: JobsView });
const chatRoute = createRoute({ getParentRoute: () => appRoute, path: "/chat", component: ChatView });
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
  // ?agent=<pane id> selects an agent, so a link or a reload opens the same one.
  validateSearch: (search: Record<string, unknown>): { agent?: string } => ({
    agent: typeof search.agent === "string" && search.agent ? search.agent : undefined,
  }),
  component: FleetView,
});
const terminalRoute = createRoute({ getParentRoute: () => appRoute, path: "/terminal", component: TerminalView });

const routeTree = rootRoute.addChildren([loginRoute, appRoute.addChildren([jobsRoute, chatRoute, inboxRoute, fleetRoute, terminalRoute])]);

export function makeRouter(queryClient: QueryClient) {
  return createRouter({ routeTree, context: { queryClient }, defaultPreload: "intent" });
}

declare module "@tanstack/react-router" {
  interface Register {
    router: ReturnType<typeof makeRouter>;
  }
}
