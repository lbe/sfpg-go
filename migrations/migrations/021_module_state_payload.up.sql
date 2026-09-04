-- Migration: Add payload column to module_state.
-- Stores optional JSON per module (e.g. file-processing last-run stats).

ALTER TABLE module_state ADD COLUMN payload TEXT;