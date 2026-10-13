-- Single-use, hashed, expiring tokens for email verification and password resets.
--
-- Only the SHA-256 hash is stored, matching how sessions already store bearer
-- tokens, so a database leak does not hand over working links. The raw token exists
-- only in the email that was sent.
CREATE TABLE auth_tokens (
    id          uuid PRIMARY KEY,
    user_id     uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    purpose     text NOT NULL CHECK (purpose IN ('email_verification', 'password_reset')),
    token_hash  text NOT NULL UNIQUE,
    expires_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

-- Lookups are always "the live token of this purpose for this user".
CREATE INDEX auth_tokens_user_purpose_idx ON auth_tokens (user_id, purpose);

-- A user's verification state. Null means unverified. Nothing enforces this yet:
-- verification is planned as a soft nudge rather than a gate, because gating it
-- would lock out every account that already exists.
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

-- Accounts that predate verification are treated as grandfathered rather than
-- unverified, so adding the column does not retroactively mark real users as
-- needing to act.
UPDATE users SET email_verified_at = created_at;
