-- First-come ticket sales (first-come-ticket-sales): the first-party sale of an
-- event's tickets, its 15-minute checkouts (reservations), an Order source that
-- can be a reservation, the Organizer's 特商法 seller details and platform fee
-- rate, and the fan's holder identity.

SET search_path TO app, public;

-- Ticket sales: one first-come sale per event. sold_count holds the tickets of
-- the sale's Committed and Completed reservations; held tickets are derived
-- from the Held reservations whose hold has not expired.
CREATE TABLE IF NOT EXISTS ticket_sales (
    id                UUID        PRIMARY KEY,
    event_id          UUID        NOT NULL REFERENCES events(id) ON DELETE RESTRICT,
    method            SMALLINT    NOT NULL,
    sale_start_at     TIMESTAMPTZ NOT NULL,
    sale_end_at       TIMESTAMPTZ NOT NULL,
    price             BIGINT      NOT NULL,
    quantity          INTEGER     NOT NULL,
    per_account_limit INTEGER     NOT NULL,
    sold_count        INTEGER     NOT NULL DEFAULT 0,
    CONSTRAINT chk_ticket_sales_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_ticket_sales_method CHECK (method IN (1)),
    CONSTRAINT chk_ticket_sales_window CHECK (sale_end_at > sale_start_at),
    CONSTRAINT chk_ticket_sales_price CHECK (price BETWEEN 1 AND 1000000),
    CONSTRAINT chk_ticket_sales_quantity CHECK (quantity >= 1),
    CONSTRAINT chk_ticket_sales_per_account_limit CHECK (per_account_limit BETWEEN 1 AND 10),
    CONSTRAINT chk_ticket_sales_sold_count CHECK (sold_count BETWEEN 0 AND quantity),
    CONSTRAINT uq_ticket_sales_event_id UNIQUE (event_id)
);

COMMENT ON TABLE ticket_sales IS 'First-party first-come sale of one event''s tickets: a quantity at one tax-inclusive price during a sale window, with a per-account limit. One per event.';
COMMENT ON COLUMN ticket_sales.id IS 'Unique ticket sale identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN ticket_sales.event_id IS 'The event whose tickets are sold; one sale per event';
COMMENT ON COLUMN ticket_sales.method IS 'Allocation method: 1=FirstCome';
COMMENT ON COLUMN ticket_sales.sale_start_at IS 'When the sale opens';
COMMENT ON COLUMN ticket_sales.sale_end_at IS 'When the sale closes; after sale_start_at and no later than the event start (checked by the application)';
COMMENT ON COLUMN ticket_sales.price IS 'Price of one ticket in yen, tax-inclusive (税込); fixed once any reservation exists';
COMMENT ON COLUMN ticket_sales.quantity IS 'Tickets offered; can shrink only down to sold plus held';
COMMENT ON COLUMN ticket_sales.per_account_limit IS 'Most tickets one user may hold or have bought from the sale';
COMMENT ON COLUMN ticket_sales.sold_count IS 'Tickets on Committed and Completed reservations; changed together with the reservation status under the sale row lock';

-- Reservations: one fan's checkout on a ticket sale. A hold lasts exactly 15
-- minutes and is never extended. A charged reservation (capture_at set) is
-- never Expired or Released.
CREATE TABLE IF NOT EXISTS reservations (
    id                        UUID        PRIMARY KEY,
    ticket_sale_id            UUID        NOT NULL REFERENCES ticket_sales(id) ON DELETE RESTRICT,
    user_id                   UUID        NOT NULL,
    ticket_count              INTEGER     NOT NULL,
    amount                    BIGINT      NOT NULL,
    holder_full_name          TEXT,
    holder_phone_number       TEXT,
    authorization_ref         TEXT,
    authorization_released_at TIMESTAMPTZ,
    status                    SMALLINT    NOT NULL,
    hold_expire_at            TIMESTAMPTZ NOT NULL,
    committed_at              TIMESTAMPTZ,
    capture_at                TIMESTAMPTZ,
    CONSTRAINT chk_reservations_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_reservations_ticket_count CHECK (ticket_count BETWEEN 1 AND 10),
    CONSTRAINT chk_reservations_amount_positive CHECK (amount > 0),
    CONSTRAINT chk_reservations_status CHECK (status BETWEEN 1 AND 5),
    CONSTRAINT chk_reservations_holder_together CHECK ((holder_full_name IS NULL) = (holder_phone_number IS NULL)),
    CONSTRAINT chk_reservations_holder_name_len CHECK (holder_full_name IS NULL OR char_length(holder_full_name) BETWEEN 1 AND 200),
    CONSTRAINT chk_reservations_holder_phone_e164 CHECK (holder_phone_number IS NULL OR holder_phone_number ~ '^\+[1-9][0-9]{1,14}$'),
    CONSTRAINT chk_reservations_authorization_ref_not_empty CHECK (authorization_ref IS NULL OR authorization_ref <> ''),
    CONSTRAINT chk_reservations_released_needs_ref CHECK (authorization_released_at IS NULL OR authorization_ref IS NOT NULL),
    CONSTRAINT chk_reservations_committed_time CHECK (status NOT IN (2, 3) OR committed_at IS NOT NULL),
    CONSTRAINT chk_reservations_charged_never_ended CHECK (capture_at IS NULL OR status IN (2, 3)),
    CONSTRAINT chk_reservations_completed_charged CHECK (status <> 3 OR capture_at IS NOT NULL)
);

