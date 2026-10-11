-- One name per concept (unify-ticket-sales, design D16 and D17).
--
-- Renames the tables, columns, constraints and indexes whose names differ
-- from the entity specs, so a concept has one name in the spec, proto, DB,
-- Go and TS:
--   kyc                          -> identity_verifications
--   organizer_connected_accounts -> payout_accounts
--   staged_concerts              -> concert_candidates
--   rejected_concerts_log        -> rejected_concerts
--   latest_search_logs           -> concert_searches
--   media                        -> series_images
--   series_media                 -> series_cover_images (one cover per Series)
-- External IDs become provider_<concept>_id, card hold references become
-- card_authorization_id, and timestamps take the present-tense stem
-- (draw_at, pay_at, issue_at, ...). The Settlement's creation time is
-- created_at.
--
-- Removed: users.country and is_active (never read), media.kind
-- and the media_kind type (an image is the only kind), series_media's
-- display_order (one cover per Series), and the RejectedScan reason 4
-- (NotHolder) from the reason CHECK.
--
-- users.time_zone stays: the sales-phase announcement and reminder specs
-- show times in the fan's time zone.
--
-- Every rename keeps its rows, and table and column privileges follow the
-- renamed objects, so no grant is redone. Forward-only; the file runs in one
-- transaction.

SET search_path TO app, public;

-- ── users ────────────────────────────────────────────────────────────

ALTER TABLE users RENAME COLUMN external_id TO provider_user_id;
ALTER TABLE users RENAME CONSTRAINT users_external_id_key TO users_provider_user_id_key;
ALTER INDEX idx_users_external_id RENAME TO idx_users_provider_user_id;
ALTER TABLE users DROP COLUMN country, DROP COLUMN is_active;

-- ── organizers ───────────────────────────────────────────────────────

ALTER TABLE organizers RENAME COLUMN zitadel_org_id TO provider_organizer_id;
ALTER INDEX uq_organizers_zitadel_org_id RENAME TO uq_organizers_provider_organizer_id;

-- ── identity_verifications ───────────────────────────────────────────

ALTER TABLE kyc RENAME TO identity_verifications;
ALTER TABLE identity_verifications RENAME COLUMN pocket_sign_user_id TO provider_user_id;
ALTER TABLE identity_verifications RENAME COLUMN verified_at TO verify_at;
ALTER TABLE identity_verifications RENAME CONSTRAINT kyc_pkey TO identity_verifications_pkey;
ALTER TABLE identity_verifications RENAME CONSTRAINT kyc_user_id_fkey TO identity_verifications_user_id_fkey;
ALTER TABLE identity_verifications RENAME CONSTRAINT chk_kyc_id_uuidv7 TO chk_identity_verifications_id_uuidv7;
ALTER TABLE identity_verifications RENAME CONSTRAINT chk_kyc_method TO chk_identity_verifications_method;
ALTER TABLE identity_verifications RENAME CONSTRAINT chk_kyc_dedupe_strength TO chk_identity_verifications_dedupe_strength;
ALTER TABLE identity_verifications RENAME CONSTRAINT chk_kyc_status TO chk_identity_verifications_status;
ALTER TABLE identity_verifications RENAME CONSTRAINT chk_kyc_pocket_sign_user_id_non_empty TO chk_identity_verifications_provider_user_id_non_empty;
ALTER INDEX uq_active_pocket_sign_user_id RENAME TO uq_identity_verifications_active_provider_user_id;
ALTER INDEX idx_kyc_user_id RENAME TO idx_identity_verifications_user_id;

-- ── tickets ──────────────────────────────────────────────────────────

ALTER TABLE tickets RENAME COLUMN kyc_id TO identity_verification_id;
ALTER TABLE tickets RENAME COLUMN issued_at TO issue_at;
ALTER TABLE tickets RENAME COLUMN admitted_at TO admit_at;

-- ── ticket_sales ─────────────────────────────────────────────────────

ALTER TABLE ticket_sales RENAME COLUMN drawn_at TO draw_at;
ALTER TABLE ticket_sales RENAME CONSTRAINT chk_ticket_sales_drawn_lottery_only TO chk_ticket_sales_draw_lottery_only;

-- ── lottery_entries ──────────────────────────────────────────────────

ALTER TABLE lottery_entries RENAME COLUMN requested_ticket_count TO ticket_count;
ALTER TABLE lottery_entries RENAME COLUMN payment_intent_ref TO card_authorization_id;
ALTER TABLE lottery_entries RENAME CONSTRAINT chk_lottery_entries_count_positive TO chk_lottery_entries_ticket_count_positive;
ALTER TABLE lottery_entries RENAME CONSTRAINT chk_lottery_entries_payment_intent_ref_not_empty TO chk_lottery_entries_card_authorization_id_not_empty;

