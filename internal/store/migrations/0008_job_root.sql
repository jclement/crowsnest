-- 0008: the directory a job's children may use without asking (R3-H1). crowsnest records it once,
-- resolved (symlinks evaluated), when it creates the job's pane, and never recomputes it from
-- paths or git configuration the job's agent could have changed since. '' grants nothing: every
-- child directory then needs the human (jobs made before this migration, the orchestrator's,
-- and a job whose directory is / or $HOME or holds $HOME).
ALTER TABLE jobs ADD COLUMN root_dir TEXT NOT NULL DEFAULT '';
