import { Link } from "@tanstack/react-router";
import { Bird } from "lucide-react";
import { EmptyState } from "../components/ui/EmptyState";

export function NotFound() {
  return (
    <EmptyState
      icon={Bird}
      title="Nothing on the horizon"
      action={
        <Link to="/" className="mt-2 inline-flex min-h-11 items-center text-xs text-accent hover:underline md:min-h-0">
          Back to Now
        </Link>
      }
    >
      The lookout searched everywhere. This page is not out there.
    </EmptyState>
  );
}
