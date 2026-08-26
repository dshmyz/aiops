-- Remove the environment alias table. The environment differentiation concept
-- was retired: roles are no longer scoped per environment, and the alias
-- expander that mapped user-friendly names (e.g. "生产") to canonical
-- environments during authentication has been removed. Existing deployments
-- that applied 009_environment_aliases.sql must drop the orphaned table so the
-- schema no longer carries the retired concept.
DROP TABLE IF EXISTS copilot_environment_aliases;
