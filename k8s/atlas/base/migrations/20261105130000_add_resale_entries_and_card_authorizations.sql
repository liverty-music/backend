-- resell-tickets-by-lottery: official resale is applied for through a
-- ResaleEntry and allocated by a daily draw (design D1-D4), and a card
-- authorization gets a record of its own with a UUIDv7 id, the payment
-- provider's ID and the expiry the draw reads (design D5, D11).
--
-- Existing orders name the payment provider's ID; each gets a card_authorizations
-- row (its id is a random UUID marked as version 7, which the id check accepts;
-- only new rows are time-ordered) and the order is pointed at it.

SET search_path TO app, public;

CREATE TABLE card_authorizations (
    id                             UUID        PRIMARY KEY,
    provider_card_authorization_id TEXT        NOT NULL,
    expire_at                      TIMESTAMPTZ,
    CONSTRAINT chk_card_authorizations_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_card_authorizations_provider_id CHECK (char_length(provider_card_authorization_id) BETWEEN 1 AND 255)
);
COMMENT ON TABLE card_authorizations IS 'The platform''s record of a card authorization on a fan''s card for the amount of one purchase (a checkout, a lottery entry or a resale entry). The purchase, amount, card brand and status are read from the payment provider; this row gives it a platform id and keeps the expiry a resale draw reads.';
COMMENT ON COLUMN card_authorizations.id IS 'Unique card authorization identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN card_authorizations.provider_card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); unique';
COMMENT ON COLUMN card_authorizations.expire_at IS 'When the authorized amount can no longer be charged (Stripe capture_before). NULL until recorded; recorded when a resale entry is made from the card authorization, since only the resale draw reads it';

CREATE UNIQUE INDEX uq_card_authorizations_provider_id ON card_authorizations(provider_card_authorization_id);
COMMENT ON INDEX uq_card_authorizations_provider_id IS 'One record per payment provider card authorization; the lookup of a provider notice';

INSERT INTO card_authorizations (id, provider_card_authorization_id)
SELECT overlay(gen_random_uuid()::text PLACING '7' FROM 15 FOR 1)::uuid, card_authorization_id
FROM orders
WHERE card_authorization_id IS NOT NULL;

ALTER TABLE orders ADD COLUMN card_authorization_uuid UUID;
UPDATE orders o SET card_authorization_uuid = ca.id
FROM card_authorizations ca
WHERE ca.provider_card_authorization_id = o.card_authorization_id;

DROP INDEX uq_orders_card_authorization_id;
ALTER TABLE orders
    DROP CONSTRAINT chk_orders_paid_card_authorization,
    DROP CONSTRAINT chk_orders_card_authorization_id_not_empty,
    DROP COLUMN card_authorization_id;
ALTER TABLE orders RENAME COLUMN card_authorization_uuid TO card_authorization_id;
ALTER TABLE orders
    ADD CONSTRAINT orders_card_authorization_id_fkey FOREIGN KEY (card_authorization_id) REFERENCES card_authorizations(id) ON DELETE RESTRICT,
    ADD CONSTRAINT chk_orders_paid_card_authorization CHECK (status NOT IN (1, 2) OR card_authorization_id IS NOT NULL);
COMMENT ON COLUMN orders.card_authorization_id IS 'The card authorization on the fan''s card for the amount; NULL until the fan authorizes, then set once; unique';

CREATE UNIQUE INDEX uq_orders_card_authorization_id ON orders(card_authorization_id) WHERE card_authorization_id IS NOT NULL;
COMMENT ON INDEX uq_orders_card_authorization_id IS 'At most one order per card authorization; the lookup of a lottery entry, checkout or resale entry by its card authorization';

