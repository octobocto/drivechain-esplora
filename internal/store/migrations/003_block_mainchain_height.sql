-- A sidechain header names the mainchain block it merge mined against, by hash
-- only. An index with no enforcer leaves this null.
ALTER TABLE blocks ADD COLUMN main_height INTEGER;
