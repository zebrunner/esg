CREATE TABLE IF NOT EXISTS tenants(
    id serial PRIMARY KEY,
    name VARCHAR (300),
    password VARCHAR (100) NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT true,
    updated_at TIMESTAMP,
    is_deleted BOOLEAN DEFAULT false
);
