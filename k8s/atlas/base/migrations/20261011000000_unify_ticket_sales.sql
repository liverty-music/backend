-- Unify ticket sales (unify-ticket-sales, design "Migration Plan").
--
-- A ticket sale (受付) now covers the events of one Series and offers one
-- ticket type per event; lottery phases become lottery sales; applications
-- become lottery entries and reservations become checkouts, both keyed by the
-- ticket type; the fan's full name and phone number live only on users; every
-- ticketing record names its user `user_id`; the Organizer's seller details
-- become business details; verified_identities becomes kyc.
--
-- Steps:
--   1. Reshape ticket_sales (Series, name, verification requirement, drawn
--      time, methods 1 and 2) and create ticket_types.
--   2. Move each first-come sale's per-event columns and sold count into one
--      ticket type (per-account limit capped at the quantity); name the sale
--      一般発売.
--   3. Copy each lottery_sales_phases row into one sale named 抽選 and one
--      ticket type. The ticket type reuses the phase id, so the entries keep
--      their reference without a mapping table.
--   4. ticket_applications -> lottery_entries (ticket_type_id, user_id,
--      draw_position; state 5 Withdrawn -> 4, 5 is now Draft).
--   5. reservations -> checkouts keyed by ticket_type_id.
--   6. users.holder_* -> full_name / phone_number, filling empty ones from the
--      newest application or ticket of each user.
--   7. Drop the copies of the name and phone on applications, tickets and
--      reservations.
--   8. Rename orders, tickets and organizers columns; verified_identities ->
--      kyc.
--   9. Drop lottery_sales_phases and redo the organizer-console-api grants.
--
-- Forward-only. Every statement runs in the file's single transaction, so a
-- failing CHECK leaves the database as it was.

SET search_path TO app, public;

-- ── 1. ticket_sales and ticket_types ─────────────────────────────────

ALTER TABLE ticket_sales
  DROP CONSTRAINT uq_ticket_sales_event_id,
  DROP CONSTRAINT chk_ticket_sales_method,
  ADD COLUMN series_id UUID,
  ADD COLUMN name TEXT,
  ADD COLUMN verification_requirement SMALLINT NOT NULL DEFAULT 0,
  ADD COLUMN drawn_at TIMESTAMPTZ,
  ADD CONSTRAINT chk_ticket_sales_method CHECK (method IN (1, 2));

CREATE TABLE ticket_types (
    id                UUID    PRIMARY KEY,
    ticket_sale_id    UUID    NOT NULL REFERENCES ticket_sales(id) ON DELETE CASCADE,
    event_id          UUID    NOT NULL REFERENCES events(id) ON DELETE CASCADE,
    price             BIGINT  NOT NULL,
    quantity          INTEGER NOT NULL,
    per_account_limit INTEGER NOT NULL,
    sold_count        INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT chk_ticket_types_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_types_price CHECK (price BETWEEN 1 AND 1000000),
    CONSTRAINT chk_ticket_types_quantity CHECK (quantity >= 1),
    CONSTRAINT chk_ticket_types_per_account_limit CHECK (per_account_limit BETWEEN 1 AND 10 AND per_account_limit <= quantity),
    CONSTRAINT chk_ticket_types_sold_count CHECK (sold_count BETWEEN 0 AND quantity),
    CONSTRAINT uq_ticket_types_sale_event UNIQUE (ticket_sale_id, event_id)
);

-- ── 2. First-come sales: one ticket type each ────────────────────────

-- The per-account limit is now at most the quantity (entity/ticket-type).
-- The first-come sale did not check that, and production holds one sale of
-- 5 tickets with a limit of 10 (read 2026-10-11), so the limit is lowered to
-- the quantity; with that cap no account can hold more than before.
INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit, sold_count)
SELECT uuidv7(), ts.id, ts.event_id, ts.price, ts.quantity, LEAST(ts.per_account_limit, ts.quantity), ts.sold_count
FROM ticket_sales ts;

UPDATE ticket_sales ts
SET series_id = e.series_id,
    name = '一般発売'
FROM events e
WHERE e.id = ts.event_id;

-- ── 3. Lottery phases: one sale and one ticket type each ─────────────

-- A plain table, not a TEMPORARY one: the migration tools may run the
-- statements outside one session transaction. Dropped right after use.
CREATE TABLE lottery_phase_sales AS
SELECT id AS phase_id, uuidv7() AS ticket_sale_id
FROM lottery_sales_phases;

