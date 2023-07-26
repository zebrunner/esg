CREATE TABLE IF NOT EXISTS families(
    family_id SERIAL PRIMARY KEY,
    task_family VARCHAR (100) NOT NULL
);

CREATE TABLE IF NOT EXISTS schemas(
    schema_id SERIAL PRIMARY KEY,
    schema VARCHAR(100) NOT NULL
);

CREATE TABLE IF NOT EXISTS definitions(
    register_definition_hash VARCHAR(16) PRIMARY KEY NOT NULL,
    revision_tag INTEGER NOT NULL,
    updated_at TIMESTAMP,
    override_definition_hash VARCHAR(16) NOT NULL,
    family_id INTEGER NOT NULL,
    schema_id INTEGER NOT NULL,
    FOREIGN KEY (family_id) REFERENCES families (family_id),
    FOREIGN KEY (schema_id) REFERENCES schemas (schema_id)
);

CREATE UNIQUE INDEX IF NOT EXISTS families_task_unique ON families (task_family);
CREATE UNIQUE INDEX IF NOT EXISTS schemas_schema_unique ON schemas (schema);
CREATE UNIQUE INDEX IF NOT EXISTS definitions_override_hash_unique ON definitions (override_definition_hash);
