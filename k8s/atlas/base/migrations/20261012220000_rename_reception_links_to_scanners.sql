-- Rename reception links to Scanners (OpenSpec change rename-reception-to-scanner, design D8).
--
-- An in-place rename: rows, ids and link tokens are untouched, so issued links
-- and bound devices keep working. Table and column privileges are stored on
-- the objects, not on their names, so the reception-api and
-- organizer-console-api grants carry over without a new grant migration.

SET search_path TO app, public;

-- Table and columns
ALTER TABLE reception_links RENAME TO scanners;
ALTER TABLE scanners RENAME COLUMN token TO link_token;
ALTER TABLE scanners RENAME COLUMN token_hash TO link_token_hash;
ALTER TABLE admissions RENAME COLUMN reception_link_id TO scanner_id;
ALTER TABLE rejected_scans RENAME COLUMN reception_link_id TO scanner_id;

-- Constraints of scanners (renaming the primary key and unique constraints
-- renames their indexes too)
ALTER TABLE scanners RENAME CONSTRAINT reception_links_pkey TO scanners_pkey;
ALTER TABLE scanners RENAME CONSTRAINT reception_links_event_id_fkey TO scanners_event_id_fkey;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_id_uuidv7 TO chk_scanners_id_uuidv7;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_number_positive TO chk_scanners_number_positive;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_status TO chk_scanners_status;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_token_hash_len TO chk_scanners_link_token_hash_len;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_token_only_unused TO chk_scanners_link_token_only_unused;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_bound_key_sec1 TO chk_scanners_bound_key_sec1;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_bound_together TO chk_scanners_bound_together;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_unused_unbound TO chk_scanners_unused_unbound;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_in_use_bound TO chk_scanners_in_use_bound;
ALTER TABLE scanners RENAME CONSTRAINT chk_reception_links_revoked_time TO chk_scanners_revoked_time;
ALTER TABLE scanners RENAME CONSTRAINT uq_reception_links_event_number TO uq_scanners_event_number;
ALTER TABLE scanners RENAME CONSTRAINT uq_reception_links_token_hash TO uq_scanners_link_token_hash;

-- Foreign keys and indexes that name the Scanner
ALTER TABLE admissions RENAME CONSTRAINT admissions_reception_link_id_fkey TO admissions_scanner_id_fkey;
ALTER TABLE rejected_scans RENAME CONSTRAINT rejected_scans_reception_link_id_fkey TO rejected_scans_scanner_id_fkey;
ALTER INDEX idx_admissions_reception_link_id RENAME TO idx_admissions_scanner_id;
ALTER INDEX idx_rejected_scans_reception_link_id RENAME TO idx_rejected_scans_scanner_id;

-- Comments
COMMENT ON TABLE scanners IS 'One venue-staff device''s right, issued by the Organizer, to admit fans to one event. Status 1=Unused, 2=InUse (bound to a device public key), 3=Revoked.';
COMMENT ON COLUMN scanners.id IS 'Unique scanner identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN scanners.event_id IS 'The one event the scanner admits to';
COMMENT ON COLUMN scanners.link_token IS 'The secret part of the scanner''s link, kept only while the scanner is unused so the Organizer can copy the link; cleared once bound or revoked';
COMMENT ON COLUMN scanners.link_token_hash IS 'SHA-256 of the link token (32 bytes); the only form a link token is looked up by';
COMMENT ON COLUMN scanners.bound_public_key IS 'Uncompressed SEC1 P-256 public key of the device the scanner is bound to; set once on first open';
COMMENT ON COLUMN scanners.bound_at IS 'When a device first opened the scanner''s link';
COMMENT ON COLUMN scanners.revoked_at IS 'When the Organizer revoked the scanner';

COMMENT ON TABLE admissions IS 'Append-only attendance evidence: a ticket let in at the venue, through which scanner and when. Written together with tickets.admit_at; never changed or removed, even when the ticket is later voided.';
COMMENT ON COLUMN admissions.scanner_id IS 'The scanner that scanned the ticket';
COMMENT ON INDEX idx_admissions_scanner_id IS 'Supports the scanners foreign key and per-scanner admission lookups';

COMMENT ON COLUMN rejected_scans.event_id IS 'The event of the scanner that scanned';
COMMENT ON COLUMN rejected_scans.scanner_id IS 'The scanner that scanned';
COMMENT ON INDEX idx_rejected_scans_scanner_id IS 'Supports the scanners foreign key';
