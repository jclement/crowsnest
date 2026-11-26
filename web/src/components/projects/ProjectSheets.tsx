// The sheets that make and change a project (DESIGN.md 21.1): New project (the directory picker,
// a name from the directory) and Edit project (the same form, plus archive and unarchive).
// Archiving stops the project's lead; an archived project takes no work until unarchived.
import { Archive, ArchiveRestore } from "lucide-react";
import { useState } from "react";
import type { ProjectInput, ProjectView } from "../../lib/projects";
import { useCreateProject, useUpdateProject } from "../../lib/queries";
import { Button } from "../ui/Button";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";
import { ProjectForm } from "./ProjectForm";

export function NewProjectSheet({ open, onClose, onCreated }: { open: boolean; onClose: () => void; onCreated?: (project: ProjectView) => void }) {
  const create = useCreateProject();
  const toast = useToast();
  const submit = (input: ProjectInput) =>
    create.mutate(input, {
      onSuccess: (project) => {
        toast.show({ tone: "success", title: `Project ${project.name} created`, description: project.dir });
        create.reset();
        onClose();
        onCreated?.(project);
      },
    });
  const close = () => {
    create.reset();
    onClose();
  };
  return (
    <Sheet open={open} onClose={close} title="New project" size="md">
      {open && <ProjectForm submitLabel="Create project" pending={create.isPending} error={create.error?.message} onSubmit={submit} onCancel={close} />}
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