COMMENT ON TABLE reservations IS 'One fan''s checkout on a ticket sale: holds a count of tickets for 15 minutes, then is committed, charged and completed, or expires or is released. Status 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released.';
COMMENT ON COLUMN reservations.id IS 'Unique reservation identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN reservations.ticket_sale_id IS 'The sale the checkout buys from';
COMMENT ON COLUMN reservations.user_id IS 'The fan checking out (no FK to survive user lifecycle independently)';
COMMENT ON COLUMN reservations.ticket_count IS 'Tickets held, 1 to the sale''s per-account limit';
COMMENT ON COLUMN reservations.amount IS 'Total to pay in yen: the sale price times ticket_count, fixed at creation';
COMMENT ON COLUMN reservations.holder_full_name IS '本人確認 name for the tickets'' face; NULL until the fan authorizes';
COMMENT ON COLUMN reservations.holder_phone_number IS '本人確認 phone in E.164 form; NULL until the fan authorizes';
COMMENT ON COLUMN reservations.authorization_ref IS 'Payment provider reference of the card hold (Stripe pi_...); NULL until the fan authorizes, then set once';
COMMENT ON COLUMN reservations.authorization_released_at IS 'When the card hold was given back without a charge; NULL unless released';
COMMENT ON COLUMN reservations.status IS 'Lifecycle: 1=Held, 2=Committed, 3=Completed, 4=Expired, 5=Released';
COMMENT ON COLUMN reservations.hold_expire_at IS 'When the hold lapses: 15 minutes after the checkout started, never extended; a commit needs hold_expire_at > now';
COMMENT ON COLUMN reservations.committed_at IS 'When the tickets were committed against the stock; kept when an uncharged commit is reverted';
COMMENT ON COLUMN reservations.capture_at IS 'When the card hold (authorization_ref) was charged; set once, and a row with it is never Expired or Released';

CREATE UNIQUE INDEX IF NOT EXISTS uq_reservations_held ON reservations(ticket_sale_id, user_id) WHERE status = 1;
COMMENT ON INDEX uq_reservations_held IS 'At most one Held reservation per (sale, user): a concurrent second insert loses and reads the winner';

CREATE INDEX IF NOT EXISTS idx_reservations_sale_status ON reservations(ticket_sale_id, status);
COMMENT ON INDEX idx_reservations_sale_status IS 'Optimizes the held and per-user committed counts of a sale';

CREATE INDEX IF NOT EXISTS idx_reservations_due ON reservations(status, hold_expire_at) WHERE status IN (1, 2) OR (status IN (4, 5) AND authorization_ref IS NOT NULL AND authorization_released_at IS NULL);
COMMENT ON INDEX idx_reservations_due IS 'Optimizes ListDue: lapsed holds, card holds to give back and stalled commits';

-- Orders: an order comes from exactly one source, a won application or a
-- Committed, charged reservation, with one order per source.
ALTER TABLE orders ALTER COLUMN application_id DROP NOT NULL;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS reservation_id UUID REFERENCES reservations(id) ON DELETE RESTRICT;
ALTER TABLE orders ADD COLUMN IF NOT EXISTS confirmation_sent_at TIMESTAMPTZ;
ALTER TABLE orders ADD CONSTRAINT chk_orders_one_source CHECK ((application_id IS NULL) <> (reservation_id IS NULL));

