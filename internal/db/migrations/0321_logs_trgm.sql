-- Log search: substring matching on the scrubbed body uses a pg_trgm GIN index
-- (ILIKE '%word%'). pg_trgm is a contrib extension, trusted since PG 13, so the
-- database owner can create it; hosted deployments must grant CREATE on the
-- database to the application role before deploying (decision D94).
-- The extension goes into public when the role may create there, otherwise into
-- the current schema. An existing install in any schema is reused, so the
-- operator class is looked up and schema-qualified rather than resolved through
-- search_path (installs that run Supermetry in its own schema, and tests that
-- use one schema per test, would not see it otherwise).
-- The index on the partitioned parent cascades to every daily partition and is
-- inherited by partitions created later. fastupdate keeps inserts cheap and a
-- small pending list keeps the unmerged part of the index short for readers.
DO $$
DECLARE
	opc_schema text;
BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'pg_trgm') THEN
		BEGIN
			CREATE EXTENSION pg_trgm WITH SCHEMA public;
		EXCEPTION WHEN insufficient_privilege OR invalid_schema_name THEN
			CREATE EXTENSION pg_trgm;
		END;
	END IF;

	SELECT n.nspname INTO opc_schema
	FROM pg_opclass c
	JOIN pg_namespace n ON n.oid = c.opcnamespace
	JOIN pg_am am ON am.oid = c.opcmethod
	WHERE c.opcname = 'gin_trgm_ops' AND am.amname = 'gin';

	EXECUTE format(
		'CREATE INDEX logs_body_trgm ON logs USING gin (body %I.gin_trgm_ops) WITH (fastupdate = on, gin_pending_list_limit = 4096)',
		opc_schema);
END
$$;
