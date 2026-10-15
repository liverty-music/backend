-- reuse-fan-email-for-organizer-sign-in (design D6): the Organizer's short
-- name, the operator email renamed owner email, and every stored email at most
-- 165 characters, so that any address can become an Organizer identity's user
-- name "<email> (<short name>)" within the identity provider's 200 characters.
-- short_name stays nullable until the existing Organizer is given its short
-- name by hand (task 6.1); a later migration makes it NOT NULL.

SET search_path TO app, public;

ALTER TABLE organizers RENAME COLUMN operator_email TO owner_email;
ALTER TABLE organizers RENAME CONSTRAINT chk_organizers_operator_email_non_empty TO chk_organizers_owner_email_non_empty;
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_owner_email_len CHECK (char_length(owner_email) <= 165);
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_contact_email_len CHECK (contact_email IS NULL OR char_length(contact_email) <= 165);

ALTER TABLE organizers ADD COLUMN short_name TEXT;
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_short_name CHECK (short_name ~ '^[a-z][a-z0-9-]{2,31}$' AND short_name NOT IN ('admin', 'liverty-music'));
CREATE UNIQUE INDEX uq_organizers_short_name ON organizers (short_name);

ALTER TABLE users ADD CONSTRAINT chk_users_email_len CHECK (char_length(email) <= 165);

COMMENT ON COLUMN organizers.owner_email IS 'Email of the person who owns the Organizer (at most 165 characters); seeded as the owner identity of the Organizer''s Organization at the identity provider';
COMMENT ON COLUMN organizers.short_name IS 'The Organizer''s short name: 3-32 lowercase letters, digits and hyphens, starting with a letter, unique, never changed; admin and liverty-music are reserved. Names its Organization at the identity provider. NULL only for the Organizer created before short names, until it is given one';
COMMENT ON INDEX uq_organizers_short_name IS 'No two Organizers, in any status, share a short name';
COMMENT ON COLUMN organizers.contact_email IS '特商法 contact email (at most 165 characters); NULL until entered';
COMMENT ON COLUMN users.email IS 'Primary contact and login identifier (at most 165 characters)';