COMMENT ON TABLE orders IS 'Purchase record for one won lottery application or one completed checkout (reservation). Created already paid from the captured payment (status 1=Paid, 2=Refunded, 3=Failed; no pending). One order covers all tickets of its source.';
COMMENT ON COLUMN orders.application_id IS 'The Won-captured application this order derives from; NULL when the source is a reservation; unique (one order per application)';
COMMENT ON COLUMN orders.reservation_id IS 'The Committed, charged reservation this order completes; NULL when the source is an application; unique (one order per reservation)';
COMMENT ON COLUMN orders.confirmation_sent_at IS 'When the purchase confirmation email was sent; NULL until sent. Set right after a successful send so a redelivered event sends nothing.';
COMMENT ON COLUMN orders.paid_at IS 'When the order was recorded as paid (after the draw''s capture or the checkout''s charge)';

CREATE UNIQUE INDEX IF NOT EXISTS uq_orders_reservation_id ON orders(reservation_id);
COMMENT ON INDEX uq_orders_reservation_id IS 'One order per reservation — the issuance idempotency guard for checkouts (a duplicate insert raises unique_violation, surfaced as AlreadyExists).';

-- Organizers: 特商法 seller details, entered all at once by an admin, and the
-- platform fee rate. Existing organizers keep the 5% they were charged so far;
-- new organizers start at 8%.
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS seller_legal_name TEXT;
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS seller_representative_name TEXT;
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS seller_address TEXT;
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS seller_phone_number TEXT;
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS seller_contact_email TEXT;
ALTER TABLE organizers ADD COLUMN IF NOT EXISTS platform_fee_rate_bps INTEGER NOT NULL DEFAULT 500;
ALTER TABLE organizers ALTER COLUMN platform_fee_rate_bps SET DEFAULT 800;
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_platform_fee_rate_bps CHECK (platform_fee_rate_bps BETWEEN 0 AND 3000);
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_seller_details_together CHECK ((seller_legal_name IS NULL AND seller_representative_name IS NULL AND seller_address IS NULL AND seller_phone_number IS NULL AND seller_contact_email IS NULL) OR (seller_legal_name IS NOT NULL AND seller_representative_name IS NOT NULL AND seller_address IS NOT NULL AND seller_phone_number IS NOT NULL AND seller_contact_email IS NOT NULL));
ALTER TABLE organizers ADD CONSTRAINT chk_organizers_seller_phone_e164 CHECK (seller_phone_number IS NULL OR seller_phone_number ~ '^\+[1-9][0-9]{1,14}$');

COMMENT ON COLUMN organizers.seller_legal_name IS '特商法 seller legal name (1-200 characters); NULL until an admin enters the seller details';
COMMENT ON COLUMN organizers.seller_representative_name IS '特商法 representative or responsible person (1-100 characters); NULL until entered';
COMMENT ON COLUMN organizers.seller_address IS '特商法 seller address (1-300 characters); NULL until entered';
COMMENT ON COLUMN organizers.seller_phone_number IS '特商法 seller phone in E.164 form; NULL until entered';
COMMENT ON COLUMN organizers.seller_contact_email IS '特商法 seller contact email; NULL until entered';
COMMENT ON COLUMN organizers.platform_fee_rate_bps IS 'Platform fee applied to future orders, in basis points (0-3000). 800 for a new organizer; organizers that existed before per-organizer rates keep 500.';

-- Users: the 本人確認 name and phone the fan last checked out with, kept to
-- prefill the next checkout.
ALTER TABLE users ADD COLUMN IF NOT EXISTS holder_full_name TEXT;
ALTER TABLE users ADD COLUMN IF NOT EXISTS holder_phone_number TEXT;
ALTER TABLE users ADD CONSTRAINT chk_users_holder_together CHECK ((holder_full_name IS NULL) = (holder_phone_number IS NULL));
ALTER TABLE users ADD CONSTRAINT chk_users_holder_name_len CHECK (holder_full_name IS NULL OR char_length(holder_full_name) BETWEEN 1 AND 200);
ALTER TABLE users ADD CONSTRAINT chk_users_holder_phone_e164 CHECK (holder_phone_number IS NULL OR holder_phone_number ~ '^\+[1-9][0-9]{1,14}$');

COMMENT ON COLUMN users.holder_full_name IS '本人確認 name the user last checked out with; NULL until the first checkout';
COMMENT ON COLUMN users.holder_phone_number IS '本人確認 phone in E.164 form the user last checked out with; NULL until the first checkout';
