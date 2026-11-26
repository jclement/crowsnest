// useStickToBottom: auto-scroll for a conversation that does not fight the reader. New messages
// scroll the view to the bottom only when the reader is already there; otherwise the view stays
// put and the count of unseen messages grows, for a "new messages" pill. Reaching the bottom by
// any means clears the count.
//
// The scroll container is found from an anchor element inside the list: the nearest ancestor
// marked data-scroll-container, else the nearest one whose overflow scrolls, else the page. That
// lets the same list scroll inside a panel on desktop and with the whole page on a phone.
import { useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";

/** Within this many pixels of the end counts as "at the bottom". */
export const BOTTOM_SLACK_PX = 48;

export function findScrollContainer(from: HTMLElement | null): HTMLElement {
  const marked = from?.closest<HTMLElement>("[data-scroll-container]");
  if (marked) return marked;
  for (let node = from?.parentElement ?? null; node; node = node.parentElement) {
    const overflow = getComputedStyle(node).overflowY;
    if (overflow === "auto" || overflow === "scroll") return node;
  }
  return (document.scrollingElement as HTMLElement | null) ?? document.documentElement;
}

function scrollElementToBottom(element: HTMLElement | null) {
  if (element) element.scrollTop = element.scrollHeight;
}

function distanceFromBottom(element: HTMLElement): number {
  return element.scrollHeight - element.scrollTop - element.clientHeight;
}

/**
 * count is the number of messages shown and revision anything that changes when one of them
 * changes (the highest seq); containerKey changes when the layout (and so the container) might.
 */
export function useStickToBottom(count: number, revision: number, containerKey: unknown) {
  const anchorRef = useRef<HTMLDivElement>(null);
  const containerRef = useRef<HTMLElement | null>(null);
  const atBottomRef = useRef(true);
  const countRef = useRef(count);
  const [unseen, setUnseen] = useState(0);

  const scrollToBottom = useCallback(() => {
    atBottomRef.current = true;
    setUnseen(0);
    scrollElementToBottom(containerRef.current);
  }, []);

  useEffect(() => {
    const container = findScrollContainer(anchorRef.current);
    containerRef.current = container;
    const target: HTMLElement | Window = container === document.scrollingElement ? window : container;
    const onScroll = () => {
      atBottomRef.current = distanceFromBottom(container) <= BOTTOM_SLACK_PX;
      if (atBottomRef.current) setUnseen(0);
    };
    target.addEventListener("scroll", onScroll, { passive: true });
    return () => target.removeEventListener("scroll", onScroll);
  }, [containerKey]);

  useLayoutEffect(() => {
    const added = Math.max(0, count - countRef.current);
    countRef.current = count;
    if (atBottomRef.current) {
      scrollElementToBottom(containerRef.current ?? findScrollContainer(anchorRef.current));
    } else if (added > 0) {
      setUnseen((value) => value + added);
    }
  }, [count, revision]);

  return { anchorRef, unseen, scrollToBottom };
}
