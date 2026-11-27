// Carries out a palette command (lib/palette.ts PaletteCommand). The palette closes as soon as an
// effect starts; what happened comes back as a toast, with a link to where it landed when there
// is one ("Open j_12", "Open Chat"). Every call goes through lib/api.ts and refreshes the same
// query keys the views' own mutations refresh, so the board, Inbox and Chat follow at once (and
// the event stream confirms it).
import { useQueryClient } from "@tanstack/react-query";
import { useRouter } from "@tanstack/react-router";
import { useCallback } from "react";
import { endpoints, queryKeys } from "../../lib/api";
import { offerJobDraft } from "../../lib/jobDraft";
import { pokeToast } from "../../lib/journal";
import type { PaletteCommand, PaletteSheet, ViewPath } from "../../lib/palette";
import { setTheme } from "../../lib/theme";
import { useToast } from "../ui/toast-context";

export interface RunContext {
  openSheet: (sheet: PaletteSheet) => void;
  signOut: () => void;
  /** Speak mode's setter, when the voice build provides one. */
  setSpeak?: (on: boolean) => void;
}

export function useRunCommand(context: RunContext): (command: PaletteCommand) => void {
  const router = useRouter();
  const queryClient = useQueryClient();
  const toast = useToast();
  const { openSheet, signOut, setSpeak } = context;

  return useCallback(
    (command: PaletteCommand) => {
      const go = (to: ViewPath | `/projects/${string}` | `/help/${string}`, search: Record<string, string> = {}, hash?: string) =>
        void router.navigate({ to, search, hash } as unknown as Parameters<typeof router.navigate>[0]);
      const failed = (title: string) => (error: unknown) =>
        toast.show({ tone: "danger", title, description: error instanceof Error ? error.message : String(error) });

      switch (command.kind) {
        case "navigate":
          go(command.to, command.search, command.hash);
          return;
        case "sheet":
          openSheet(command.sheet);
          return;
        case "theme":
          setTheme(command.choice);
          return;
        case "signOut":
          signOut();
          return;
        case "speak":
          setSpeak?.(command.on);
          return;
        case "jobForm":
          offerJobDraft({ brief: command.brief });
          go("/jobs");
          return;
        case "giveJob":
          endpoints
            .createJob({ brief: command.brief })
            .then((job) => {
              toast.show({
                tone: "success",
                title: `Job ${job.id} given`,
                description: "Sent to the orchestrator",
                action: { label: `Open ${job.id}`, onClick: () => go("/jobs", { job: job.id }) },
              });
              if (command.open) go("/jobs", { job: job.id });
            })
            .catch(failed("The job was not created"))
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.jobs }));
          return;
        case "ask":
          if (command.open) go("/chat");
          endpoints
            .prompt("orchestrator", { text: command.text })
            .then((result) =>
              toast.show({
                tone: "success",
                title: result.queued ? "Queued for the orchestrator" : "Sent to the orchestrator",
                description: result.queued ? "It is mid-turn and reads this when the turn ends." : undefined,
                action: command.open ? undefined : { label: "Open Chat", onClick: () => go("/chat") },
              }),
            )
            .catch((error: unknown) => {
              toast.show({
                tone: "danger",
                title: "Not sent",
                description: error instanceof Error ? error.message : String(error),
                action: command.open ? undefined : { label: "Open Chat", onClick: () => go("/chat") },
              });
            })
            .finally(() => {
              void queryClient.invalidateQueries({ queryKey: queryKeys.agent("orchestrator") });
              void queryClient.invalidateQueries({ queryKey: queryKeys.orchestrator });
            });
          return;
        case "jobAction": {
          const words = { nudge: ["Nudged", "Not nudged"], resume: ["Getting it moving", "Did not work"], cancel: ["Cancelled", "Not cancelled"] }[command.action];
          endpoints
            .jobAction(command.id, command.action)
            .then(() => toast.show({ tone: "success", title: `${words[0]} ${command.id}`, action: { label: `Open ${command.id}`, onClick: () => go("/jobs", { job: command.id }) } }))
            .catch(failed(`${words[1]}: ${command.id}`))
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.jobs }));
          return;
        }
        case "projectJob":
          endpoints
            .createJob({ brief: command.text, project_id: command.projectId, to: "lead" })
            .then((job) =>
              toast.show({
                tone: "success",
                title: `Job ${job.id} given`,
                description: `To ${command.name}'s lead`,
                action: { label: `Open ${job.id}`, onClick: () => go("/jobs", { job: job.id }) },
              }),
            )
            .catch(failed("The job was not created"))
            .finally(() => {
              void queryClient.invalidateQueries({ queryKey: queryKeys.jobs });
              void queryClient.invalidateQueries({ queryKey: queryKeys.projects });
            });
          return;
        case "leadAction":
          endpoints
            .leadAction(command.slug, command.action)
            .then(() => toast.show({ tone: "success", title: `${command.name}: lead ${command.action === "start" ? "started" : "stopped"}` }))
            .catch(failed(`${command.name}: the lead did not ${command.action}`))
            .finally(() => {
              void queryClient.invalidateQueries({ queryKey: queryKeys.projects });
              void queryClient.invalidateQueries({ queryKey: queryKeys.fleet });
            });
          return;
        case "jobMessage":
          endpoints
            .jobMessage(command.id, command.text)
            .then((result) => toast.show({ tone: "success", title: result.queued ? `Queued for ${command.id}` : `Sent to ${command.id}` }))
            .catch((error: unknown) =>
              toast.show({
                tone: "danger",
                title: `Not sent to ${command.id}`,
                description: error instanceof Error ? error.message : String(error),
                action: { label: `Open ${command.id}`, onClick: () => go("/jobs", { job: command.id }) },
              }),
            )
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.jobs }));
          return;
        case "answer":
          endpoints
            .answerQuestion(command.questionId, command.value === undefined ? {} : { answer_value: command.value })
            .then(() => toast.show({ tone: "success", title: `Answered ${command.questionId}: ${command.label}` }))
            .catch(failed(`Not answered: ${command.questionId}`))
            .finally(() => {
              void queryClient.invalidateQueries({ queryKey: queryKeys.questions });
              void queryClient.invalidateQueries({ queryKey: queryKeys.jobs });
            });
          return;
        case "poke":
          endpoints
            .poke()
            .then((result) => toast.show(pokeToast(result)))
            .catch(failed("Could not poke"))
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.journal }));
          return;
        case "resume":
          endpoints
            .resume()
            .then((state) => {
              queryClient.setQueryData(queryKeys.pause, state);
              toast.show({ tone: "success", title: "Automation resumed" });
            })
            .catch(failed("Not resumed"));
          return;
        case "digestNow":
          endpoints
            .digestNow()
            .then((result) => toast.show({ tone: "success", title: "Digest on its way", description: result.note }))
            .catch(failed("No digest"))
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.digests }));
          return;
        case "compact":
          endpoints
            .orchestratorCompact()
            .then(() => toast.show({ tone: "success", title: "Compacting the orchestrator" }))
            .catch(failed("Not compacted"))
            .finally(() => {
              void queryClient.invalidateQueries({ queryKey: queryKeys.orchestratorContext });
              void queryClient.invalidateQueries({ queryKey: queryKeys.journal });
            });
          return;
        case "relaunch":
          (command.action === "restart" ? endpoints.orchestratorRestart() : endpoints.orchestratorNew())
            .then((status) => {
              queryClient.setQueryData(queryKeys.orchestrator, status);
              toast.show({ tone: "success", title: command.action === "restart" ? "Orchestrator restarted" : "New conversation started" });
            })
            .catch(failed("That did not work"))
            .finally(() => void queryClient.invalidateQueries({ queryKey: queryKeys.agents }));
          return;
        case "pushTest":
          endpoints
            .pushTest()
            .then((result) =>
              toast.show(
                result.sent + result.failed === 0
                  ? { tone: "warning", title: "No device has notifications on", action: { label: "Notifications", onClick: () => openSheet("notifications") } }
                  : { tone: result.failed ? "warning" : "success", title: `Test sent to ${result.sent} device${result.sent === 1 ? "" : "s"}`, description: result.failed ? `${result.failed} failed` : undefined },
              ),
            )
            .catch(failed("No test notification"));
          return;
        case "confirm":
        case "input":
          // Levels, not effects: the palette handles them before calling here.
          return;
      }
    },
    [router, queryClient, toast, openSheet, signOut, setSpeak],
  );
}