INSERT INTO ticket_sales (id, event_id, method, sale_start_at, sale_end_at, price, quantity, per_account_limit,
                          series_id, name, verification_requirement, drawn_at)
SELECT m.ticket_sale_id, p.event_id, 2, p.open_at, p.close_at, p.ticket_price, p.ticket_capacity,
       p.max_tickets_per_application, e.series_id, '抽選', p.verification_requirement, p.drawn_at
FROM lottery_sales_phases p
JOIN lottery_phase_sales m ON m.phase_id = p.id
JOIN events e ON e.id = p.event_id;

INSERT INTO ticket_types (id, ticket_sale_id, event_id, price, quantity, per_account_limit)
SELECT p.id, m.ticket_sale_id, p.event_id, p.ticket_price, p.ticket_capacity, p.max_tickets_per_application
FROM lottery_sales_phases p
JOIN lottery_phase_sales m ON m.phase_id = p.id;

DROP TABLE lottery_phase_sales;

-- The per-event columns now live on ticket_types.
ALTER TABLE ticket_sales
  DROP COLUMN event_id,
  DROP COLUMN price,
  DROP COLUMN quantity,
  DROP COLUMN per_account_limit,
  DROP COLUMN sold_count,
  ALTER COLUMN series_id SET NOT NULL,
  ALTER COLUMN name SET NOT NULL,
  ADD CONSTRAINT ticket_sales_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE,
  ADD CONSTRAINT chk_ticket_sales_name_len CHECK (char_length(name) BETWEEN 1 AND 100),
  ADD CONSTRAINT chk_ticket_sales_verification_requirement CHECK (verification_requirement BETWEEN 0 AND 2),
  ADD CONSTRAINT chk_ticket_sales_drawn_lottery_only CHECK (drawn_at IS NULL OR method = 2);

CREATE INDEX idx_ticket_sales_series_id ON ticket_sales(series_id);
CREATE INDEX idx_ticket_types_event_id ON ticket_types(event_id);

-- ── 6. users full name and phone number (before the copies are dropped) ──

ALTER TABLE users DROP CONSTRAINT chk_users_holder_together;
ALTER TABLE users RENAME COLUMN holder_full_name TO full_name;
ALTER TABLE users RENAME COLUMN holder_phone_number TO phone_number;
ALTER TABLE users RENAME CONSTRAINT chk_users_holder_name_len TO chk_users_full_name_len;
ALTER TABLE users RENAME CONSTRAINT chk_users_holder_phone_e164 TO chk_users_phone_number_e164;

-- Newest by id: every id is a UUIDv7, so id order is creation order across
-- both tables. Name and phone come from the same row.
WITH given AS (
  SELECT applicant_id AS user_id, id, applicant_full_name AS full_name, applicant_phone_number AS phone_number
  FROM ticket_applications
  UNION ALL
  SELECT holder_id, id, holder_full_name, holder_phone_number
  FROM tickets
), newest AS (
  SELECT DISTINCT ON (user_id) user_id, full_name, phone_number
  FROM given
  ORDER BY user_id, id DESC
)
UPDATE users u
SET full_name = COALESCE(u.full_name, n.full_name),
    phone_number = COALESCE(u.phone_number, n.phone_number)
FROM newest n
WHERE n.user_id = u.id
  AND (u.full_name IS NULL OR u.phone_number IS NULL);

-- ── 4. ticket_applications -> lottery_entries ────────────────────────

DROP INDEX uq_ticket_applications_active;
ALTER TABLE ticket_applications DROP CONSTRAINT ticket_applications_phase_id_fkey;
ALTER TABLE ticket_applications DROP CONSTRAINT chk_ticket_applications_state;
ALTER TABLE ticket_applications DROP CONSTRAINT chk_ticket_applications_applicant_phone_e164;
ALTER TABLE ticket_applications
  DROP COLUMN applicant_full_name,
  DROP COLUMN applicant_phone_number;
ALTER TABLE ticket_applications RENAME TO lottery_entries;
ALTER TABLE lottery_entries RENAME CONSTRAINT ticket_applications_pkey TO lottery_entries_pkey;
ALTER TABLE lottery_entries RENAME CONSTRAINT chk_ticket_applications_id_uuidv7 TO chk_lottery_entries_id_uuidv7;
ALTER TABLE lottery_entries RENAME CONSTRAINT chk_ticket_applications_count_positive TO chk_lottery_entries_count_positive;
ALTER TABLE lottery_entries RENAME COLUMN phase_id TO ticket_type_id;
ALTER TABLE lottery_entries RENAME COLUMN applicant_id TO user_id;
ALTER TABLE lottery_entries RENAME COLUMN draw_sequence TO draw_position;
ALTER INDEX idx_ticket_applications_phase_id RENAME TO idx_lottery_entries_ticket_type_id;

