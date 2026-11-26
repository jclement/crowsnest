// SpendChip: a job card's compact spend (DESIGN.md 7.11): the estimated cost, tokens when no
// price is known, "not measured" for a Codex job. The full wording (estimate, not a bill) is in
// its title and accessible name, so the chip itself stays a few characters.
import { Coins } from "lucide-react";
import { chipText, type SpendSummary } from "../../lib/spend";
import { Chip } from "../ui/Chip";

export function SpendChip({ spend }: { spend: SpendSummary | undefined }) {
  const chip = chipText(spend);
  if (!chip) return null;
  return (
    <Chip mono title={chip.title}>
      <Coins className="h-3 w-3" aria-hidden />
      <span aria-label={`Spend: ${chip.title}`}>{chip.text}</span>
    </Chip>
  );
}
