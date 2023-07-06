DROP TABLE IF EXISTS users;
DROP TABLE IF EXISTS familiesschemas;
DROP TABLE IF EXISTS definitions;
DROP TABLE IF EXISTS families;
DROP TABLE IF EXISTS schemas;

DROP INDEX IF EXISTS users_name_idx;
DROP INDEX IF EXISTS families_task_unique;
DROP INDEX IF EXISTS schemas_schema_unique;
DROP INDEX IF EXISTS definitions_overridden_task_hash_unique;