-- Withdrawn moves from 5 to 4 before Draft takes 5 (design D7).
UPDATE lottery_entries SET state = 4 WHERE state = 5;

ALTER TABLE lottery_entries
  ALTER COLUMN payment_intent_ref DROP NOT NULL,
  ADD CONSTRAINT lottery_entries_ticket_type_id_fkey FOREIGN KEY (ticket_type_id) REFERENCES ticket_types(id) ON DELETE CASCADE,
  ADD CONSTRAINT chk_lottery_entries_state CHECK (state BETWEEN 1 AND 5),
  ADD CONSTRAINT chk_lottery_entries_payment_intent_ref_not_empty CHECK (payment_intent_ref IS NULL OR payment_intent_ref <> ''),
  ADD CONSTRAINT chk_lottery_entries_authorized_from_entered CHECK (payment_intent_ref IS NOT NULL OR state IN (4, 5));

CREATE UNIQUE INDEX uq_lottery_entries_active ON lottery_entries(ticket_type_id, user_id) WHERE state <> 4;

-- ── 5. reservations -> checkouts ─────────────────────────────────────

DROP INDEX uq_reservations_held;
DROP INDEX idx_reservations_sale_status;
ALTER TABLE reservations DROP CONSTRAINT reservations_ticket_sale_id_fkey;
ALTER TABLE reservations DROP CONSTRAINT chk_reservations_holder_together;
ALTER TABLE reservations DROP CONSTRAINT chk_reservations_holder_name_len;
ALTER TABLE reservations DROP CONSTRAINT chk_reservations_holder_phone_e164;
ALTER TABLE reservations
  DROP COLUMN holder_full_name,
  DROP COLUMN holder_phone_number,
  ADD COLUMN ticket_type_id UUID;

-- At this point every sale with reservations is a first-come sale with
-- exactly one ticket type.
UPDATE reservations r
SET ticket_type_id = tt.id
FROM ticket_types tt
WHERE tt.ticket_sale_id = r.ticket_sale_id;

ALTER TABLE reservations
  DROP COLUMN ticket_sale_id,
  ALTER COLUMN ticket_type_id SET NOT NULL;
ALTER TABLE reservations RENAME TO checkouts;
ALTER TABLE checkouts RENAME CONSTRAINT reservations_pkey TO checkouts_pkey;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_id_uuidv7 TO chk_checkouts_id_uuidv7;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_ticket_count TO chk_checkouts_ticket_count;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_amount_positive TO chk_checkouts_amount_positive;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_status TO chk_checkouts_status;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_authorization_ref_not_empty TO chk_checkouts_authorization_ref_not_empty;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_released_needs_ref TO chk_checkouts_released_needs_ref;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_committed_time TO chk_checkouts_committed_time;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_charged_never_ended TO chk_checkouts_charged_never_ended;
ALTER TABLE checkouts RENAME CONSTRAINT chk_reservations_completed_charged TO chk_checkouts_completed_charged;
ALTER INDEX idx_reservations_due RENAME TO idx_checkouts_due;
ALTER TABLE checkouts
  ADD CONSTRAINT checkouts_ticket_type_id_fkey FOREIGN KEY (ticket_type_id) REFERENCES ticket_types(id) ON DELETE RESTRICT;

CREATE UNIQUE INDEX uq_checkouts_held ON checkouts(ticket_type_id, user_id) WHERE status = 1;
CREATE INDEX idx_checkouts_ticket_type_status ON checkouts(ticket_type_id, status);

-- ── 7 and 8. tickets ─────────────────────────────────────────────────

ALTER TABLE tickets DROP CONSTRAINT chk_tickets_holder_phone_e164;
ALTER TABLE tickets
  DROP COLUMN holder_full_name,
  DROP COLUMN holder_phone_number;
ALTER TABLE tickets RENAME COLUMN holder_id TO user_id;
ALTER TABLE tickets RENAME COLUMN verified_identity_id TO kyc_id;
ALTER INDEX idx_tickets_holder_id RENAME TO idx_tickets_user_id;

-- ── 8. orders ────────────────────────────────────────────────────────