-- ── checkouts ────────────────────────────────────────────────────────

ALTER TABLE checkouts RENAME COLUMN authorization_ref TO card_authorization_id;
ALTER TABLE checkouts RENAME COLUMN authorization_released_at TO card_authorization_release_at;
ALTER TABLE checkouts RENAME COLUMN committed_at TO commit_at;
ALTER TABLE checkouts RENAME CONSTRAINT chk_checkouts_authorization_ref_not_empty TO chk_checkouts_card_authorization_id_not_empty;
ALTER TABLE checkouts RENAME CONSTRAINT chk_checkouts_released_needs_ref TO chk_checkouts_release_needs_card_authorization;
ALTER TABLE checkouts RENAME CONSTRAINT chk_checkouts_committed_time TO chk_checkouts_commit_time;

-- ── orders ───────────────────────────────────────────────────────────

ALTER TABLE orders RENAME COLUMN payment_intent_ref TO provider_payment_id;
ALTER TABLE orders RENAME COLUMN payment_method_ref TO provider_payment_method_id;
ALTER TABLE orders RENAME COLUMN refund_ref TO provider_refund_id;
ALTER TABLE orders RENAME COLUMN paid_at TO pay_at;
ALTER TABLE orders RENAME COLUMN confirmation_sent_at TO confirmation_send_at;

-- ── payout_accounts ──────────────────────────────────────────────────

ALTER TABLE organizer_connected_accounts RENAME TO payout_accounts;
ALTER TABLE payout_accounts RENAME COLUMN account_ref TO provider_account_id;
ALTER TABLE payout_accounts RENAME CONSTRAINT organizer_connected_accounts_pkey TO payout_accounts_pkey;
ALTER TABLE payout_accounts RENAME CONSTRAINT organizer_connected_accounts_organizer_id_fkey TO payout_accounts_organizer_id_fkey;
ALTER TABLE payout_accounts RENAME CONSTRAINT chk_oca_account_ref_not_empty TO chk_payout_accounts_provider_account_id_not_empty;
ALTER TABLE payout_accounts RENAME CONSTRAINT chk_oca_status TO chk_payout_accounts_status;

-- ── settlements and settlement_splits ────────────────────────────────

ALTER TABLE settlements RENAME COLUMN charge_ref TO provider_charge_id;
ALTER TABLE settlements RENAME COLUMN released_at TO release_at;
ALTER TABLE settlements RENAME COLUMN settled_at TO created_at;
ALTER TABLE settlements RENAME CONSTRAINT chk_settlements_charge_ref_not_empty TO chk_settlements_provider_charge_id_not_empty;
ALTER TABLE settlement_splits RENAME COLUMN transfer_ref TO provider_payout_id;
ALTER TABLE settlement_splits RENAME COLUMN transfer_reversal_ref TO provider_payout_reversal_id;
ALTER TABLE settlement_splits RENAME CONSTRAINT chk_settlement_splits_transfer_ref_not_empty TO chk_settlement_splits_provider_payout_id_not_empty;
ALTER TABLE settlement_splits RENAME CONSTRAINT chk_settlement_splits_reversal_ref_not_empty TO chk_settlement_splits_provider_payout_reversal_id_not_empty;

-- ── concert_candidates, rejected_concerts, concert_searches ──────────

ALTER TABLE staged_concerts RENAME TO concert_candidates;
ALTER TABLE concert_candidates RENAME CONSTRAINT staged_concerts_pkey TO concert_candidates_pkey;
ALTER TABLE concert_candidates RENAME CONSTRAINT staged_concerts_artist_id_fkey TO concert_candidates_artist_id_fkey;
ALTER TABLE concert_candidates RENAME CONSTRAINT staged_concerts_series_id_fkey TO concert_candidates_series_id_fkey;
ALTER TABLE concert_candidates RENAME CONSTRAINT chk_staged_concerts_id_uuidv7 TO chk_concert_candidates_id_uuidv7;
ALTER INDEX uq_staged_concerts_by_place RENAME TO uq_concert_candidates_by_place;
ALTER INDEX uq_staged_concerts_by_listed_name RENAME TO uq_concert_candidates_by_listed_name;
ALTER INDEX idx_staged_concerts_discovered_at RENAME TO idx_concert_candidates_discovered_at;

ALTER TABLE rejected_concerts_log RENAME TO rejected_concerts;
ALTER TABLE rejected_concerts RENAME CONSTRAINT rejected_concerts_log_pkey TO rejected_concerts_pkey;
ALTER TABLE rejected_concerts RENAME CONSTRAINT chk_rejected_concerts_log_id_uuidv7 TO chk_rejected_concerts_id_uuidv7;
ALTER INDEX idx_rejected_concerts_log_artist_id RENAME TO idx_rejected_concerts_artist_id;
ALTER INDEX idx_rejected_concerts_log_rejected_at RENAME TO idx_rejected_concerts_rejected_at;

