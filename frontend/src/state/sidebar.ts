export const SIDEBAR_OPEN_KEY = "agent-platform.sidebarOpen";

export function readSidebarOpen(): boolean {
  try {
    const raw = window.localStorage.getItem(SIDEBAR_OPEN_KEY);
    if (raw === "false") return false;
    if (raw === "true") return true;
    return true;
  } catch {
    return true;
  }
}

export function writeSidebarOpen(open: boolean): void {
  try {
    window.localStorage.setItem(SIDEBAR_OPEN_KEY, String(open));
  } catch {
    // Ignore storage failures in restricted environments.
  }
}
