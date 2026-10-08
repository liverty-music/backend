-- Ticket wallet and venue reception (ticket-wallet-and-checkin): the ticket's
-- admitted time, the fan's wallet public key, reception links, admissions and
-- rejected scans. Admissions and rejected scans are append-only.

SET search_path TO app, public;

ALTER TABLE tickets ADD COLUMN IF NOT EXISTS admitted_at TIMESTAMPTZ;
COMMENT ON COLUMN tickets.admitted_at IS 'When the ticket was admitted at the venue; NULL until admitted. Set once together with its admissions row (conditional update on this row) and kept when the ticket is later voided.';

-- Wallet public keys: the public half of the key pair a fan's device created
-- for showing tickets. One row per user; registering another device's key
-- replaces the row in one upsert.
CREATE TABLE IF NOT EXISTS wallet_public_keys (
    user_id       UUID        PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    public_key    BYTEA       NOT NULL,
    registered_at TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_wallet_public_keys_sec1_uncompressed CHECK (octet_length(public_key) = 65 AND get_byte(public_key, 0) = 4)
);

COMMENT ON TABLE wallet_public_keys IS 'Public key (ECDSA P-256) of the device a fan shows tickets from. One per user, replaced when another device registers; AdmissionCodes are verified against it.';
COMMENT ON COLUMN wallet_public_keys.user_id IS 'The user whose device created the key (one key per user)';
COMMENT ON COLUMN wallet_public_keys.public_key IS 'Uncompressed SEC1 point 0x04 || X || Y (65 bytes); validated as a P-256 curve point by the application';
COMMENT ON COLUMN wallet_public_keys.registered_at IS 'When the device registered the key';

-- Reception links: the link an Organizer issues so that one venue-staff
-- device can admit fans to one event. Numbered per event in issue order,
-- numbers never reused. The token itself is kept only while the link is
-- unused (so the console can show its URL); lookups go through its SHA-256.
CREATE TABLE IF NOT EXISTS reception_links (
    id               UUID        PRIMARY KEY,
    event_id         UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    number           INTEGER     NOT NULL,
    token            TEXT,
    token_hash       BYTEA       NOT NULL,
    bound_public_key BYTEA,
    status           SMALLINT    NOT NULL,
    bound_at         TIMESTAMPTZ,
    revoked_at       TIMESTAMPTZ,
    CONSTRAINT chk_reception_links_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_reception_links_number_positive CHECK (number >= 1),
    CONSTRAINT chk_reception_links_status CHECK (status IN (1, 2, 3)),
    CONSTRAINT chk_reception_links_token_hash_len CHECK (octet_length(token_hash) = 32),
    CONSTRAINT chk_reception_links_token_only_unused CHECK (token IS NULL OR status = 1),
    CONSTRAINT chk_reception_links_bound_key_sec1 CHECK (bound_public_key IS NULL OR (octet_length(bound_public_key) = 65 AND get_byte(bound_public_key, 0) = 4)),
    CONSTRAINT chk_reception_links_bound_together CHECK ((bound_public_key IS NULL) = (bound_at IS NULL)),
    CONSTRAINT chk_reception_links_unused_unbound CHECK (status <> 1 OR bound_public_key IS NULL),
    CONSTRAINT chk_reception_links_in_use_bound CHECK (status <> 2 OR bound_public_key IS NOT NULL),
    CONSTRAINT chk_reception_links_revoked_time CHECK ((status = 3) = (revoked_at IS NOT NULL)),
    CONSTRAINT uq_reception_links_event_number UNIQUE (event_id, number),
    CONSTRAINT uq_reception_links_token_hash UNIQUE (token_hash)
);

COMMENT ON TABLE reception_links IS 'Organizer-issued link through which one venue-staff device admits fans to one event. Status 1=Unused, 2=InUse (bound to a device public key), 3=Revoked.';
COMMENT ON COLUMN reception_links.id IS 'Unique reception link identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN reception_links.event_id IS 'The one event the link admits to';
COMMENT ON COLUMN reception_links.number IS 'Number within the event in issue order (shown as 受付1, 受付2 ...); never reused, assigned under a lock on the event row';
COMMENT ON COLUMN reception_links.token IS 'The secret part of the link URL, kept only while the link is unused so the Organizer can copy it; cleared once bound or revoked';
COMMENT ON COLUMN reception_links.token_hash IS 'SHA-256 of the token (32 bytes); the only form a token is looked up by';
COMMENT ON COLUMN reception_links.bound_public_key IS 'Uncompressed SEC1 P-256 public key of the device the link is bound to; set once on first open';
COMMENT ON COLUMN reception_links.status IS 'Lifecycle: 1=Unused, 2=InUse, 3=Revoked';
COMMENT ON COLUMN reception_links.bound_at IS 'When a device first opened the link';
COMMENT ON COLUMN reception_links.revoked_at IS 'When the Organizer revoked the link';

