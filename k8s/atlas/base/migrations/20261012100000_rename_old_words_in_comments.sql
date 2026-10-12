-- Rewrite the comments that still used replaced domain words (unify-ticket-sales,
-- design D16 and D17): seller, drawn, card hold, waitlist, connected account,
-- KYC/KYB, NotHolder and application/phase. Comments only; no schema change.

SET search_path TO app, public;

COMMENT ON TABLE organizers IS 'Vetted businesses (label / agency / promoter / self-publishing artist) that represent artists and sell their tickets';
COMMENT ON COLUMN ticket_sales.draw_at IS 'When the lottery draw ran for every ticket type of the sale; NULL until the draw runs and for a first-come sale. Set atomically with the draw outcome; the draw sweeper skips sales where it is set.';
COMMENT ON COLUMN lottery_entries.card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); NULL on a Draft until the card authorization is opened, and on an entry withdrawn as a Draft; required from Entered on';
COMMENT ON COLUMN lottery_entries.state IS 'Lifecycle state: 1=Entered (card authorization in place), 2=Won (captured), 3=Lost (card authorization released), 4=Withdrawn (fan-cancelled or abandoned Draft, card authorization released), 5=Draft (before confirmation; takes no part in the draw)';
COMMENT ON COLUMN lottery_entries.draw_position IS 'Zero-based position in the ticket type''s draw shuffle. NULL until the draw runs; the draw order of the losers for official resale.';
COMMENT ON INDEX idx_checkouts_due IS 'Optimizes ListDue: lapsed holds, card authorizations to release and stalled commits';
COMMENT ON TABLE payout_accounts IS 'The Organizer''s payout account at the payment provider (for example a Stripe account, Accounts v2, transfers capability only). One row per Organizer. status: 1=Pending (payout account verification in progress), 2=Active (eligible for payout), 3=Restricted (capability disabled).';
COMMENT ON COLUMN payout_accounts.provisioned_at IS 'When the payout account was first provisioned at the payment provider';
COMMENT ON COLUMN settlements.organizer_id IS 'The Organizer that receives the payout. Stored denormalized so the sweeper avoids joining through the order''s lottery entry or checkout → ticket type → event.';
COMMENT ON TABLE rejected_scans IS 'Append-only note of a refused scan (Forged, Expired, OtherEvent) or of one refused ticket in it (Voided, AlreadyAdmitted). Reason 1=Forged, 2=Expired, 3=OtherEvent, 5=Voided, 6=AlreadyAdmitted (4 is retired).';