ALTER TABLE orders RENAME COLUMN buyer_id TO user_id;
ALTER TABLE orders RENAME COLUMN application_id TO lottery_entry_id;
ALTER TABLE orders RENAME COLUMN reservation_id TO checkout_id;
ALTER TABLE orders RENAME CONSTRAINT orders_application_id_fkey TO orders_lottery_entry_id_fkey;
ALTER TABLE orders RENAME CONSTRAINT orders_reservation_id_fkey TO orders_checkout_id_fkey;
ALTER INDEX uq_orders_application_id RENAME TO uq_orders_lottery_entry_id;
ALTER INDEX uq_orders_reservation_id RENAME TO uq_orders_checkout_id;
ALTER INDEX idx_orders_buyer_id RENAME TO idx_orders_user_id;

-- ── 8. organizers ────────────────────────────────────────────────────

ALTER TABLE organizers RENAME COLUMN seller_legal_name TO legal_name;
ALTER TABLE organizers RENAME COLUMN seller_representative_name TO representative_name;
ALTER TABLE organizers RENAME COLUMN seller_address TO business_address;
ALTER TABLE organizers RENAME COLUMN seller_phone_number TO business_phone_number;
ALTER TABLE organizers RENAME COLUMN seller_contact_email TO contact_email;
ALTER TABLE organizers RENAME CONSTRAINT chk_organizers_seller_details_together TO chk_organizers_business_details_together;
ALTER TABLE organizers RENAME CONSTRAINT chk_organizers_seller_phone_e164 TO chk_organizers_business_phone_e164;

-- ── 8. verified_identities -> kyc ────────────────────────────────────

ALTER TABLE verified_identities RENAME TO kyc;
ALTER TABLE kyc RENAME CONSTRAINT verified_identities_pkey TO kyc_pkey;
ALTER TABLE kyc RENAME CONSTRAINT verified_identities_user_id_fkey TO kyc_user_id_fkey;
ALTER TABLE kyc RENAME CONSTRAINT chk_verified_identities_id_uuidv7 TO chk_kyc_id_uuidv7;
ALTER TABLE kyc RENAME CONSTRAINT chk_verified_identities_method TO chk_kyc_method;
ALTER TABLE kyc RENAME CONSTRAINT chk_verified_identities_dedupe_strength TO chk_kyc_dedupe_strength;
ALTER TABLE kyc RENAME CONSTRAINT chk_verified_identities_status TO chk_kyc_status;
ALTER TABLE kyc RENAME CONSTRAINT chk_verified_identities_pocket_sign_user_id_non_empty TO chk_kyc_pocket_sign_user_id_non_empty;
ALTER INDEX idx_verified_identities_user_id RENAME TO idx_kyc_user_id;

-- ── 9. lottery_sales_phases ──────────────────────────────────────────

DROP TABLE lottery_sales_phases;

-- ── Comments ─────────────────────────────────────────────────────────

COMMENT ON TABLE users IS 'User profiles and authentication data';
COMMENT ON COLUMN users.full_name IS '本人確認 full name (1-200 characters) shown on the face of the user''s tickets; NULL until the user first gives it';
COMMENT ON COLUMN users.phone_number IS '本人確認 phone number in E.164 form shown on the face of the user''s tickets; NULL until the user first gives it';

COMMENT ON COLUMN organizers.legal_name IS '特商法 販売業者 legal name (1-200 characters); NULL until an admin enters the business details';
COMMENT ON COLUMN organizers.business_address IS '特商法 business address (1-300 characters); NULL until entered';
COMMENT ON COLUMN organizers.business_phone_number IS '特商法 business phone number in E.164 form; NULL until entered';
COMMENT ON COLUMN organizers.contact_email IS '特商法 contact email; NULL until entered';

COMMENT ON TABLE kyc IS 'Account-level 本人確認 (Kyc) results. One row per (user, verification attempt); the active invariant (at most one ACTIVE row per pocket_sign_user_id) is enforced by the partial unique index.';
COMMENT ON COLUMN kyc.id IS 'Unique Kyc record identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN kyc.user_id IS 'Reference to the user account this Kyc is bound to.';
COMMENT ON INDEX uq_active_pocket_sign_user_id IS 'At most one ACTIVE Kyc row per Pocket Sign person key. A second KYC_VERIFIED account for the same person is rejected here.';
COMMENT ON INDEX idx_kyc_user_id IS 'Optimizes lookup of a user''s active Kyc record.';