CREATE TABLE resale_entries (
    id                  UUID        PRIMARY KEY,
    order_id            UUID        NOT NULL REFERENCES orders(id) ON DELETE RESTRICT,
    state               SMALLINT    NOT NULL,
    lost_reason         SMALLINT,
    created_at          TIMESTAMPTZ NOT NULL,
    draw_at             TIMESTAMPTZ,
    withdraw_at         TIMESTAMPTZ,
    ended_email_send_at TIMESTAMPTZ,
    CONSTRAINT chk_resale_entries_id_uuidv7 CHECK (substring(id::text, 15, 1) = '7'),
    CONSTRAINT chk_resale_entries_state CHECK (state BETWEEN 1 AND 5),
    CONSTRAINT chk_resale_entries_lost_reason CHECK ((state = 3 AND lost_reason BETWEEN 1 AND 4) OR (state <> 3 AND lost_reason IS NULL)),
    CONSTRAINT chk_resale_entries_draw_time CHECK ((state IN (2, 3, 4)) = (draw_at IS NOT NULL)),
    CONSTRAINT chk_resale_entries_withdraw_time CHECK ((state = 5) = (withdraw_at IS NOT NULL)),
    CONSTRAINT chk_resale_entries_ended_email CHECK (ended_email_send_at IS NULL OR state IN (3, 4))
);
COMMENT ON TABLE resale_entries IS 'One fan''s application, through its order, for official resale (公式リセール) tickets of a sold-out ticket stock, allocated all-or-nothing by a daily draw. At most one Entered entry per (user, event), checked by the application under the users row lock. State: 1=Entered, 2=Won, 3=Lost, 4=Lapsed, 5=Withdrawn.';
COMMENT ON COLUMN resale_entries.id IS 'Unique resale entry identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN resale_entries.order_id IS 'The order the entry applies for: user, ticket stock, ticket count, referral code and card authorization; unique';
COMMENT ON COLUMN resale_entries.state IS 'Lifecycle: 1=Entered (waiting for a draw), 2=Won (allocated listings; the order is Pending until charged, then Paid), 3=Lost (no ticket left at the last draw, concert cancelled, or a win that could not be charged; the order is Void), 4=Lapsed (the card authorization would expire within 24 hours of a draw; the order is Void), 5=Withdrawn (by the fan while Entered; the order is Void)';
COMMENT ON COLUMN resale_entries.lost_reason IS 'Why a Lost entry lost: 1=no ticket left at the last draw, 2=concert cancelled, 3=the card could not be charged after it won, 4=the payment was not completed before the show. NULL unless state is 3 (Lost)';
COMMENT ON COLUMN resale_entries.created_at IS 'When the fan applied';
COMMENT ON COLUMN resale_entries.draw_at IS 'The draw that ended the entry: the draw it won, lost or lapsed at; set exactly when Won, Lost or Lapsed';
COMMENT ON COLUMN resale_entries.withdraw_at IS 'When the fan withdrew it; set exactly when Withdrawn';
COMMENT ON COLUMN resale_entries.ended_email_send_at IS 'When the email telling the fan that the entry lapsed or lost was sent; set once right after a successful send';

CREATE UNIQUE INDEX uq_resale_entries_order_id ON resale_entries(order_id);
COMMENT ON INDEX uq_resale_entries_order_id IS 'One resale entry per order';

CREATE INDEX idx_resale_entries_entered ON resale_entries(order_id) WHERE state = 1;
COMMENT ON INDEX idx_resale_entries_entered IS 'Optimizes finding the Entered entries a draw takes part in and a fan''s Entered entry for an event';

ALTER TABLE resale_listings ADD COLUMN resale_entry_id UUID REFERENCES resale_entries(id) ON DELETE SET NULL;
COMMENT ON COLUMN resale_listings.resale_entry_id IS 'The resale entry a draw allocated the listing to; NULL until allocated. A listing whose entry is Lost is free for the next draw again';

-- Each Sold listing is refunded by its own workflow, started from its sale's
-- announcement (design D10), so nothing lists the unrefunded ones any more.
DROP INDEX idx_resale_listings_sold_unfinished;

CREATE INDEX idx_resale_listings_resale_entry_id ON resale_listings(resale_entry_id) WHERE resale_entry_id IS NOT NULL;
COMMENT ON INDEX idx_resale_listings_resale_entry_id IS 'Optimizes finding the listings allocated to an entry and the foreign key check of an entry delete';

COMMENT ON COLUMN notifications.type IS 'Notification type: new_concerts, sales_reminder, ticket_sale_announcement, order_confirmation, lottery_loss, resale_entry_lapsed, resale_entry_lost';

CREATE OR REPLACE FUNCTION delete_ticket_stock(p_ticket_stock_id UUID) RETURNS BOOLEAN
LANGUAGE plpgsql SECURITY DEFINER SET search_path = app, public, pg_temp AS $$
BEGIN
  PERFORM 1 FROM ticket_stocks WHERE id = p_ticket_stock_id FOR UPDATE;
  IF NOT FOUND THEN
    RETURN FALSE;
  END IF;
  IF EXISTS (SELECT 1 FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status <> 5) THEN
    RAISE EXCEPTION 'ticket stock % is named by an order that is not void', p_ticket_stock_id USING ERRCODE = '23001';
  END IF;
  DELETE FROM checkouts WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id);
  DELETE FROM lottery_entries WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id);
  DELETE FROM resale_entries WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id);
  WITH removed AS (
    DELETE FROM orders WHERE ticket_stock_id = p_ticket_stock_id RETURNING card_authorization_id
  )
  DELETE FROM card_authorizations WHERE id IN (SELECT card_authorization_id FROM removed);
  DELETE FROM ticket_stocks WHERE id = p_ticket_stock_id;
  RETURN TRUE;
END
$$;

COMMENT ON FUNCTION delete_ticket_stock(UUID) IS 'TicketStock.Delete: removes a stock with its Void orders and their checkouts, lottery entries, resale entries and card authorizations, refused (23001) while a live order names it. SECURITY DEFINER; EXECUTE is granted only to the organizer server.';
