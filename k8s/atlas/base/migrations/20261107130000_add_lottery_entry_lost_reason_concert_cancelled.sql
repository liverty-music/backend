-- end-lottery-entries-of-cancelled-events: an Entered lottery entry of a
-- cancelled Event, or a won one whose concert was cancelled before the charge,
-- becomes Lost with the new lost reason 3, "concert cancelled" (design D4).
-- Widens the lost reason check from 1..2 to 1..3; no row changes.

SET search_path TO app, public;

ALTER TABLE lottery_entries
    DROP CONSTRAINT chk_lottery_entries_lost_reason,
    ADD CONSTRAINT chk_lottery_entries_lost_reason CHECK ((state = 3 AND lost_reason IN (1, 2, 3)) OR (state <> 3 AND lost_reason IS NULL));

COMMENT ON COLUMN lottery_entries.state IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (allocated by the draw; the order is Pending until charged, then Paid), 3=Lost (not allocated, the win was reverted because the card could not be charged, or the concert was cancelled before the charge; the order is Void)';
COMMENT ON COLUMN lottery_entries.lost_reason IS 'Why a Lost entry lost: 1=not drawn, 2=the card authorization could not be captured, 3=the concert was cancelled before the entry was charged. NULL unless state is 3 (Lost).';
