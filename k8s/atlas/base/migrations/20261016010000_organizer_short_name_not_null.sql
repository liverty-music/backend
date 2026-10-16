-- reuse-fan-email-for-organizer-sign-in (design D6, task 6.1): every Organizer
-- has a short name now that the one created before short names was given
-- `e2e-test` by hand, so the column becomes required.

SET search_path TO app, public;

ALTER TABLE organizers ALTER COLUMN short_name SET NOT NULL;

COMMENT ON COLUMN organizers.short_name IS 'The Organizer''s short name: 3-32 lowercase letters, digits and hyphens, starting with a letter, unique, never changed; admin and liverty-music are reserved. Names its Organization at the identity provider';
