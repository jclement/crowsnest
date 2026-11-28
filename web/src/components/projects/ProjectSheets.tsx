// The sheets that make and change a project (DESIGN.md 21.1): New project (the directory picker,
// a name from the directory) and Edit project (the same form, plus archive and unarchive).
// Archiving stops the project's lead; an archived project takes no work until unarchived.
import { Archive, ArchiveRestore } from "lucide-react";
import { useState } from "react";
import { useQueryClient } from "@tanstack/react-query";
import { endpoints, queryKeys } from "../../lib/api";
import type { ProjectInput, ProjectView } from "../../lib/projects";
import { useBrowsePlaces, useCreateProject, useUpdateProject } from "../../lib/queries";
import { tildePath } from "../../lib/browse";
import { Button } from "../ui/Button";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";
import { ProjectForm, type ProjectExtras } from "./ProjectForm";

interface NewProjectSheetProps {
  open: boolean;
  onClose: () => void;
  onCreated?: (project: ProjectView) => void;
  /** Open the folder picker as the sheet opens. */
  browse?: boolean;
}

export function NewProjectSheet({ open, onClose, onCreated, browse }: NewProjectSheetProps) {
  const create = useCreateProject();
  // The folder as the picker showed it: ~ for home (R10: three lines of temporary path in a toast).
  const home = useBrowsePlaces(open).data?.home;
  const toast = useToast();
  const queryClient = useQueryClient();
  const submit = (input: ProjectInput, extras: ProjectExtras) =>
    create.mutate({ ...input, trust_folder: extras.trustFolder, ...(extras.trustFolder && extras.codexFingerprint !== undefined ? { codex_fingerprint: extras.codexFingerprint } : {}) }, {
      onSuccess: (project) => {
        toast.show({ tone: "success", title: `Project ${project.name} created`, description: tildePath(project.dir, home) });
        const trustProblem = (project.trust ?? []).find((result) => result.status === "failed" || (result.status === "skipped" && result.agent === "claude"));
        if (trustProblem) {
          toast.show({
            tone: "warning",
            title: "Its folder was not marked trusted",
            description: `${trustProblem.detail ?? trustProblem.status}. Its first agent will ask in its terminal; the question comes to you as a card.`,
          });
        }
        create.reset();
        onClose();
        onCreated?.(project);
        // A check command other than the one found by itself is the project's acceptance setting.
        if (extras.checkCommand && extras.checkCommand !== extras.detectedCheck) {
          endpoints
            .setProjectAccept(project.id, { check_command: extras.checkCommand, budget_usd: 0, budget_minutes: 0 })
            .then(() => queryClient.invalidateQueries({ queryKey: queryKeys.projectAccept(project.id) }))
            .catch((error: unknown) => toast.show({ tone: "danger", title: "The check command was not saved", description: error instanceof Error ? error.message : String(error) }));
        }
      },
    });
  const close = () => {
    create.reset();
    onClose();
  };
  return (
    <Sheet open={open} onClose={close} title="New project" size="md">
      {open && <ProjectForm submitLabel="Create project" pending={create.isPending} error={create.error?.message} onSubmit={submit} onCancel={close} startBrowsing={browse} />}
    </Sheet>
  );
}

export function EditProjectSheet({ project, open, onClose }: { project: ProjectView; open: boolean; onClose: () => void }) {
  const update = useUpdateProject(project.slug);
  const toast = useToast();
  const submit = (input: ProjectInput) =>
    update.mutate(input, {
      onSuccess: (saved) => {
        toast.show({ tone: "success", title: `${saved.name} saved` });
        update.reset();
        onClose();
      },
    });
  const close = () => {
    update.reset();
    onClose();
  };
  return (
    <Sheet open={open} onClose={close} title={`Edit ${project.name}`} size="md">
      {open && <ProjectForm project={project} submitLabel="Save" pending={update.isPending} error={update.error?.message} onSubmit={submit} onCancel={close} />}
    </Sheet>
  );
}

/** Archive (with a confirmation: it stops the lead) or unarchive. */
export function ArchiveButton({ project }: { project: ProjectView }) {
  const [confirming, setConfirming] = useState(false);
  const update = useUpdateProject(project.slug);
  const toast = useToast();
  const set = (archived: boolean) =>
    update.mutate(
      { archived },
      {
        onSuccess: () => {
          setConfirming(false);
          toast.show({ tone: "success", title: archived ? `${project.name} archived` : `${project.name} unarchived` });
        },
        onError: (error) => toast.show({ tone: "danger", title: "Not changed", description: error.message }),
      },
    );
  if (project.archived) {
    return (
      <Button compact variant="ghost" icon={ArchiveRestore} onClick={() => set(false)} disabled={update.isPending}>
        Unarchive
      </Button>
    );
  }
  if (!confirming) {
    return (
      <Button compact variant="ghost" icon={Archive} onClick={() => setConfirming(true)}>
        Archive
      </Button>
    );
  }
  return (
    <span className="flex flex-wrap items-center gap-2 text-xs text-body">
      Archive {project.name}? Its lead stops and it takes no work until unarchived.
      <Button compact variant="danger" onClick={() => set(true)} disabled={update.isPending}>
        Archive
      </Button>
      <Button compact variant="ghost" onClick={() => setConfirming(false)}>
        Keep it
      </Button>
    </span>
  );
}
