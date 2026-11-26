// The Terminal view (M5). Jobs, Fleet, Chat and Inbox have their own files in routes/.
import { TerminalPanel } from "../components/terminal/TerminalPanel";
import { terminalPaths } from "../lib/api";

/** The whole herdr session (the TUI in a PTY) in a terminal that fills the visible screen. */
export function TerminalView() {
  return <TerminalPanel path={terminalPaths.tui} label="herdr session terminal" fill="viewport" />;
}
