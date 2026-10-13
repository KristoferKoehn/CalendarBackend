-- Optional profile details for users.
--
-- All of these are nullable and self-service: a user edits their own profile via
-- PATCH /auth/me, and every authenticated user may read another user's profile.
--
-- Birthday is stored as month and day only, with no year. That supports birthday
-- recognition without holding a full date of birth, which keeps the most
-- sensitive part of it out of the database entirely. Both parts are validated at
-- the database level as well as in the application, and they are constrained to
-- be present or absent together.
--
-- display_name is the human label used on appointments and in the directory. When
-- it is unset, readers fall back to the account's email, which is also the
-- durable label kept on an appointment after its owner is deleted.

ALTER TABLE users
    ADD COLUMN display_name text,
    ADD COLUMN birth_month  smallint,
    ADD COLUMN birth_day    smallint,
    ADD COLUMN pronouns     text,
    ADD COLUMN job_title    text,
    ADD COLUMN bio          text;

ALTER TABLE users
    ADD CONSTRAINT users_birth_month_range CHECK (birth_month BETWEEN 1 AND 12),
    ADD CONSTRAINT users_birth_day_range   CHECK (birth_day BETWEEN 1 AND 31),
    ADD CONSTRAINT users_birthday_paired   CHECK (
        (birth_month IS NULL AND birth_day IS NULL)
        OR (birth_month IS NOT NULL AND birth_day IS NOT NULL)
    );

-- A name is looked up for every appointment list, so index the non-null values.
CREATE INDEX users_display_name_idx ON users (display_name) WHERE display_name IS NOT NULL;

-- Appointments also carry the owner's name at the time of booking, alongside the
-- email they already store. This mirrors how owner_email works: the row stays
-- readable as a record even if the owner later renames themselves or is deleted.
-- It is nullable so the email remains the fallback label.
ALTER TABLE appointments ADD COLUMN owner_name text;