ALTER TABLE latest_search_logs RENAME TO concert_searches;
ALTER TABLE concert_searches RENAME CONSTRAINT latest_search_logs_pkey TO concert_searches_pkey;
ALTER TABLE concert_searches RENAME CONSTRAINT latest_search_logs_artist_id_fkey TO concert_searches_artist_id_fkey;

-- ── series_images and series_cover_images ────────────────────────────

ALTER TABLE media RENAME TO series_images;
ALTER TABLE series_images RENAME CONSTRAINT media_pkey TO series_images_pkey;
ALTER TABLE series_images RENAME CONSTRAINT media_organizer_id_fkey TO series_images_organizer_id_fkey;
ALTER TABLE series_images RENAME CONSTRAINT chk_media_id_uuidv7 TO chk_series_images_id_uuidv7;
ALTER TABLE series_images DROP COLUMN kind;
DROP TYPE media_kind;

ALTER TABLE series_media RENAME TO series_cover_images;
ALTER TABLE series_cover_images RENAME COLUMN media_id TO series_image_id;
ALTER TABLE series_cover_images RENAME CONSTRAINT series_media_pkey TO series_cover_images_pkey;
ALTER TABLE series_cover_images RENAME CONSTRAINT series_media_series_id_fkey TO series_cover_images_series_id_fkey;
ALTER TABLE series_cover_images RENAME CONSTRAINT series_media_media_id_fkey TO series_cover_images_series_image_id_fkey;
ALTER INDEX uq_series_media_series RENAME TO uq_series_cover_images_series;
ALTER TABLE series_cover_images DROP COLUMN display_order;

-- ── rejected_scans: retire reason 4 (NotHolder) ──────────────────────

ALTER TABLE rejected_scans
  DROP CONSTRAINT chk_rejected_scans_reason,
  ADD CONSTRAINT chk_rejected_scans_reason CHECK (reason IN (1, 2, 3, 5, 6));

-- ── comments whose text names the old concepts ───────────────────────

