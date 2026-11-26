// The Settings sheet (account menu → Settings): the settings that make sense to change while
// crowsnest runs, by section, each showing where its value comes from and when a change takes
// effect. A setting given by a flag or an environment variable is read-only here ("set by the
// environment: change it there"); the bind is shown read-only with the command that changes it.
// Account, Voice and Notifications keep their own sheets; this one links to them.
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { ApiError } from "../../lib/api";
import { applyLabels, sourceLabels, systemEndpoints, systemKeys, type SettingView, type SettingsPage } from "../../lib/system";
import { Button } from "../ui/Button";
import { Chip } from "../ui/Chip";
import { Sheet } from "../ui/Sheet";
import { useToast } from "../ui/toast-context";
import { CommandLine } from "./CheckList";

const inputClass = "h-11 w-full min-w-0 rounded border border-border bg-surface px-3 text-sm font-normal text-heading md:h-9 disabled:bg-surface-sunken disabled:text-muted";

interface SettingsSheetProps {
  open: boolean;
  onClose: () => void;
  /** Opens one of the other sheets this one links to. */
  onOpen?: (sheet: "account" | "voice" | "notifications") => void;
}

export function SettingsSheet({ open, onClose, onOpen }: SettingsSheetProps) {
  const page = useQuery({ queryKey: systemKeys.settings, queryFn: systemEndpoints.settings, enabled: open });
  return (
    <Sheet open={open} onClose={onClose} title="Settings">
      <div className="flex flex-col gap-5 p-4">
        {page.isPending && <p className="text-xs text-muted">Loading…</p>}
        {page.isError && <p className="text-xs text-danger">Could not load the settings. {(page.error as Error).message}</p>}
        {page.data && <SettingsBody page={page.data} onOpen={onOpen} />}
      </div>
    </Sheet>
  );
}

export function SettingsBody({ page, onOpen }: { page: SettingsPage; onOpen?: SettingsSheetProps["onOpen"] }) {
  const sections = [...new Set(page.settings.map((setting) => setting.section))];
  return (
    <>
      <section className="flex flex-col gap-2">
        <h3 className="text-2xs uppercase tracking-wide text-muted">Elsewhere</h3>
        <div className="flex flex-wrap gap-2">
          <Button compact onClick={() => onOpen?.("account")}>
            Account and name ({page.name})
          </Button>
          <Button compact onClick={() => onOpen?.("notifications")}>
            Notifications
          </Button>
          <Button compact onClick={() => onOpen?.("voice")}>
            Voice
          </Button>
        </div>
      </section>
      {sections.map((section) => (
        <section key={section} className="flex flex-col gap-1" aria-label={section}>
          <h3 className="text-2xs uppercase tracking-wide text-muted">{section}</h3>
          <ul className="divide-y divide-border">
            {page.settings
              .filter((setting) => setting.section === section)
              .map((setting) => (
                <li key={setting.name} className="py-3">
                  <SettingRow setting={setting} engineNow={setting.name === "engine" ? page.orchestrator_engine : undefined} />
                </li>
              ))}
          </ul>
        </section>
      ))}
      <section className="flex flex-col gap-1.5" aria-label="Network">
        <h3 className="text-2xs uppercase tracking-wide text-muted">Network</h3>
        <p className="text-xs text-body">
          Listening on <span className="font-mono">{page.bind}</span>. The bind, the login, the herdr session and the data directory are not changed from a
          browser: restart crowsnest with the flag or variable.
        </p>
        <CommandLine command={page.bind_command} />
      </section>
      {page.config_file && <p className="text-2xs text-muted">Config file: {page.config_file}. A value set here wins over the file; a flag or environment variable wins over both.</p>}
    </>
  );
}

