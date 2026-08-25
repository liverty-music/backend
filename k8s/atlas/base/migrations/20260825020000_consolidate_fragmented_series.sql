-- Consolidate fragmented discovered series in place.
--
-- Production discovery data is badly fragmented: 381 of 382 series rows have
-- exactly one event because the persistence path minted a fresh series per
-- event, ignoring tour grouping. This one-time, re-runnable migration groups the
-- fragments back into one canonical series per tour, re-points every member
-- event and sales phase onto it, dedups the duplicated sales phases, types the
-- consolidated multi-event groups as TOUR, and deletes the emptied fragments.
-- User-owned data (ticket_journeys) hangs off events, which are preserved, so no
-- user data is lost. See specification change fix-series-fragmentation, §5.
--
-- Grouping key: (source_url, type) where a source_url is present — the tour's
-- stable feature/announcement page, which survives title re-branding — falling
-- back to (normalized title, type) only when source_url is empty. NOT by artist:
-- a co-headline tour spans artists but is one series (artists stay linked via
-- event_performers). The canonical series per group is the earliest UUIDv7.

-- ===== Snapshot for rollback (before any mutation) =====
-- Records every event / sales_phase's prior series_id and every series' prior
-- type. Idempotent: ON CONFLICT keeps the ORIGINAL snapshot across re-runs, so a
-- second run (after re-pointing) never overwrites the pre-migration values.
CREATE TABLE IF NOT EXISTS _series_consolidation_backup (
    entity        text NOT NULL,
    id            uuid NOT NULL,
    old_series_id uuid,
    old_type      series_type,
    PRIMARY KEY (entity, id)
);

INSERT INTO _series_consolidation_backup (entity, id, old_series_id, old_type)
SELECT 'event', id, series_id, NULL FROM events
ON CONFLICT (entity, id) DO NOTHING;

INSERT INTO _series_consolidation_backup (entity, id, old_series_id, old_type)
SELECT 'sales_phase', id, series_id, NULL FROM sales_phases
ON CONFLICT (entity, id) DO NOTHING;

INSERT INTO _series_consolidation_backup (entity, id, old_series_id, old_type)
SELECT 'series', id, id, type FROM series
ON CONFLICT (entity, id) DO NOTHING;

-- ===== Compute the canonical series per fragmentation group =====
-- A regular (non-temporary) table, not a TEMPORARY one: Atlas replays a
-- migration file statement-by-statement (validate/lint use a scratch dev DB and
-- may run each statement in its own transaction/connection), so an
-- ON COMMIT DROP temp table would vanish before the UPDATEs below can see it.
-- Dropped explicitly at the end; DROP IF EXISTS up front keeps the run re-runnable.
DROP TABLE IF EXISTS _series_group_map;
CREATE TABLE _series_group_map AS
WITH grp AS (
    SELECT
        id,
        CASE
            WHEN source_url IS NOT NULL AND btrim(source_url) <> ''
                THEN 'U:' || btrim(source_url) || '|' || type::text
            ELSE 'T:' || btrim(lower(title)) || '|' || type::text
        END AS group_key
    FROM series
),
canon AS (
    -- UUIDv7 has no MIN() aggregate, but its text form is lexicographically
    -- time-ordered, so MIN(id::text) picks the earliest-minted series per group.
    SELECT group_key, MIN(id::text)::uuid AS canonical_id
    FROM grp
    GROUP BY group_key
)
SELECT g.id AS old_id, c.canonical_id
FROM grp g
JOIN canon c USING (group_key);

-- ===== Re-point member events and sales phases onto the canonical series =====
UPDATE events e
SET series_id = m.canonical_id
FROM _series_group_map m
WHERE e.series_id = m.old_id AND m.old_id <> m.canonical_id;

UPDATE sales_phases sp
SET series_id = m.canonical_id
FROM _series_group_map m
WHERE sp.series_id = m.old_id AND m.old_id <> m.canonical_id;

-- ===== Dedup sales phases on the FULL business tuple, keep earliest =====
-- Key is (series_id, apply_start_at, method, channel, provider_name) — NOT just
-- (series_id, apply_start_at) — so two genuinely-distinct phases that share a
-- start time (e.g. an FC lottery and a general on-sale) are preserved. The
-- earliest discovered_at row wins (id breaks any remaining tie deterministically).
DELETE FROM sales_phases sp
USING (
    SELECT
        id,
        row_number() OVER (
            PARTITION BY series_id, apply_start_at, method, channel, COALESCE(provider_name, '')
            ORDER BY discovered_at ASC, id ASC
        ) AS rn
    FROM sales_phases
) d
WHERE sp.id = d.id AND d.rn > 1;

-- ===== Type consolidated multi-event groups as TOUR =====
-- A canonical series that absorbed other fragments AND now owns more than one
-- event is a real multi-date tour; the pre-migration mis-typing (SINGLE) is
-- corrected here. Genuinely single-event SINGLE series are left untouched.
UPDATE series s
SET type = 'TOUR'
WHERE EXISTS (
        SELECT 1 FROM _series_group_map m
        WHERE m.canonical_id = s.id AND m.old_id <> m.canonical_id
    )
  AND (SELECT count(*) FROM events e WHERE e.series_id = s.id) > 1;

-- ===== Delete the emptied fragmented (non-canonical) series =====
-- Their events and sales phases were re-pointed above, so they now own nothing.
DELETE FROM series s
WHERE EXISTS (
    SELECT 1 FROM _series_group_map m
    WHERE m.old_id = s.id AND m.old_id <> m.canonical_id
);

-- ===== Drop the scratch group map (the rollback snapshot table is kept) =====
DROP TABLE _series_group_map;
