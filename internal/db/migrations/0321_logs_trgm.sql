-- Log search: substring matching on the scrubbed body uses a pg_trgm GIN index
-- (ILIKE '%word%'). pg_trgm is a contrib extension, trusted since PG 13, so the
-- database owner can create it; hosted deployments must grant CREATE on the
-- database to the application role before deploying (decision D94).
-- The index on the partitioned parent cascades to every daily partition and is
-- inherited by partitions created later. fastupdate keeps inserts cheap and a
-- small pending list keeps the unmerged part of the index short for readers.
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE INDEX logs_body_trgm ON logs USING gin (body gin_trgm_ops) WITH (fastupdate = on, gin_pending_list_limit = 4096);
