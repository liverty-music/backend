-- Add CHECK constraints to reject empty-string names on venues and related tables.
-- NOT NULL alone allows '', which is semantically invalid for display names.

ALTER TABLE venues ADD CONSTRAINT chk_venues_name_not_empty CHECK (name <> '');
ALTER TABLE venues ADD CONSTRAINT chk_venues_raw_name_not_empty CHECK (raw_name <> '');
