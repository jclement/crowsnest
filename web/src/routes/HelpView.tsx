// Help: the built-in manual (DESIGN.md 12.10). /help is the contents, /help/<page> a page and
// /help/<page>#<heading> a heading in it. Desktop: the contents and search on the left, the page,
// with its own headings above it. Phone: a Contents button opens the same list as a sheet. Every
// page ends with previous and next in reading order.
import { Link, useRouterState } from "@tanstack/react-router";
import { BookOpen, ChevronLeft, ChevronRight, List, Search, X } from "lucide-react";
import { useEffect, useId, useMemo, useRef, useState } from "react";
import { ManualMarkdown } from "../components/help/ManualMarkdown";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { cn } from "../lib/cn";
import { CONTENTS_ID, helpPath, neighbours, partOf, searchManual, type Manual, type SearchEntry } from "../lib/manual";
import { useManual } from "../lib/manual-queries";

export function HelpView({ pageId }: { pageId?: string }) {
  const id = pageId ? pageId.replace(/\/+$/, "") : CONTENTS_ID;
  const manual = useManual();
  const hash = useRouterState({ select: (state) => state.location.hash });
  const [contentsOpen, setContentsOpen] = useState(false);
  const articleRef = useRef<HTMLElement | null>(null);

  const page = manual.data?.manual.pages.get(id);

  // Land on the heading a link names, once the page has rendered; else at the top.
  useEffect(() => {
    if (!page) return;
    const frame = requestAnimationFrame(() => {
      const target = hash ? document.getElementById(decodeURIComponent(hash)) : null;
      if (target) target.scrollIntoView({ block: "start" });
      else articleRef.current?.closest("main")?.scrollTo({ top: 0 });
    });
    return () => cancelAnimationFrame(frame);
  }, [page, hash]);

  useEffect(() => {
    const previous = document.title;
    if (page) document.title = `${page.title} · Help`;
    return () => {
      document.title = previous;
    };
  }, [page]);

  if (manual.isPending) return <SkeletonRows label="Loading the manual" />;
  if (!manual.data) return <p className="p-4 text-sm text-danger">The manual could not be loaded.</p>;
  const { manual: data, index } = manual.data;

  return (
    <div className="flex min-h-full md:h-full">
      <aside aria-label="Manual contents" className="hidden w-64 shrink-0 flex-col border-r border-border bg-surface-raised md:flex">
        <ContentsPanel manual={data} index={index} current={id} />
      </aside>
      <div className="min-w-0 flex-1 md:overflow-y-auto">
        <div className="sticky top-0 z-10 flex items-center gap-2 border-b border-border bg-surface/95 px-3 py-1.5 backdrop-blur md:hidden">
          <button
            type="button"
            onClick={() => setContentsOpen(true)}
            className="inline-flex min-h-11 items-center gap-1.5 rounded px-2 text-sm font-medium text-heading hover:bg-hover"
            aria-haspopup="dialog"
          >
            <List className="h-4 w-4" aria-hidden />
            Contents and search
          </button>
        </div>
        {page ? (
          <article ref={articleRef} className="mx-auto w-full max-w-3xl px-3 py-4 md:px-6 md:py-6" aria-labelledby={page.headings[0]?.slug}>
            <Breadcrumb manual={data} id={id} />
            {id !== CONTENTS_ID && <OnThisPage manual={data} id={id} />}
            <ManualMarkdown manual={data} pageId={id} className="text-sm">
              {page.markdown}
            </ManualMarkdown>
            <PrevNext manual={data} id={id} />
          </article>
        ) : (
          <div className="mx-auto max-w-3xl px-3 py-6 text-sm">
            <h1 className="text-lg font-semibold text-heading">No such page</h1>
            <p className="mt-2 text-body">
              The manual has no page <code>{id}</code>.{" "}
              <Link to="/help" className="text-accent hover:underline">
                Open the contents
              </Link>
              .
            </p>
          </div>
        )}
      </div>
      <Sheet open={contentsOpen} onClose={() => setContentsOpen(false)} title="Manual">
        <ContentsPanel manual={data} index={index} current={id} onNavigate={() => setContentsOpen(false)} />
      </Sheet>
    </div>
  );
}

function Breadcrumb({ manual, id }: { manual: Manual; id: string }) {
  if (id === CONTENTS_ID) return null;
  const part = partOf(manual, id);
  return (
    <nav aria-label="Breadcrumb" className="mb-2 flex items-center gap-1 text-2xs text-muted">
      <Link to="/help" className="hover:text-accent hover:underline">
        Manual
      </Link>
      {part && (
        <>
          <ChevronRight className="h-3 w-3" aria-hidden />
          <span>{part.title}</span>
        </>
      )}
    </nav>
  );
}