COMMENT ON TABLE ticket_sales IS 'One sale opportunity (受付) over the events of one Series: a name, an allocation method, a window and a verification requirement. Its per-event price, quantity and limit are ticket_types rows.';
COMMENT ON COLUMN ticket_sales.series_id IS 'The Series whose events the sale offers tickets for';
COMMENT ON COLUMN ticket_sales.name IS 'Sale name shown to fans and operators (1-100 characters), e.g. ファンクラブ先行 or 一般発売';
COMMENT ON COLUMN ticket_sales.method IS 'Allocation method: 1=FirstCome, 2=Lottery';
COMMENT ON COLUMN ticket_sales.sale_end_at IS 'When the sale closes; after sale_start_at, 1 to 14 days after it for a lottery, and before the start of every event the sale covers (checked by the application)';
COMMENT ON COLUMN ticket_sales.verification_requirement IS '本人確認 requirement: 0=NONE (any fan), 1=VERIFIED_ANY (an Active Kyc is required), 2=JPKI_ONLY (a JPKI-backed Kyc is required)';
COMMENT ON COLUMN ticket_sales.drawn_at IS 'When the lottery draw ran for every ticket type of the sale; NULL until drawn and for a first-come sale. Set atomically with the draw outcome; the draw sweeper skips sales where it is set.';
COMMENT ON INDEX idx_ticket_sales_series_id IS 'Optimizes listing the sales of a Series';

COMMENT ON TABLE ticket_types IS 'What one ticket sale offers for one event: a quantity at one tax-inclusive price with a per-account limit, and for a first-come sale the tickets sold. One per (sale, event).';
COMMENT ON COLUMN ticket_types.id IS 'Unique ticket type identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_types.ticket_sale_id IS 'The sale that offers the ticket type';
COMMENT ON COLUMN ticket_types.event_id IS 'The event the tickets admit to; an event of the sale''s Series (checked by the application)';
COMMENT ON COLUMN ticket_types.price IS 'Price of one ticket in yen, tax-inclusive (税込); for a first-come sale fixed while any ticket is sold or held';
COMMENT ON COLUMN ticket_types.quantity IS 'Tickets offered; for a first-come sale it can shrink only down to sold plus held';
COMMENT ON COLUMN ticket_types.per_account_limit IS 'Most tickets one account may hold for the event once this sale is done, counting tickets it already holds; 1 to 10 and at most the quantity';
COMMENT ON COLUMN ticket_types.sold_count IS 'Tickets on Committed and Completed checkouts (first-come only; stays 0 for a lottery); changed together with the checkout status under the ticket type row lock';
COMMENT ON INDEX idx_ticket_types_event_id IS 'Optimizes listing the sales of an event through its ticket types';

COMMENT ON TABLE lottery_entries IS 'Fan entries into the lottery of a ticket type. One row per attempt; a new entry after withdrawal creates a fresh row. State: 1=Entered, 2=Won, 3=Lost, 4=Withdrawn, 5=Draft.';
COMMENT ON COLUMN lottery_entries.id IS 'Unique lottery entry identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN lottery_entries.ticket_type_id IS 'The ticket type entered; its sale''s method is Lottery';
COMMENT ON COLUMN lottery_entries.payment_intent_ref IS 'Payment provider reference of the card hold (Stripe pi_...); NULL on a Draft until the hold is opened, and on an entry withdrawn as a Draft; required from Entered on';
COMMENT ON COLUMN lottery_entries.state IS 'Lifecycle state: 1=Entered (hold in place), 2=Won (captured), 3=Lost (hold released), 4=Withdrawn (fan-cancelled or abandoned Draft, hold released), 5=Draft (before confirmation; takes no part in the draw)';
COMMENT ON COLUMN lottery_entries.draw_position IS 'Zero-based position in the ticket type''s draw shuffle. NULL until the draw runs; used to order the loser waitlist for official-resale.';
COMMENT ON INDEX uq_lottery_entries_active IS 'At most one active (Draft/Entered/Won/Lost) entry per (ticket type, user). Withdrawn (4) is excluded so the fan can enter again. Won/Lost are included: a concluded draw outcome still uniquely identifies participation.';
COMMENT ON INDEX idx_lottery_entries_ticket_type_id IS 'Optimizes listing all entries for a given ticket type (draw batch load)';

