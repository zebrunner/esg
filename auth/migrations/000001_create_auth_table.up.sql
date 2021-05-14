CREATE TABLE IF NOT EXISTS tenants(
    id serial PRIMARY KEY,
    name VARCHAR (300),
    password VARCHAR (100) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT true,
    updated_at TIMESTAMP,
    deleted_at TIMESTAMP
);

CREATE INDEX mult_col_idx_tenants ON tenants(name, deleted_at);