-- Admissions: the permanent note that a ticket was let in. Written only in the
-- same statement as tickets.admitted_at; at most one per ticket. Append-only.
CREATE TABLE IF NOT EXISTS admissions (
    ticket_id         UUID        PRIMARY KEY REFERENCES tickets(id) ON DELETE RESTRICT,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    reception_link_id UUID        NOT NULL REFERENCES reception_links(id) ON DELETE RESTRICT,
    admitted_at       TIMESTAMPTZ NOT NULL
);

COMMENT ON TABLE admissions IS 'Append-only attendance evidence: a ticket let in at the venue, through which reception link and when. Written together with tickets.admitted_at; never changed or removed, even when the ticket is later voided.';
COMMENT ON COLUMN admissions.ticket_id IS 'The ticket let in (at most one admission per ticket)';
COMMENT ON COLUMN admissions.event_id IS 'The event of the ticket';
COMMENT ON COLUMN admissions.reception_link_id IS 'The reception link that scanned the ticket';
COMMENT ON COLUMN admissions.admitted_at IS 'When the ticket was let in; equal to tickets.admitted_at';

CREATE INDEX IF NOT EXISTS idx_admissions_reception_link_id ON admissions(reception_link_id);
COMMENT ON INDEX idx_admissions_reception_link_id IS 'Supports the reception_links foreign key and per-link admission lookups';

-- Rejected scans: the append-only note of a refused scan or of one refused
-- ticket in it. ticket_id is absent exactly for reason Forged. It carries no
-- foreign key: a genuine code may present an id that is no ticket (rejected
-- as NotHolder), and losing a rejection is acceptable while a failed batch
-- would lose all of them.
CREATE TABLE IF NOT EXISTS rejected_scans (
    id                UUID        PRIMARY KEY,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    reception_link_id UUID        NOT NULL REFERENCES reception_links(id) ON DELETE RESTRICT,
    ticket_id         UUID,
    reason            SMALLINT    NOT NULL,
    scanned_at        TIMESTAMPTZ NOT NULL,
    CONSTRAINT chk_rejected_scans_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_rejected_scans_reason CHECK (reason BETWEEN 1 AND 6),
    CONSTRAINT chk_rejected_scans_ticket_unless_forged CHECK ((reason = 1) = (ticket_id IS NULL))
);

COMMENT ON TABLE rejected_scans IS 'Append-only note of a refused scan (Forged, Expired, OtherEvent) or of one refused ticket in it (NotHolder, Voided, AlreadyAdmitted). Reason 1=Forged, 2=Expired, 3=OtherEvent, 4=NotHolder, 5=Voided, 6=AlreadyAdmitted.';
COMMENT ON COLUMN rejected_scans.id IS 'Unique rejected scan identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN rejected_scans.event_id IS 'The event of the reception link that scanned';
COMMENT ON COLUMN rejected_scans.reception_link_id IS 'The reception link that scanned';
COMMENT ON COLUMN rejected_scans.ticket_id IS 'The presented ticket the refusal is about; NULL exactly when the reason is Forged. No FK: a genuine code may present an id that is no ticket';
COMMENT ON COLUMN rejected_scans.reason IS 'Why it was refused: 1=Forged, 2=Expired, 3=OtherEvent, 4=NotHolder, 5=Voided, 6=AlreadyAdmitted';
COMMENT ON COLUMN rejected_scans.scanned_at IS 'When the scan was decided';

CREATE INDEX IF NOT EXISTS idx_rejected_scans_event_id ON rejected_scans(event_id);
COMMENT ON INDEX idx_rejected_scans_event_id IS 'Supports per-event review of rejected scans and the events foreign key';

CREATE INDEX IF NOT EXISTS idx_rejected_scans_reception_link_id ON rejected_scans(reception_link_id);
COMMENT ON INDEX idx_rejected_scans_reception_link_id IS 'Supports the reception_links foreign key';
