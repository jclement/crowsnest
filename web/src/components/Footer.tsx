// Footer: copyright, version from /health ("DEV" for a development build), a link to a newer
// release when the server's update check found one (DESIGN.md 16.1), and a tagline.
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { endpoints, queryKeys } from "../lib/api";
import { formatVersion } from "../lib/format";
import { randomTagline } from "../lib/taglines";

/** `stacked` puts the tagline on its own line, for the narrow desktop rail. */
export function Footer({ className = "", stacked = false }: { className?: string; stacked?: boolean }) {
  const health = useQuery({ queryKey: queryKeys.health, queryFn: endpoints.health, staleTime: 60_000 });
  const [tagline] = useState(randomTagline);
  const version = formatVersion(health.data?.version);
  return (
    <footer className={`text-xs text-muted ${className}`}>
      <span>
        (C) 2026 Jeff Clement{version && `, ${version}`}
        {health.data?.update_available && health.data.latest_version && (
          <>
            {" ("}
            <a
              className="underline hover:text-heading"
              href={health.data.release_url}
              target="_blank"
              rel="noopener noreferrer"
              title="crowsnest update installs it (signature verified)"
            >
              {health.data.latest_version} available
            </a>
            {")"}
          </>
        )}
      </span>
      {!stacked && <span className="mx-1.5 hidden sm:inline">·</span>}
      <span className={stacked ? "block italic" : "block truncate italic sm:inline"}>{tagline}</span>
    </footer>
  );
}
