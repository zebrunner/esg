CREATE TABLE IF NOT EXISTS users(
    id serial PRIMARY KEY,
    name VARCHAR (300),
    password VARCHAR (100) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT true,
    updated_at TIMESTAMP,
    is_deleted BOOLEAN DEFAULT false
);

CREATE TABLE IF NOT EXISTS families(
    family_id SERIAL PRIMARY KEY,
    task_family VARCHAR (100) NOT NULL
);

CREATE TABLE IF NOT EXISTS schemas(
    schema_id SERIAL PRIMARY KEY,
    schema VARCHAR(100) NOT NULL
);

CREATE TABLE IF NOT EXISTS familiesSchemas(
    family_id INTEGER NOT NULL,
    schema_id INTEGER NOT NULL,
    FOREIGN KEY (family_id) REFERENCES families (family_id),
    FOREIGN KEY (schema_id) REFERENCES schemas (schema_id)
);

CREATE TABLE IF NOT EXISTS definitions(
    registered_task_hash VARCHAR(64) PRIMARY KEY NOT NULL,
    tag INTEGER NOT NULL,
    updated_at TIMESTAMP,
    overridden_task_hash VARCHAR(64) NOT NULL,
    schema_id INTEGER NOT NULL,
    FOREIGN KEY (schema_id) REFERENCES schemas (schema_id)
);

CREATE UNIQUE INDEX users_name_idx ON users (name) WHERE NOT is_deleted;
CREATE UNIQUE INDEX families_task_unique ON families (task_family)
CREATE UNIQUE INDEX schemas_schema_unique ON schemas (schema)
CREATE UNIQUE INDEX definitions_overridden_task_hash_unique ON schemas (overridden_task_hash)