function SettingRow({ setting, engineNow }: { setting: SettingView; engineNow?: string }) {
  const toast = useToast();
  const queryClient = useQueryClient();
  const initial = setting.write_only ? "" : setting.value;
  const [draft, setDraft] = useState(initial);
  const [error, setError] = useState<string | null>(null);
  const locked = Boolean(setting.locked);
  const save = useMutation({
    mutationFn: (value: string | null) => (value === null ? systemEndpoints.clearSetting(setting.name) : systemEndpoints.updateSetting(setting.name, value)),
  });
  const id = `setting-${setting.name}`;

  const commit = (value: string | null) => {
    setError(null);
    save
      .mutateAsync(value)
      .then((view) => {
        setDraft(view.write_only ? "" : view.value);
        void queryClient.invalidateQueries({ queryKey: systemKeys.settings });
        void queryClient.invalidateQueries({ queryKey: systemKeys.system });
        toast.show({ tone: "success", title: `${setting.label} saved`, description: applyLabels[setting.apply] + "." });
      })
      .catch((caught: unknown) => setError(caught instanceof ApiError ? caught.message : "Could not save."));
  };
  const submit = (event: FormEvent) => {
    event.preventDefault();
    commit(draft);
  };
  const dirty = setting.write_only ? draft !== "" : draft !== setting.value;

  return (
    <form onSubmit={submit} className="flex flex-col gap-1.5">
      <div className="flex flex-wrap items-center gap-1.5">
        <label htmlFor={id} className="mr-auto text-xs font-medium text-heading">
          {setting.label}
        </label>
        <Chip tone={setting.source === "database" ? "accent" : "neutral"} title={setting.where}>
          {sourceLabels[setting.source]}
        </Chip>
        {setting.pending && <Chip tone="warning">restart to apply</Chip>}
      </div>
      <div className="flex min-w-0 items-center gap-2">
        <SettingInput id={id} setting={setting} value={draft} disabled={locked || save.isPending} onChange={setDraft} onToggle={(value) => commit(value)} />
        {setting.kind !== "bool" && setting.kind !== "choice" && !locked && (
          <Button type="submit" compact disabled={!dirty || save.isPending}>
            Save
          </Button>
        )}
        {setting.source === "database" && !locked && (
          <Button compact variant="ghost" onClick={() => commit(null)} disabled={save.isPending} title="Forget the value saved here and use the file or the default">
            Reset
          </Button>
        )}
      </div>
      <p className="text-2xs text-muted">
        {setting.help}
        {engineNow && setting.value === "auto" ? ` Now: ${engineNow}.` : ""} {applyLabels[setting.apply]}.
      </p>
      {setting.locked && <p className="text-2xs text-warning">{setting.locked}</p>}
      {error && (
        <p role="alert" className="text-2xs text-danger">
          {error}
        </p>
      )}
    </form>
  );
}

interface SettingInputProps {
  id: string;
  setting: SettingView;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
  /** Bools and choices save as soon as they change. */
  onToggle: (value: string) => void;
}

function SettingInput({ id, setting, value, disabled, onChange, onToggle }: SettingInputProps) {
  if (setting.kind === "bool") {
    const on = setting.invert ? value !== "true" : value === "true";
    return (
      <button
        id={id}
        type="button"
        role="switch"
        aria-checked={on}
        disabled={disabled}
        onClick={() => onToggle(setting.invert ? String(on) : String(!on))}
        className="inline-flex h-7 w-12 shrink-0 items-center rounded-full border border-border bg-surface-sunken p-0.5 aria-checked:border-accent aria-checked:bg-accent disabled:opacity-50"
      >
        <span className={"h-5.5 w-5.5 rounded-full bg-surface shadow transition-transform " + (on ? "translate-x-5" : "")} />
        <span className="sr-only">{on ? "On" : "Off"}</span>
      </button>
    );
  }
  if (setting.kind === "choice") {
    return (
      <select id={id} value={value} disabled={disabled} onChange={(event) => onToggle(event.target.value)} className={inputClass + " max-w-xs"}>
        {(setting.choices ?? []).map((choice) => (
          <option key={choice} value={choice}>
            {choice}
          </option>
        ))}
      </select>
    );
  }
  if (setting.kind === "secret") {
    return (
      <input
        id={id}
        type="password"
        autoComplete="off"
        placeholder={setting.is_set ? "set (write-only): type to replace" : "not set"}
        value={value}
        disabled={disabled}
        onChange={(event) => onChange(event.target.value)}
        className={inputClass}
      />
    );
  }
  return (
    <input
      id={id}
      inputMode={setting.kind === "int" ? "numeric" : undefined}
      autoCapitalize="none"
      spellCheck={false}
      placeholder={setting.default ? `default: ${setting.default}` : setting.kind === "list" ? "comma-separated" : "empty"}
      value={value}
      disabled={disabled}
      onChange={(event) => onChange(event.target.value)}
      className={inputClass}
    />
  );
}
