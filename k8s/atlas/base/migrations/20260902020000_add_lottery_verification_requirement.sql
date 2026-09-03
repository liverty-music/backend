-- Add verification_requirement column to lottery_sales_phases.
--
-- This is the Section 6 gate for identity-ekyc-jpki: a lottery phase can
-- now require applicants to hold an active verified identity before applying.
-- The requirement is evaluated by the Apply use case at submission time.
--
-- Values:
--   0 = NONE         (default; no requirement, any fan may apply)
--   1 = VERIFIED_ANY (active verified identity required; MVP behaves as JPKI_ONLY
--                     because the driver's-licence fallback is POST-MVP)
--   2 = JPKI_ONLY    (JPKI-backed verified identity required; no fallback)
--
-- The column is NOT NULL DEFAULT 0 so all existing phases inherit NONE without
-- any application-layer migration.

-- Add the column with NOT NULL DEFAULT 0 so existing rows default to NONE.
ALTER TABLE lottery_sales_phases
    ADD COLUMN IF NOT EXISTS verification_requirement SMALLINT NOT NULL DEFAULT 0;

-- Enforce the valid enum range (0=NONE, 1=VERIFIED_ANY, 2=JPKI_ONLY).
ALTER TABLE lottery_sales_phases
    ADD CONSTRAINT chk_lottery_sales_phases_verification_requirement
    CHECK (verification_requirement BETWEEN 0 AND 2);

COMMENT ON COLUMN lottery_sales_phases.verification_requirement IS
    'Identity-verification requirement: 0=NONE (any fan may apply), '
    '1=VERIFIED_ANY (active verified identity required; MVP behaves as JPKI_ONLY), '
    '2=JPKI_ONLY (JPKI-backed identity required, no licence fallback).';
