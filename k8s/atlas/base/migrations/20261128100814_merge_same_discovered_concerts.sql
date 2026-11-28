-- Create "_merge_same_concerts_backup" table
CREATE TABLE "_merge_same_concerts_backup" (
  "id" uuid NOT NULL DEFAULT uuidv7(),
  "entity" text NOT NULL,
  "row_data" jsonb NOT NULL,
  PRIMARY KEY ("id")
);
-- Set comment to table: "_merge_same_concerts_backup"
COMMENT ON TABLE "_merge_same_concerts_backup" IS 'One-time rollback snapshot of the merge of duplicate discovered concerts and split TOUR Series (recognize-same-discovered-concert D10): every row the merge deleted or changed, as it was before. No code reads it';
-- Set comment to column: "id" on table: "_merge_same_concerts_backup"
COMMENT ON COLUMN "_merge_same_concerts_backup"."id" IS 'Surrogate key (UUIDv7), in the order the rows were saved';
-- Set comment to column: "entity" on table: "_merge_same_concerts_backup"
COMMENT ON COLUMN "_merge_same_concerts_backup"."entity" IS 'What the row was and what the merge did to it: the table name, followed by deleted, changed or moved';
-- Set comment to column: "row_data" on table: "_merge_same_concerts_backup"
COMMENT ON COLUMN "_merge_same_concerts_backup"."row_data" IS 'The row before the merge, as to_jsonb of the table row';

