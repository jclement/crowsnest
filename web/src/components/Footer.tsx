// Footer: copyright, version from /health ("DEV" for a development build), and a tagline.
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
      <span>(C) 2026 Jeff Clement{version && `, ${version}`}</span>
      {!stacked && <span className="mx-1.5 hidden sm:inline">·</span>}
      <span className={stacked ? "block italic" : "block truncate italic sm:inline"}>{tagline}</span>
    </footer>
  );
}
