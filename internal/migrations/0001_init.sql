CREATE EXTENSION IF NOT EXISTS btree_gist;

CREATE TABLE users (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    role text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin')),
    is_agent boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE resources (
    id uuid PRIMARY KEY,
    name text NOT NULL,
    description text,
    timezone text NOT NULL DEFAULT 'America/Los_Angeles',
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE appointments (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    resource_id uuid NOT NULL REFERENCES resources(id),
    start_at timestamptz NOT NULL,
    end_at timestamptz NOT NULL,
    status text NOT NULL DEFAULT 'booked' CHECK (status IN ('booked', 'cancelled')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz,
    CHECK (end_at > start_at)
);

CREATE TABLE sessions (
    id uuid PRIMARY KEY,
    user_id uuid NOT NULL REFERENCES users(id),
    token_hash text NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX appointments_user_id_idx ON appointments (user_id);
CREATE UNIQUE INDEX sessions_token_hash_idx ON sessions (token_hash);

ALTER TABLE appointments
ADD CONSTRAINT no_overlapping_booked
EXCLUDE USING gist (
    resource_id WITH =,
    tstzrange(start_at, end_at, '[)') WITH &&
)
WHERE (status = 'booked');
