-- show-hype-and-journey-benefits: a discovered lottery records the day its
-- results are announced, and the reminder sent-log accepts the RESULT_DAY
-- stage (4) again. Catalog-only changes; no row holds a result date or stage 4.

SET search_path TO app, public;

ALTER TABLE ticket_sales
    ADD COLUMN result_date date NULL,
    ADD CONSTRAINT chk_ticket_sales_result_date CHECK ((result_date IS NULL) OR ((discovered_at IS NOT NULL) AND (method = 2) AND (result_date >= ((sale_end_at AT TIME ZONE 'Asia/Tokyo'::text))::date)));
COMMENT ON COLUMN ticket_sales.result_date IS 'Calendar day in Japan time (Asia/Tokyo) on which a discovered lottery''s results are announced, as its pages state it; no earlier than the Japan-time day of sale_end_at. NULL when the pages do not state it, and always NULL on a first-come sale and on an Organizer''s sale. Anchors the RESULT_DAY reminder.';

ALTER TABLE ticket_sale_reminders
    DROP CONSTRAINT chk_ticket_sale_reminders_stage,
    ADD CONSTRAINT chk_ticket_sale_reminders_stage CHECK (stage = ANY (ARRAY[1, 2, 4]));
COMMENT ON COLUMN ticket_sale_reminders.stage IS 'Reminder stage: 1=APPLY_OPEN (at the start for a lottery, 30 minutes before it for a first-come sale), 2=APPLY_CLOSE_24H (24h before the end, lottery only), 4=RESULT_DAY (09:00 on the result date in the fan''s time zone, discovered lottery with a result date only). 3 was APPLY_CLOSE_1H and is not used.';
