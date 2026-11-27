// The contextual help sheet: one manual section, over the view the "?" was pressed on, so reading
// it never loses your place. "Read the whole page" goes to the Help view at that heading.
import { Link } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";
import { useManual } from "../../lib/manual-queries";
import { helpPath, parseTopic, sectionMarkdown } from "../../lib/manual";
import { Sheet } from "../ui/Sheet";
import { Skeleton } from "../ui/Skeleton";
import { ManualMarkdown } from "./ManualMarkdown";

export function HelpSheet({ topic, onClose }: { topic: string | null; onClose: () => void }) {
  const manual = useManual(topic !== null);
  const { id, hash } = parseTopic(topic ?? "");
  const page = manual.data?.manual.pages.get(id);
  const section = page ? sectionMarkdown(page, hash) : null;
  const title = section?.heading?.text ?? page?.title ?? "Help";
  return (
    <Sheet open={topic !== null} onClose={onClose} title={title} size="md">
      <div className="flex flex-col gap-3 p-3 md:p-4" data-help-topic={topic ?? undefined}>
        {manual.isPending && <Skeleton className="h-24" />}
        {manual.data && !page && <p className="text-sm text-muted">This help topic is missing from the manual ({topic}).</p>}
        {manual.data && page && section && (
          <>
            {section.heading && <p className="text-2xs uppercase tracking-wide text-muted">{page.title}</p>}
            <ManualMarkdown manual={manual.data.manual} pageId={id} onNavigate={onClose}>
              {section.markdown}
            </ManualMarkdown>
            <Link
              to={helpPath(id, hash)}
              onClick={onClose}
              className="inline-flex min-h-11 items-center gap-1.5 self-start text-sm font-medium text-accent hover:underline md:min-h-8"
            >
              <BookOpen className="h-4 w-4" aria-hidden />
              Read the whole page
            </Link>
          </>
        )}
      </div>
    </Sheet>
  );
}