-- ===== Merge duplicate discovered concerts and split discovered TOUR Series =====
-- OpenSpec change recognize-same-discovered-concert, design D10 (liverty-music/backend#926).
-- Applied after the discovery code of the change runs in prod, so neither the
-- old code nor two concurrent batches recreate what this merges. Every target
-- is listed by id (collected read-only from prod on 2026-11-28; the query and
-- its result are in the pull request). A listed row that no longer exists, or
-- that no longer matches what was collected, is skipped. Every deleted or
-- changed row is saved in _merge_same_concerts_backup first.
--
-- The scratch tables are regular tables, dropped at the end: Atlas may run
-- statements on separate connections, where a temporary table is not visible.

-- ----- 1. Duplicate pairs: keep the older event, delete the newer one -----
DROP TABLE IF EXISTS _merge_pairs;
CREATE TABLE _merge_pairs (
    keep_id UUID PRIMARY KEY,
    drop_id UUID NOT NULL UNIQUE,
    listed_name TEXT NOT NULL,
    drop_start TIME,
    drop_open TIME,
    other_names TEXT[]
);
INSERT INTO _merge_pairs (keep_id, drop_id, listed_name) VALUES
    -- ELLEGARDEN 2026-11-28 / 11-29: "YES24 LIVE HALL, Seoul" (17:30 / 18:00)
    ('01a05c37-de11-7c01-80ee-89710323bf55', '01a10f21-4e32-7af8-ba19-4926e03e4ae0', 'Seoul YES24 LIVE HALL'),
    ('01a05c37-de1d-7651-8dfc-06c3dd0f75f2', '01a10f21-4e48-79fc-99f2-894d71d93f29', 'Seoul YES24 LIVE HALL'),
    -- LE SSERAFIM 2026-12-19 / 12-20: "Kyocera Dome Osaka" kept, named as Japanese sources do
    ('01a10f2c-a661-7faa-abce-d5bc094038ec', '01a1965a-ee02-7a6d-8ec2-622349a45aef', '京セラドーム大阪'),
    ('01a10f2c-a673-7cec-91a7-363666acf8fb', '01a1965a-ee2c-70d1-b98a-e26371b8e740', '京セラドーム大阪'),
    -- Lucky Kilimanjaro 2027-03-13 / 03-22
    ('01a05c4c-c490-7907-afc6-c307fe8af438', '01a1966b-31cc-761e-aadc-a5590b760ff3', 'SENDAI PIT'),
    ('01a05c4c-c691-7875-abd7-1ef3bf1f2ef3', '01a1966b-32b0-7865-b305-34fda4d97ab0', 'KANAZAWA EIGHT HALL'),
    -- Kevin Atwater 2026-12-06: one show at Le Botanique (official site); "Witloof Bar" resolved to another venue
    ('01a05c5c-6fdf-7790-9d93-92da8dbf1312', '01a19674-e0a7-7a0a-9f0e-e317bf7aa791', 'Botanique'),
    -- Vaundy 2027-08-28 / 08-29: one show a day (official site); the two names resolved to two venues
    ('019f8cd1-768e-7188-b6a7-7f55e26ab014', '01a11fe4-b057-72a8-b95d-e7b275bbbd69', 'クロコくんホール (旧 日本ガイシホール)'),
    ('019f8cd1-79d7-7206-9a88-5f30b1c7571a', '01a11fe4-b16a-7fe7-aef1-f6b44304ca50', 'クロコくんホール (旧 日本ガイシホール)');

-- Skip a pair whose events are gone, moved to another date, or no longer discovered.
DELETE FROM _merge_pairs p
WHERE NOT EXISTS (
    SELECT 1 FROM events k JOIN events d ON d.id = p.drop_id
    WHERE k.id = p.keep_id
      AND k.local_event_date = d.local_event_date
      AND k.publish_state IS NULL AND d.publish_state IS NULL
);

-- The newer event's times, and every name of the pair other than the agreed one.
UPDATE _merge_pairs p
SET drop_start = d.local_start_time,
    drop_open = d.local_open_time,
    other_names = ARRAY(
        SELECT DISTINCT n
        FROM unnest(k.other_listed_venue_names || d.other_listed_venue_names
                    || ARRAY[k.listed_venue_name, d.listed_venue_name]) AS n
        WHERE n IS NOT NULL AND n <> p.listed_name
        ORDER BY n)
FROM events k, events d
WHERE k.id = p.keep_id AND d.id = p.drop_id;

INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'events changed', to_jsonb(e) FROM events e JOIN _merge_pairs p ON e.id = p.keep_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'events deleted', to_jsonb(e) FROM events e JOIN _merge_pairs p ON e.id = p.drop_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'concert_artists deleted', to_jsonb(c) FROM concert_artists c JOIN _merge_pairs p ON c.event_id = p.drop_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_journeys deleted', to_jsonb(j) FROM ticket_journeys j JOIN _merge_pairs p ON j.event_id = p.drop_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sale_events deleted', to_jsonb(l) FROM ticket_sale_events l JOIN _merge_pairs p ON l.event_id = p.drop_id;

-- As Concert.Merge does (design D3): copy performers, ticket journeys and
-- ticket sale links to the kept event; a fan's journey on the kept event wins.
INSERT INTO concerts (event_id)
SELECT keep_id FROM _merge_pairs
ON CONFLICT DO NOTHING;
INSERT INTO concert_artists (event_id, artist_id)
SELECT p.keep_id, c.artist_id FROM concert_artists c JOIN _merge_pairs p ON c.event_id = p.drop_id
ON CONFLICT DO NOTHING;
INSERT INTO ticket_journeys (user_id, event_id, status, status_set_by)
SELECT j.user_id, p.keep_id, j.status, j.status_set_by FROM ticket_journeys j JOIN _merge_pairs p ON j.event_id = p.drop_id
ON CONFLICT (user_id, event_id) DO NOTHING;
INSERT INTO ticket_sale_events (ticket_sale_id, event_id)
SELECT l.ticket_sale_id, p.keep_id FROM ticket_sale_events l JOIN _merge_pairs p ON l.event_id = p.drop_id
ON CONFLICT DO NOTHING;

-- Delete the newer event first: it may hold the slot (venue, date, start time)
-- of uq_events_natural_key that the kept event takes next.
DELETE FROM events e USING _merge_pairs p WHERE e.id = p.drop_id;

UPDATE events e
SET listed_venue_name = p.listed_name,
    local_start_time = COALESCE(e.local_start_time, p.drop_start),
    local_open_time = COALESCE(e.local_open_time, p.drop_open),
    other_listed_venue_names = p.other_names
FROM _merge_pairs p
WHERE e.id = p.keep_id;

-- ----- 2. Split TOUR Series: fold each group into its oldest Series -----
-- Ayron Jones' two "TOUR DATES" Series are left alone (a generic title).
DROP TABLE IF EXISTS _merge_series;
CREATE TABLE _merge_series (
    member_id UUID PRIMARY KEY,
    canonical_id UUID NOT NULL
);
INSERT INTO _merge_series (canonical_id, member_id) VALUES
    -- Bruno Mars "The Romantic Tour"
    ('01a05c3c-ac85-716a-b105-930e403db89c', '01a10f26-529a-7a61-ace0-b6727773a9e8'),
    -- coldrain "OPTIMIZE = OPTDEMISE JAPAN TOUR 2026"
    ('01a05c4b-cb67-7169-ad46-0c665e758eac', '01a10f33-ab1a-7823-b53d-621434ec8ebb'),
    -- ELLEGARDEN "Bad For Education Tour II (2026)"
    ('01a05c37-d7c8-76dc-b321-f5da2840d644', '01a10f21-4be9-71e5-aaaa-524062dce42c'),
    ('01a05c37-d7c8-76dc-b321-f5da2840d644', '01a1b020-e57d-7310-94a3-02b6ea09eb6d'),
    -- Kevin Atwater "Blush Red Tour"
    ('01a05c5c-5b5c-709a-9807-94e5bf164fbe', '01a19674-deb2-75d3-8894-32f9874ef4c3'),
    -- King Gnu "King Gnu US TOUR '27"
    ('01a1b016-fd22-77c2-86f2-fe8fc85f5822', '01a1f831-388e-7ef0-a3b6-534f85e46869'),
    -- LE SSERAFIM "2026 LE SSERAFIM TOUR 'PUREFLOW'"
    ('01a10f2c-a18f-7b09-a45a-f629fc4636b6', '01a1965a-ec29-7dcd-ac7c-9be1285df3f8'),
    -- Mrs. GREEN APPLE "Mrs. GREEN APPLE Ringo Jam Tour “SHADOWS”"
    ('019f8cd8-774e-77ea-9047-973899a4b6a6', '01a21c3d-7ead-7162-b350-1251f02bc077'),
    -- Phoebe Bridgers "The Lost Tour"
    ('01a05c80-98d4-7618-8a25-44e2f1dfcc21', '01a1de79-4fb4-7a54-83e0-93dd8b69d502'),
    -- Sumika "sumika Live Tour 2027 『Laurens Library』"
    ('01a05c72-915d-7d63-a947-783dccb2fb17', '01a10f4b-aab1-7f84-b573-530b1ac2202f'),
    -- UVERworld "UVERworld LIVE “危ない” TOUR 2026"
    ('019fabcc-037e-71f4-a5a2-58e5a4356e82', '01a11fe6-f416-7474-b847-bfcf52238390'),
    -- Vaundy "Vaundy JAPAN ARENA TOUR 2027-2028"
    ('019f8cd1-3b21-77c9-94a1-a9db8b0e0e29', '01a11fe4-af3a-78d9-8316-8f21b63d31f5'),
    ('019f8cd1-3b21-77c9-94a1-a9db8b0e0e29', '01a1c4b0-8903-7d8a-bc3f-64cbb61582ca'),
    -- 米津玄師 "KENSHI YONEZU 2026-2027 WORLD TOUR / GHOST"
    ('01a15364-9aa2-787f-9bf1-182a858ee29f', '01a1a0a3-b6a0-7867-8238-37105df42523');

-- Skip a member that is gone, or whose canonical Series is gone or no longer a discovered TOUR.
DELETE FROM _merge_series m
WHERE NOT EXISTS (SELECT 1 FROM series s WHERE s.id = m.member_id AND s.organizer_id IS NULL)
   OR NOT EXISTS (SELECT 1 FROM series s WHERE s.id = m.canonical_id AND s.organizer_id IS NULL AND s.type = 'TOUR');

-- Discovered ticket sales that would collide on uq_ticket_sales_discovered_convergence
-- (series_id, method, sale_start_local_date) once on one Series: keep the
-- first found, drop the others.
DROP TABLE IF EXISTS _merge_sales;
CREATE TABLE _merge_sales AS
WITH grp AS (
    SELECT DISTINCT canonical_id, canonical_id AS series_id FROM _merge_series
    UNION ALL
    SELECT canonical_id, member_id FROM _merge_series
), ranked AS (
    SELECT t.id,
           first_value(t.id) OVER (
               PARTITION BY g.canonical_id, t.method, t.sale_start_local_date
               ORDER BY t.find_time, t.id::text) AS keep_id
    FROM ticket_sales t JOIN grp g ON g.series_id = t.series_id
    WHERE t.find_time IS NOT NULL
)
SELECT id AS drop_id, keep_id FROM ranked WHERE id <> keep_id;

INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sales deleted', to_jsonb(t) FROM ticket_sales t JOIN _merge_sales m ON t.id = m.drop_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sale_reminders deleted', to_jsonb(r) FROM ticket_sale_reminders r JOIN _merge_sales m ON r.ticket_sale_id = m.drop_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sale_events deleted', to_jsonb(l) FROM ticket_sale_events l JOIN _merge_sales m ON l.ticket_sale_id = m.drop_id;

-- The kept sale takes over the dropped one's reminder log (so a fan already
-- reminded is not reminded again) and its events.
INSERT INTO ticket_sale_reminders (id, user_id, ticket_sale_id, stage, send_time)
SELECT uuidv7(), r.user_id, m.keep_id, r.stage, r.send_time
FROM ticket_sale_reminders r JOIN _merge_sales m ON r.ticket_sale_id = m.drop_id
ON CONFLICT (user_id, ticket_sale_id, stage) DO NOTHING;
INSERT INTO ticket_sale_events (ticket_sale_id, event_id)
SELECT m.keep_id, l.event_id FROM ticket_sale_events l JOIN _merge_sales m ON l.ticket_sale_id = m.drop_id
ON CONFLICT DO NOTHING;
DELETE FROM ticket_sales t USING _merge_sales m WHERE t.id = m.drop_id;

INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'events moved', to_jsonb(e) FROM events e JOIN _merge_series m ON e.series_id = m.member_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'concert_candidates moved', to_jsonb(c) FROM concert_candidates c JOIN _merge_series m ON c.series_id = m.member_id;
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sales moved', to_jsonb(t) FROM ticket_sales t JOIN _merge_series m ON t.series_id = m.member_id;

UPDATE events e SET series_id = m.canonical_id FROM _merge_series m WHERE e.series_id = m.member_id;
UPDATE concert_candidates c SET series_id = m.canonical_id FROM _merge_series m WHERE c.series_id = m.member_id;
UPDATE ticket_sales t SET series_id = m.canonical_id FROM _merge_series m WHERE t.series_id = m.member_id;

-- The canonical Series keeps the oldest ticket sale search of its group.
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'ticket_sale_searches changed', to_jsonb(s)
FROM ticket_sale_searches s
WHERE s.series_id IN (SELECT canonical_id FROM _merge_series UNION SELECT member_id FROM _merge_series);
INSERT INTO ticket_sale_searches (series_id, search_time)
SELECT g.canonical_id, min(s.search_time)
FROM (SELECT DISTINCT canonical_id, canonical_id AS series_id FROM _merge_series
      UNION ALL SELECT canonical_id, member_id FROM _merge_series) g
JOIN ticket_sale_searches s ON s.series_id = g.series_id
GROUP BY g.canonical_id
ON CONFLICT (series_id) DO UPDATE SET search_time = EXCLUDED.search_time;

-- Delete the emptied member Series (their ticket sale searches go with them).
-- A member that still holds anything is kept.
INSERT INTO _merge_same_concerts_backup (entity, row_data)
SELECT 'series deleted', to_jsonb(s) FROM series s JOIN _merge_series m ON s.id = m.member_id
WHERE NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM concert_candidates c WHERE c.series_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM ticket_sales t WHERE t.series_id = s.id);
DELETE FROM series s USING _merge_series m
WHERE s.id = m.member_id
  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM concert_candidates c WHERE c.series_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM ticket_sales t WHERE t.series_id = s.id);

DROP TABLE _merge_pairs;
DROP TABLE _merge_series;
DROP TABLE _merge_sales;
