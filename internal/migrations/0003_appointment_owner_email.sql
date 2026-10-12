-- Let users be deleted while their appointments survive.
--
-- Deleting a user used to require having no appointments, because
-- appointments.user_id had a NOT NULL foreign key to users. That made a single
-- cancelled booking pin its owner's account permanently: there was no other way
-- to remove an appointment through the API.
--
-- Instead, the owner's email is denormalised onto the appointment so the record
-- stays meaningful after the user row is gone, and user_id becomes a historical
-- reference rather than a constraint. New appointments still populate user_id,
-- so filtering by owner keeps working.
--
-- Both foreign keys into users are dropped. sessions also references users, so
-- dropping only the appointments constraint would still block the delete. The
-- application already removes a user's sessions before deleting the row, so
-- nothing depends on that constraint.
--
-- The constraints are located through the system catalogue rather than assumed
-- names, so this works regardless of how they were originally named.

ALTER TABLE appointments ADD COLUMN owner_email text NOT NULL DEFAULT '';

UPDATE appointments a
SET owner_email = u.email
FROM users u
WHERE u.id = a.user_id;

ALTER TABLE appointments ALTER COLUMN owner_email DROP DEFAULT;

ALTER TABLE appointments ALTER COLUMN user_id DROP NOT NULL;

DO $$
DECLARE
    con record;
BEGIN
    FOR con IN
        SELECT c.conname, c.conrelid::regclass AS tbl
        FROM pg_constraint c
        WHERE c.contype = 'f'
          AND c.confrelid = 'users'::regclass
          AND c.conrelid IN ('appointments'::regclass, 'sessions'::regclass)
    LOOP
        EXECUTE format('ALTER TABLE %s DROP CONSTRAINT %I', con.tbl, con.conname);
    END LOOP;
END $$;