COMMENT ON COLUMN users.provider_user_id IS 'The identity provider''s ID for the user (Zitadel sub claim), used for account sync';
COMMENT ON TABLE series IS 'Parent aggregation above events. Owns metadata shared across every event in a tour, festival, or multi-day single-venue run. Cover image URL is derived from the series_cover_images join at read time.';
COMMENT ON TABLE series_images IS 'Images uploaded by an organizer for its Series. The row id is the UUIDv7 that forms the object-key basename (internal/{org}/{id} or cdn/{org}/{id}); content_type is stored in attributes.';
COMMENT ON COLUMN series_images.id IS 'Unique series image identifier (UUIDv7, application-generated). Doubles as the cache-busting version token in the object key; a new upload mints a new id.';
COMMENT ON COLUMN series_images.attributes IS 'Upload metadata as JSONB: {"content_type": "image/jpeg"}. May include width/height later.';
COMMENT ON TABLE series_cover_images IS 'The cover SeriesImage of a Series. One cover per series (uq_series_cover_images_series).';
COMMENT ON COLUMN series_cover_images.series_id IS 'The Series whose cover this is.';
COMMENT ON COLUMN series_cover_images.series_image_id IS 'The SeriesImage shown as the cover.';
COMMENT ON INDEX uq_series_cover_images_series IS 'At most one cover image per series.';
COMMENT ON COLUMN concert_candidates.id IS 'Unique concert candidate identifier (UUIDv7, application-generated). Exposed to the admin console as ConcertCandidateId.';
COMMENT ON TABLE rejected_concerts IS 'Append-only record of rejected concert candidates, used for searcher-quality analysis only. Not consulted by discovery dedup; never suppresses re-discovery.';
COMMENT ON COLUMN rejected_concerts.id IS 'Unique rejected concert identifier (UUIDv7, application-generated).';
COMMENT ON TABLE suppressed_concerts IS 'Moderation suppression set: the natural key of each published concert an operator deleted, consulted by discovery so a deleted concert is not auto-published or re-staged again. Distinct from rejected_concerts, which is analysis-only and never suppresses.';
COMMENT ON INDEX idx_users_provider_user_id IS 'Speeds up user lookup by the identity provider''s ID (Zitadel sub claim)';
COMMENT ON COLUMN organizers.provider_organizer_id IS 'The identity provider''s ID for the Organizer (Zitadel tenant organization ID); NULL until provisioning completes';
COMMENT ON TABLE identity_verifications IS 'Account-level 本人確認 (IdentityVerification) results. One row per (user, verification attempt); the active invariant (at most one ACTIVE row per provider_user_id) is enforced by the partial unique index.';
COMMENT ON COLUMN identity_verifications.id IS 'Unique IdentityVerification identifier (UUIDv7, application-generated).';
COMMENT ON COLUMN identity_verifications.user_id IS 'Reference to the user account this IdentityVerification is bound to.';
COMMENT ON COLUMN identity_verifications.provider_user_id IS 'The verification provider''s ID for the user (Pocket Sign tenant-scoped person key, User.id). The only person-identifying datum stored; never the serial, never the 個人番号.';
COMMENT ON INDEX uq_identity_verifications_active_provider_user_id IS 'At most one ACTIVE IdentityVerification per verification provider person key. A second verified account for the same person is rejected here.';
COMMENT ON INDEX idx_identity_verifications_user_id IS 'Optimizes lookup of a user''s active IdentityVerification.';
COMMENT ON COLUMN ticket_sales.verification_requirement IS '本人確認 requirement: 0=NONE (any fan), 1=VERIFIED_ANY (an Active IdentityVerification is required), 2=JPKI_ONLY (a JPKI-backed IdentityVerification is required)';
COMMENT ON COLUMN lottery_entries.card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); NULL on a Draft until the hold is opened, and on an entry withdrawn as a Draft; required from Entered on';
COMMENT ON COLUMN checkouts.card_authorization_id IS 'The payment provider''s ID for the card authorization (Stripe pi_...); NULL until the fan authorizes, then set once';
COMMENT ON COLUMN checkouts.card_authorization_release_at IS 'When the card authorization was released without a charge; NULL unless released';
COMMENT ON COLUMN checkouts.capture_at IS 'When the card authorization (card_authorization_id) was captured; set once, and a row with it is never Expired or Released';
COMMENT ON COLUMN orders.provider_payment_id IS 'The payment provider''s ID for the captured payment (e.g. Stripe pi_...)';
COMMENT ON COLUMN orders.provider_payment_method_id IS 'The payment provider''s ID for the payment method (e.g. Stripe pm_...), optional';
COMMENT ON COLUMN orders.provider_refund_id IS 'The payment provider''s ID for the refund (e.g. Stripe "re_...") set when the order is refunded via CANCELLATION. Empty for DISPUTE reason (the chargeback already reversed the charge at the card network) and for orders that are not yet refunded.';
COMMENT ON COLUMN tickets.identity_verification_id IS 'The IdentityVerification the ticket is bound to when its sale required verification; NULL otherwise. No FK so privacy deletion of an IdentityVerification is independent.';
COMMENT ON TABLE payout_accounts IS 'The Organizer''s payout account at the payment provider (a payout-recipient Stripe connected account, Accounts v2, transfers capability only). One row per Organizer. status: 1=Pending (KYC/KYB in progress), 2=Active (eligible for payout), 3=Restricted (capability disabled).';
COMMENT ON COLUMN payout_accounts.provider_account_id IS 'The payment provider''s ID for the payout account (e.g. Stripe "acct_..."); meaningful only to the provider';
COMMENT ON COLUMN settlements.provider_charge_id IS 'The payment provider''s ID for the charge (e.g. Stripe "ch_...") resolved from the Order''s provider payment ID at payout time. NULL until resolved. Used as source_transaction on each Transfer.';
COMMENT ON COLUMN settlement_splits.provider_payout_id IS 'The payment provider''s ID for the payout (a Stripe Transfer, e.g. "tr_..."). NULL until the split is released.';
COMMENT ON COLUMN settlement_splits.provider_payout_reversal_id IS 'The payment provider''s ID for the payout reversal (a Stripe transfer reversal, e.g. "trr_..."). NULL unless reversed on a refund/dispute.';
COMMENT ON TABLE admissions IS 'Append-only attendance evidence: a ticket let in at the venue, through which reception link and when. Written together with tickets.admit_at; never changed or removed, even when the ticket is later voided.';
COMMENT ON COLUMN admissions.admitted_at IS 'When the ticket was let in; equal to tickets.admit_at';
COMMENT ON TABLE rejected_scans IS 'Append-only note of a refused scan (Forged, Expired, OtherEvent) or of one refused ticket in it (Voided, AlreadyAdmitted). Reason 1=Forged, 2=Expired, 3=OtherEvent, 5=Voided, 6=AlreadyAdmitted (4, the former NotHolder, is retired).';
COMMENT ON COLUMN rejected_scans.reason IS 'Why it was refused: 1=Forged, 2=Expired, 3=OtherEvent, 5=Voided, 6=AlreadyAdmitted';
