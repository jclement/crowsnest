// Entry point: one QueryClient, the router, and the global 401 handler that sends the user to
// the login page when a session ends mid-use.
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { RouterProvider } from "@tanstack/react-router";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { ToastProvider } from "./components/ui/Toast";
import "./index.css";
import { ApiError, onUnauthorized } from "./lib/api";
import { captureSetupToken } from "./lib/setupToken";
import { applyStartupTheme } from "./lib/theme";
import { makeRouter } from "./router";

// Take a setup token out of the address bar before anything renders (lib/setupToken.ts).
captureSetupToken();

// public/theme-init.js already applied the theme; this catches a saved one that no longer exists.
applyStartupTheme();

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // A 401 or 403 will not fix itself by retrying; everything else gets two more tries.
      retry: (failures, error) => !(error instanceof ApiError && [401, 403].includes(error.status)) && failures < 2,
      refetchOnWindowFocus: true,
    },
  },
});
const router = makeRouter(queryClient);

onUnauthorized(() => {
  if (router.state.location.pathname === "/login") return;
  queryClient.clear();
  void router.navigate({ to: "/login", search: { redirect: router.state.location.href } });
});

const root = document.getElementById("root");
if (!root) throw new Error("index.html is missing #root");
createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <ToastProvider>
        <RouterProvider router={router} />
      </ToastProvider>
    </QueryClientProvider>
  </StrictMode>,
);
