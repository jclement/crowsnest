// ScreenPreview: the agent's visible screen as monospace text (GET /agents/{target}/screen),
// polled while shown. It is a read-only glance until the live terminal arrives.
import { RefreshCw } from "lucide-react";
import type { UseQueryResult } from "@tanstack/react-query";
import type { AgentScreen } from "../../lib/types";
import { Banner } from "../ui/Banner";
import { IconButton } from "../ui/Button";
import { Card } from "../ui/Card";
import { Chip } from "../ui/Chip";
import { Skeleton } from "../ui/Skeleton";
import { ScreenText } from "./DialogCard";

export function ScreenPreview({ query }: { query: UseQueryResult<AgentScreen> }) {
  const screen = query.data;
  return (
    <Card
      title="Screen"
      bodyClassName="p-2"
      actions={
        <>
          {screen?.draft.present && (
            <Chip tone="warning" title="Text typed at the terminal is waiting in the input box">
              draft in input
            </Chip>
          )}
          {screen && (
            <Chip mono title={`Screen hash ${screen.screen_hash}`}>
              {screen.screen_hash.slice(0, 8)}
            </Chip>
          )}
          <IconButton
            icon={RefreshCw}
            label="Refresh screen"
            onClick={() => void query.refetch()}
            className={query.isFetching ? "[&_svg]:animate-spin" : undefined}
          />
        </>
      }
    >
      {query.isError ? (
        <Banner tone="danger" role="alert" className="rounded">
          Could not read the screen: {query.error.message}
        </Banner>
      ) : screen ? (
        <ScreenText text={screen.text} className="max-h-[50vh] border-0" />
      ) : (
        <div role="status" aria-label="Loading screen" className="flex flex-col gap-1.5 p-2">
          <Skeleton className="h-3 w-3/4" />
          <Skeleton className="h-3 w-1/2" />
          <Skeleton className="h-3 w-2/3" />
        </div>
      )}
    </Card>
  );
}
