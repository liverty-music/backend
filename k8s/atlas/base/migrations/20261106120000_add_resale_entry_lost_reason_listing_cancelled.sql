-- stop-charging-resale-wins-without-tickets: a won resale entry whose
-- allocated listing was cancelled after the charge has the charge refunded
-- and becomes Lost with the new lost reason 5, "listing cancelled" (design D3).
-- Widens the lost reason check from 1..4 to 1..5; no row changes.

SET search_path TO app, public;

ALTER TABLE resale_entries
    DROP CONSTRAINT chk_resale_entries_lost_reason,
    ADD CONSTRAINT chk_resale_entries_lost_reason CHECK ((state = 3 AND lost_reason BETWEEN 1 AND 5) OR (state <> 3 AND lost_reason IS NULL));

COMMENT ON COLUMN resale_entries.lost_reason IS 'Why a Lost entry lost: 1=no ticket left at the last draw, 2=concert cancelled, 3=the card could not be charged after it won, 4=the payment was not completed before the show, 5=a listing allocated to it was cancelled after it was charged, so the charge was refunded. NULL unless state is 3 (Lost)';
