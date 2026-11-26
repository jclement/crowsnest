import { Link } from "@tanstack/react-router";
import { Bird } from "lucide-react";
import { EmptyState } from "../components/EmptyState";

export function NotFound() {
  return (
    <EmptyState
      icon={Bird}
      title="Nothing on the horizon"
      action={
        <Link to="/" className="mt-2 text-xs text-accent hover:underline">
          Back to chat
        </Link>
      }
    >
      The lookout searched everywhere. This page is not out there.
    </EmptyState>
  );
}
