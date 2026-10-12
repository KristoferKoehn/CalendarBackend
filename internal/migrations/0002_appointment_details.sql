ALTER TABLE appointments
    ADD COLUMN title text NOT NULL DEFAULT 'Untitled',
    ADD COLUMN description text NOT NULL DEFAULT '';
