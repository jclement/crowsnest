// Scrolling a view to one of its parts without moving the app's frame (R10 F-B1 / O-B5).
//
// The frame (components/AppFrame.tsx) is a fixed-height column whose <main> scrolls. A plain
// `element.scrollIntoView()` scrolls every scrollable ancestor, the document included: the
// document can scroll a few pixels past the frame, so a link to /settings#tailnet left the header
// (search, account) cut off at the top and a blank band under the tab bar. This scrolls only the
// nearest ancestor that scrolls itself, and puts the document back where it belongs.

/** The nearest ancestor of element whose own content scrolls vertically, or null. */
export function scrollParent(element: HTMLElement): HTMLElement | null {
  for (let node = element.parentElement; node; node = node.parentElement) {
    const overflowY = getComputedStyle(node).overflowY;
    if ((overflowY === "auto" || overflowY === "scroll") && node.scrollHeight > node.clientHeight) return node;
  }
  return null;
}

/**
 * Scrolls element's scroll container so element's top sits `margin` pixels below the container's
 * top. The document itself is never scrolled; if something already scrolled it, it goes back to 0.
 */
export function scrollToTop(element: HTMLElement, { margin = 12, smooth = false }: { margin?: number; smooth?: boolean } = {}): void {
  const container = scrollParent(element);
  if (container) {
    const top = element.getBoundingClientRect().top - container.getBoundingClientRect().top + container.scrollTop - margin;
    container.scrollTo({ top: Math.max(0, top), behavior: smooth ? "smooth" : "auto" });
  }
  const root = document.scrollingElement;
  if (root && root.scrollTop !== 0) root.scrollTop = 0;
}
