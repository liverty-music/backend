-- Create homes table for structured user home locations.
--
-- Design rationale: A normalized table keeps the home concept self-contained
-- and avoids mixing multiple code systems in a single column.
-- level_1 is always ISO 3166-2. level_2's code system is a discriminated union
-- keyed by country_code (US→FIPS, DE→AGS, etc.).
-- Phase 1 (Japan-only): level_2 is always NULL.

CREATE TABLE homes (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     UUID NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
  country_code VARCHAR(2) NOT NULL,
  level_1     VARCHAR(6) NOT NULL,
  level_2     VARCHAR(20),
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

COMMENT ON TABLE homes IS 'Structured geographic home area for users. Determines dashboard lane classification (home/nearby/away).';
COMMENT ON COLUMN homes.country_code IS 'ISO 3166-1 alpha-2 country code (e.g., JP, US).';
COMMENT ON COLUMN homes.level_1 IS 'ISO 3166-2 subdivision code (e.g., JP-13 for Tokyo, US-NY for New York). Always this standard worldwide.';
COMMENT ON COLUMN homes.level_2 IS 'Optional finer-grained area code. Code system determined by country_code (US→FIPS, DE→AGS). NULL in Phase 1.';

-- FK reference from users to homes
ALTER TABLE users ADD COLUMN home_id UUID REFERENCES homes(id) ON DELETE SET NULL;

COMMENT ON COLUMN users.home_id IS 'Reference to the user home area in the homes table. NULL when home is not set.';

-- Grant access to IAM-authenticated application users
GRANT SELECT, INSERT, UPDATE, DELETE ON homes TO PUBLIC;
