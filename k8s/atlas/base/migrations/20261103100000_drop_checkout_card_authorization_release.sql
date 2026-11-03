-- unify-order-issuance (design "Decisions made while implementing"): a
-- Checkout's card authorization is released by the release-card-authorization
-- workflow, whose stored state records whether it finished, so the Checkout no
-- longer keeps a release time. Its index served only the removed ListDue, and
-- delete_ticket_stock no longer waits for the release.

SET search_path TO app, public;

DROP INDEX IF EXISTS idx_checkouts_due;

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
  DELETE FROM orders WHERE ticket_stock_id = p_ticket_stock_id;
  DELETE FROM ticket_stocks WHERE id = p_ticket_stock_id;
  RETURN TRUE;
END
$$;

COMMENT ON FUNCTION delete_ticket_stock(UUID) IS 'TicketStock.Delete: removes a stock with its Void orders and their checkouts and lottery entries, refused (23001) while a live order names it. SECURITY DEFINER; EXECUTE is granted only to the organizer server.';

ALTER TABLE checkouts DROP COLUMN card_authorization_release_at;
