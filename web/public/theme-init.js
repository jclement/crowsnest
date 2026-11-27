// Runs before first paint, before React: apply the saved theme and mode so the page never
// flashes the wrong colours. Keep in step with src/lib/theme.ts.
//
// The theme: this browser's choice (crowsnest-theme-name), else the server's default
// (data-theme-default on <html>, written by the server from the "theme" setting), else the house
// theme. This script cannot see the theme list (the stylesheet loads after it), so a saved name
// whose theme has since been removed is corrected by src/main.tsx (applyStartupTheme) as the app
// boots: the cost is one frame of index.css's sentinel colours, once.
// The mode: "system" (or nothing saved) follows the OS.
(function () {
  var root = document.documentElement;
  var theme = "crowsnest";
  try {
    theme = localStorage.getItem("crowsnest-theme-name") || root.getAttribute("data-theme-default") || theme;
    var saved = localStorage.getItem("crowsnest-theme");
    var dark = saved === "dark" || (saved !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    root.classList.toggle("dark", dark);
  } catch (e) {
    /* storage blocked: the house theme, light, is fine */
  }
  root.setAttribute("data-theme", theme);
})();
