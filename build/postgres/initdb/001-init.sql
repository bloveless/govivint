CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE vivint_events(
    id uuid DEFAULT uuid_generate_v4(),
    data jsonb NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
);
