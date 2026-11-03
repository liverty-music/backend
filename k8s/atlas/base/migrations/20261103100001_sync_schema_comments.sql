-- #765: the comments schema.sql and the migrations disagreed on, found by the
-- first `atlas migrate diff` that could run again; schema.sql's text is the
-- current one. Also comments idx_venues_listed_name_admin_area, which
-- 20260406120000 created and schema.sql now lists. Comments only; no schema
-- or data change.

SET search_path TO app, public;

COMMENT ON COLUMN events.series_id IS 'Reference to the parent series that aggregates this event with any sibling events. Not part of the natural key — series is a grouping parent, not a component of event identity.';
COMMENT ON COLUMN series.id IS 'Unique series identifier (UUIDv7, application-generated). Series has no content-derived key: cross-run identity is established by adopting the series_id already carried by its member events (matched on the events physical natural key); a fresh UUIDv7 series is minted only when no member event yet exists.';
COMMENT ON INDEX idx_tickets_event_id IS 'Optimizes per-event ticket lookups (check-in, cancellation)';
COMMENT ON INDEX idx_tickets_order_id IS 'Optimizes loading the tickets of an order';
COMMENT ON COLUMN venues.listed_venue_name IS 'Raw scraped venue name as returned by Gemini; used for DB-first lookup to avoid redundant Places API calls';
COMMENT ON INDEX idx_venues_listed_name_admin_area IS 'One venue per scraped name and admin area: the DB-first venue lookup, and a conflict that VenueRepository.Create resolves to the existing row';
