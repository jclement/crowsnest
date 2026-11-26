// Runs before first paint, before React: apply the saved theme so the page never flashes the
// wrong colours. "system" (or nothing saved) follows the OS. Keep in step with src/lib/theme.ts.
(function () {
  try {
    var saved = localStorage.getItem("crowsnest-theme");
    var dark = saved === "dark" || (saved !== "light" && window.matchMedia("(prefers-color-scheme: dark)").matches);
    document.documentElement.classList.toggle("dark", dark);
  } catch (e) {
    /* storage blocked: the light default is fine */
  }
})();
