CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

CREATE TABLE vivint_device(
    vivint_id float NOT NULL PRIMARY KEY,
    name varchar(255) NOT NULL,
    type varchar(255) NOT NULL
);

CREATE TABLE vivint_event(
    id uuid NOT NULL DEFAULT uuid_generate_v4(),
    devices jsonb NULL,
    data jsonb NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);
