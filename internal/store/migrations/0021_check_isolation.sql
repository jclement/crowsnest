-- 0021: what a check's evidence was made in (DESIGN.md 6.15, R7-C01).
--
-- job_checks.mode says where crowsnest ran the check: 'worktree' (the job's own checkout, the
-- default: it has the dependencies and caches a check needs) or 'isolated' (a temporary detached
-- worktree of exactly the pinned commit, made and removed by crowsnest). tree_before and
-- tree_after are content fingerprints of the job's checkout (its tracked and untracked files as
-- a git tree) when a worktree-mode check started and ended; a run where they differ is not
-- evidence. Older rows are worktree checks without fingerprints.
--
-- projects.check_isolated turns the isolated mode on for a project's jobs; check_prepare is the
-- command run in the isolated copy before the check (installing dependencies), '' for none.
ALTER TABLE job_checks ADD COLUMN mode TEXT NOT NULL DEFAULT 'worktree';
ALTER TABLE job_checks ADD COLUMN tree_before TEXT NOT NULL DEFAULT '';
ALTER TABLE job_checks ADD COLUMN tree_after TEXT NOT NULL DEFAULT '';
ALTER TABLE projects ADD COLUMN check_isolated INTEGER NOT NULL DEFAULT 0;
ALTER TABLE projects ADD COLUMN check_prepare TEXT NOT NULL DEFAULT '';
