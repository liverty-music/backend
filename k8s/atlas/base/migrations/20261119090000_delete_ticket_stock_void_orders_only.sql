-- remove-concert-suppression-and-fix-removal-leftovers (design D4, backend#741):
-- delete_ticket_stock removes only the Void Orders its check allowed, with
-- their checkouts, lottery entries, resale entries and card authorizations.
-- An Order that is not Void stays in place, so its RESTRICT foreign key fails
-- the whole delete instead of the Order vanishing. CREATE OR REPLACE keeps the
-- function's owner and its EXECUTE grant.

SET search_path TO app, public;

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
  DELETE FROM checkouts WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status = 5);
  DELETE FROM lottery_entries WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status = 5);
  DELETE FROM resale_entries WHERE order_id IN (SELECT id FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status = 5);
  WITH removed AS (
    DELETE FROM orders WHERE ticket_stock_id = p_ticket_stock_id AND status = 5 RETURNING card_authorization_id
  )
  DELETE FROM card_authorizations WHERE id IN (SELECT card_authorization_id FROM removed);
  DELETE FROM ticket_stocks WHERE id = p_ticket_stock_id;
  RETURN TRUE;
END
$$;

COMMENT ON FUNCTION delete_ticket_stock(UUID) IS 'TicketStock.Delete: removes a stock with its Void orders and their checkouts, lottery entries, resale entries and card authorizations, refused (23001) while an order that is not Void names it; only Void orders are deleted, so one created during the delete fails it through its foreign key. SECURITY DEFINER; EXECUTE is granted only to the organizer server.';
