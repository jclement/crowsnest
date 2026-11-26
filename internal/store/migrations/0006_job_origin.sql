-- 0006: who created a job (R2-H1). Jobs an agent created are limited (depth, fan-out, a global
-- cap on live ones) and confined to the creator's directory; the human's are not. The column
-- says which is which: '' for the human (the browser's text box, and every job made before this
-- migration), 'agent' for a job agent's job_create, 'orchestrator' for the orchestrator's.
ALTER TABLE jobs ADD COLUMN origin TEXT NOT NULL DEFAULT '';
CREATE INDEX jobs_parent ON jobs (parent_id);