/** The page's own h2 headings, as links to their anchors (deep links). */
function OnThisPage({ manual, id }: { manual: Manual; id: string }) {
  const page = manual.pages.get(id);
  const headings = page?.headings.filter((heading) => heading.depth === 2) ?? [];
  if (headings.length < 3) return null;
  return (
    <nav aria-label="On this page" className="mb-4 rounded border border-border bg-surface-raised px-3 py-2">
      <p className="mb-1 text-2xs font-semibold uppercase tracking-wide text-muted">On this page</p>
      <ul className="flex flex-wrap gap-x-3 gap-y-1 text-xs">
        {headings.map((heading) => (
          <li key={heading.slug}>
            <Link to={helpPath(id, heading.slug)} className="inline-flex min-h-11 items-center text-accent hover:underline md:min-h-0">
              {heading.text}
            </Link>
          </li>
        ))}
      </ul>
    </nav>
  );
}

function PrevNext({ manual, id }: { manual: Manual; id: string }) {
  const { previous, next } = neighbours(manual, id);
  if (!previous && !next) return null;
  return (
    <nav aria-label="Previous and next" className="mt-8 grid grid-cols-2 gap-2 border-t border-border pt-3">
      {previous ? (
        <Link to={helpPath(previous.id)} className="flex min-h-11 flex-col justify-center rounded border border-border px-3 py-1.5 hover:bg-hover">
          <span className="flex items-center gap-0.5 text-2xs text-muted">
            <ChevronLeft className="h-3 w-3" aria-hidden />
            Previous
          </span>
          <span className="truncate text-sm text-heading">{previous.title}</span>
        </Link>
      ) : (
        <span />
      )}
      {next ? (
        <Link to={helpPath(next.id)} className="flex min-h-11 flex-col items-end justify-center rounded border border-border px-3 py-1.5 text-right hover:bg-hover">
          <span className="flex items-center gap-0.5 text-2xs text-muted">
            Next
            <ChevronRight className="h-3 w-3" aria-hidden />
          </span>
          <span className="max-w-full truncate text-sm text-heading">{next.title}</span>
        </Link>
      ) : (
        <span />
      )}
    </nav>
  );
}

/** Search across the manual, and the contents when the box is empty. */
function ContentsPanel({ manual, index, current, onNavigate }: { manual: Manual; index: SearchEntry[]; current: string; onNavigate?: () => void }) {
  const [query, setQuery] = useState("");
  const inputId = useId();
  const hits = useMemo(() => (query.trim().length >= 2 ? searchManual(index, query, 20) : null), [index, query]);
  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="border-b border-border p-2">
        <label htmlFor={inputId} className="sr-only">
          Search the manual
        </label>
        <div className="relative">
          <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted" aria-hidden />
          <input
            id={inputId}
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Search the manual"
            className="h-11 w-full rounded border border-border bg-surface pl-7 pr-11 text-sm text-heading placeholder:text-muted md:h-8"
          />
          {query && (
            <button type="button" onClick={() => setQuery("")} aria-label="Clear the search" className="absolute right-0 top-0 inline-flex h-11 w-11 items-center justify-center text-muted md:h-8 md:w-8">
              <X className="h-3.5 w-3.5" aria-hidden />
            </button>
          )}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto p-2">
        {hits ? (
          <section aria-label="Search results">
            <p role="status" className="px-1 pb-1 text-2xs text-muted">
              {hits.length === 0 ? "Nothing in the manual matches." : `${hits.length} ${hits.length === 1 ? "match" : "matches"}`}
            </p>
            <ul className="flex flex-col gap-0.5">
              {hits.map((hit) => (
                <li key={`${hit.pageId}#${hit.slug}`}>
                  <Link to={helpPath(hit.pageId, hit.slug)} onClick={onNavigate} className="block min-h-11 rounded px-2 py-1.5 hover:bg-hover">
                    <span className="block text-sm font-medium text-heading">{hit.slug ? hit.heading : hit.pageTitle}</span>
                    {hit.slug && <span className="block text-2xs text-muted">{hit.pageTitle}</span>}
                    <span className="mt-0.5 block text-xs text-body">{hit.snippet}</span>
                  </Link>
                </li>
              ))}
            </ul>
          </section>
        ) : (
          <nav aria-label="Pages">
            <Link
              to="/help"
              onClick={onNavigate}
              className={cn("mb-2 flex min-h-11 items-center gap-1.5 md:min-h-9 rounded px-2 text-sm hover:bg-hover", current === CONTENTS_ID ? "font-medium text-accent" : "text-heading")}
              aria-current={current === CONTENTS_ID ? "page" : undefined}
            >
              <BookOpen className="h-4 w-4" aria-hidden />
              Contents
            </Link>
            {manual.parts.map((part) => (
              <section key={part.title} className="mb-2">
                <h2 className="px-2 pb-0.5 text-2xs font-semibold uppercase tracking-wide text-muted">{part.title}</h2>
                <ul>
                  {part.pages.map((pageId) => {
                    const page = manual.pages.get(pageId);
                    if (!page) return null;
                    return (
                      <li key={pageId}>
                        <Link
                          to={helpPath(pageId)}
                          onClick={onNavigate}
                          aria-current={current === pageId ? "page" : undefined}
                          className={cn("flex min-h-11 items-center rounded px-2 text-sm hover:bg-hover md:min-h-7", current === pageId ? "bg-accent/10 font-medium text-accent" : "text-body")}
                        >
                          {page.title}
                        </Link>
                      </li>
                    );
                  })}
                </ul>
              </section>
            ))}
          </nav>
        )}
      </div>
    </div>
  );
}