COMMENT ON TABLE checkouts IS 'One fan''s first-come purchase in progress on a ticket type: holds a count of tickets for 15 minutes, then is committed, charged and completed, or expires or is released. Status 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released.';
COMMENT ON COLUMN checkouts.id IS 'Unique checkout identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN checkouts.ticket_type_id IS 'The ticket type the checkout buys from; its sale''s method is FirstCome';
COMMENT ON COLUMN checkouts.user_id IS 'The fan checking out (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN checkouts.ticket_count IS 'Tickets held, 1 to the ticket type''s per-account limit';
COMMENT ON COLUMN checkouts.amount IS 'Total to pay in yen: the ticket type price times ticket_count, fixed at creation';
COMMENT ON INDEX uq_checkouts_held IS 'At most one Held checkout per (ticket type, user): a concurrent second insert loses and reads the winner';
COMMENT ON INDEX idx_checkouts_ticket_type_status IS 'Optimizes the held and per-user committed counts of a ticket type';
COMMENT ON INDEX idx_checkouts_due IS 'Optimizes ListDue: lapsed holds, card holds to give back and stalled commits';

COMMENT ON TABLE orders IS 'Purchase record for one won lottery entry or one completed checkout. Created already paid from the captured payment (status 1=Paid, 2=Refunded, 3=Failed; no pending). One order covers all tickets of its source.';
COMMENT ON COLUMN orders.user_id IS 'The fan who bought the tickets (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN orders.lottery_entry_id IS 'The Won-captured lottery entry this order derives from; NULL when the source is a checkout; unique (one order per entry)';
COMMENT ON COLUMN orders.checkout_id IS 'The Committed, charged checkout this order completes; NULL when the source is a lottery entry; unique (one order per checkout)';
COMMENT ON INDEX uq_orders_lottery_entry_id IS 'One order per winning lottery entry — the issuance idempotency guard (a duplicate insert raises unique_violation, surfaced as AlreadyExists).';
COMMENT ON INDEX uq_orders_checkout_id IS 'One order per checkout — the issuance idempotency guard for checkouts (a duplicate insert raises unique_violation, surfaced as AlreadyExists).';
COMMENT ON INDEX idx_orders_user_id IS 'Optimizes listing a user''s orders';

COMMENT ON TABLE tickets IS 'Account-bound covered tickets (特定興行入場券) issued from a won lottery entry or a completed checkout. Each carries the three covered-ticket conditions: resale-without-consent prohibited, date/venue+eligible-person (event_id + user_id, whose current full name and phone number the face shows), and bound 本人確認.';
COMMENT ON COLUMN tickets.user_id IS 'The account the ticket is bound to; never changes (official resale voids the ticket and issues a new one)';
COMMENT ON COLUMN tickets.kyc_id IS 'The Kyc the ticket is bound to when its sale required verification; NULL otherwise. No FK so privacy deletion of a Kyc is independent.';
COMMENT ON INDEX idx_tickets_user_id IS 'Optimizes a user''s my-tickets view';

-- ── 9. organizer-console-api grants ──────────────────────────────────
--
-- Privileges follow a table or column through a rename; a dropped column
-- takes its column privilege with it. So after the steps above the role
-- keeps INSERT and UPDATE (sale_start_at, sale_end_at) on ticket_sales and
-- lost UPDATE on its dropped price, quantity and per_account_limit, and its
-- lottery_sales_phases grants went with the table. ticket_types inherited
-- only SELECT from the default privileges (20261008020000). The organizer
-- server's writes are now:
--
--   ticket_sales — TicketSale Create (INSERT), SetWindow (sale_start_at,
--                  sale_end_at) and SetVerificationRequirement
--                  (verification_requirement).
--   ticket_types — created with their sale (INSERT); TicketType Update
--                  changes price, quantity and per_account_limit. The column
--                  privilege also covers its row lock (SELECT ... FOR UPDATE).
--
-- sold_count and drawn_at stay read-only for the role: only checkouts (the
-- fan server) and the draw (the backend jobs) move them. lottery_entries and
-- checkouts stay SELECT-only.
DO $$
DECLARE
  iam_role TEXT;
BEGIN
  FOR iam_role IN
    SELECT rolname FROM pg_roles WHERE rolname LIKE 'organizer-console-api@%.iam'
  LOOP
    EXECUTE format('GRANT INSERT ON app.ticket_sales TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (sale_start_at, sale_end_at, verification_requirement) ON app.ticket_sales TO %I', iam_role);
    EXECUTE format('GRANT INSERT ON app.ticket_types TO %I', iam_role);
    EXECUTE format('GRANT UPDATE (price, quantity, per_account_limit) ON app.ticket_types TO %I', iam_role);
  END LOOP;
END
$$;
