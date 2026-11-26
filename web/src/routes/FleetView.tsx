// The Fleet view (DESIGN.md section 12): the tree at left and the selected agent at right from
// md; on mobile the tree fills the page and an agent opens in a full-screen sheet. The selected
// agent lives in the URL (?agent=<pane id>) so a link or a reload lands on the same agent.
import { useNavigate, useSearch } from "@tanstack/react-router";
import { MousePointerClick, RefreshCw, Ship } from "lucide-react";
import { AgentDetail } from "../components/fleet/AgentDetail";
import { FleetTree } from "../components/fleet/FleetTree";
import { Banner } from "../components/ui/Banner";
import { Button } from "../components/ui/Button";
import { StatusDot } from "../components/ui/Chip";
import { EmptyState } from "../components/ui/EmptyState";
import { Sheet } from "../components/ui/Sheet";
import { SkeletonRows } from "../components/ui/Skeleton";
import { findAgent } from "../lib/fleet";
import { DESKTOP_QUERY, useMediaQuery } from "../lib/media";
import { useFleet } from "../lib/queries";

export function FleetView() {
  const fleet = useFleet();
  const { agent: selected } = useSearch({ strict: false }) as { agent?: string };
  const navigate = useNavigate();
  const desktop = useMediaQuery(DESKTOP_QUERY);
  const select = (paneId: string | undefined) => void navigate({ to: "/fleet", search: { agent: paneId } });

  if (fleet.isPending) return <SkeletonRows rows={8} label="Loading the fleet" />;
  if (fleet.isError) {
    return (
      <Banner
        tone="danger"
        role="alert"
        action={
          <Button compact icon={RefreshCw} onClick={() => void fleet.refetch()}>
            Retry
          </Button>
        }
      >
        Could not load the fleet: {fleet.error.message}
      </Banner>
    );
  }
  if (fleet.data.workspaces.length === 0) {
    return (
      <EmptyState icon={Ship} title="No workspaces yet">
        The herdr session <span className="font-mono">{fleet.data.session}</span> has no workspaces. Agents you or the
        orchestrator start will appear here.
      </EmptyState>
    );
  }

  const found = selected ? findAgent(fleet.data, selected) : null;
  const detail = found && (
    <AgentDetail
      key={found.agent.pane_id}
      agent={found.agent}
      workspace={found.workspace}
      tab={found.tab}
      onClose={desktop ? () => select(undefined) : undefined}
      hideHeading={!desktop}
    />
  );

  return (
    <div className="md:flex md:h-full md:min-h-0">
      <div className="md:w-[22rem] md:shrink-0 md:overflow-y-auto md:border-r md:border-border lg:w-[26rem]">
        <FleetTree fleet={fleet.data} selected={found?.agent.pane_id} onSelect={select} />
      </div>
      {desktop ? (
        <div className="min-w-0 flex-1 overflow-y-auto bg-surface-raised/40">
          {detail ?? (
            <EmptyState icon={MousePointerClick} title="Pick an agent">
              Choose an agent on the left to see its screen, answer its dialogs, and open its conversation or terminal.
            </EmptyState>
          )}
        </div>
      ) : (
        <Sheet
          open={!!found}
          onClose={() => select(undefined)}
          title={
            found && (
              <>
                <StatusDot status={found.agent.status} />
                <span className="truncate">{found.agent.name}</span>
              </>
            )
          }
        >
          {detail}
        </Sheet>
      )}
    </div>
  );
}
