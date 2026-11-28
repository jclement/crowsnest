// AgentStartList: what a folder carries that an agent runs or reads the moment it starts there
// (R9-S4, internal/agenttrust/inspect.go), in the words the trust box and the trust banner share.
// Hooks, MCP servers and a Codex config run with no prompt once the folder is trusted, so they are
// named first, with "these run when an agent starts"; instruction files are only read.
import { findingLabel, type AgentStartFinding } from "../../lib/projects";

export function AgentStartList({ findings }: { findings: AgentStartFinding[] }) {
  const runs = findings.filter((finding) => finding.runs);
  const reads = findings.filter((finding) => !finding.runs);
  if (findings.length === 0) {
    return <span className="text-2xs text-muted">Nothing in this folder runs when an agent starts (no agent hooks, MCP servers or Codex config, no instruction files).</span>;
  }
  return (
    <div className="flex flex-col gap-1 text-2xs" aria-label="What runs when an agent starts here">
      {runs.length > 0 && (
        <div className="flex flex-col gap-0.5">
          <span className="font-medium text-warning">These run when an agent starts, with no prompt once the folder is trusted:</span>
          <ul className="list-disc pl-4 text-body">
            {runs.map((finding) => (
              <li key={finding.path} className="break-all font-mono">{findingLabel(finding)}</li>
            ))}
          </ul>
          <span className="text-muted">A repository someone else wrote can run commands here. Trust it only once you have read these.</span>
        </div>
      )}
      {reads.length > 0 && (
        <div className="flex flex-col gap-0.5">
          <span className="font-medium text-heading">Agents read these first, as instructions:</span>
          <ul className="list-disc pl-4 text-body">
            {reads.map((finding) => (
              <li key={finding.path} className="break-all font-mono">{findingLabel(finding)}</li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
