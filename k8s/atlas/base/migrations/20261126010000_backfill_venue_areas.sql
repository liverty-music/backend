-- Migration B of OpenSpec change globalize-venues (design Decision 16 step 3
-- and Decision 18): give every Venue stored before release 1 its area and time
-- zone from the owner's review of the backfill, and delete or repair the rows
-- the review found wrong. The ids are Venue and Series rows read from
-- production on 2026-11-26; on a database without them every statement is a
-- no-op.

-- Refuse to delete a show that something restricts or that a fan paid for.
DO $$
BEGIN
  IF EXISTS (
    SELECT 1 FROM events e WHERE e.venue_id IN (
    '019e3433-265b-7494-86c0-db9ee468a9ab',
    '01a05c53-6101-7430-b935-90847798a000',
    '01a05c53-642d-70cb-8696-0154903cee2b',
    '01a05c5b-2a1d-71aa-bcc7-fc539e7e8ce6',
    '01a12522-296a-71fc-8208-2cbab0681f7a',
    '01a1589b-d3df-7f91-8d28-c328e65f5a62',
    '01a19681-2a56-761e-ad91-4855a4750801',
    '01a19681-2bda-7597-ad0d-4fb455d2bb84',
    '01a19681-2cbc-76dc-8ef9-73fd934d8e6c',
    '01a19681-2dbc-7a8a-a9f6-42e71aad4d41',
    '01a19681-2e96-7502-b1f6-41a8182c8016',
    '01a19681-2f74-7bb9-a4a6-fed071f54ed6',
    '01a19681-3044-7666-80c0-e06bfa0366ff',
    '01a19681-313d-70ac-9e67-f63cdf7bc9e7',
    '01a19681-3215-75f6-a0a8-f552a11d9953',
    '01a19681-32f5-7518-9ba4-d245e4b153cf',
    '01a19681-33d3-7639-b200-815de87f39d2',
    '01a19681-3527-7f31-832e-d0be62b154d6'
    ) AND (
         EXISTS (SELECT 1 FROM tickets x WHERE x.event_id = e.id)
      OR EXISTS (SELECT 1 FROM ticket_stocks x WHERE x.event_id = e.id)
      OR EXISTS (SELECT 1 FROM settlements x WHERE x.event_id = e.id)
      OR EXISTS (SELECT 1 FROM scanners x WHERE x.event_id = e.id)
      OR EXISTS (SELECT 1 FROM admissions x WHERE x.event_id = e.id)
      OR EXISTS (SELECT 1 FROM rejected_scans x WHERE x.event_id = e.id)
    )
  ) OR EXISTS (
    SELECT 1 FROM ticket_sales x WHERE x.series_id IN (
    '01a05c53-6012-75f2-a5ce-b13edbc2e3c6',
    '01a05c53-6338-7249-bd57-3bf8ca9b8c46',
    '01a05c53-643b-7acb-9091-0340d913bf23',
    '01a05c5b-2948-7917-9cf6-f7be218afad0',
    '01a1589b-d2f4-7ff8-938c-41ec58635d75',
    '01a19681-294d-72be-b770-2222f9c3d201',
    '01a19681-33e8-7ebd-bee2-6dda3a6045f0'
    )
  ) OR EXISTS (
    SELECT 1 FROM ticket_sale_searches x WHERE x.series_id IN (
    '01a05c53-6012-75f2-a5ce-b13edbc2e3c6',
    '01a05c53-6338-7249-bd57-3bf8ca9b8c46',
    '01a05c53-643b-7acb-9091-0340d913bf23',
    '01a05c5b-2948-7917-9cf6-f7be218afad0',
    '01a1589b-d2f4-7ff8-938c-41ec58635d75',
    '01a19681-294d-72be-b770-2222f9c3d201',
    '01a19681-33e8-7ebd-bee2-6dda3a6045f0'
    )
  ) THEN
    RAISE EXCEPTION 'a show or series to be deleted has tickets, settlements, scanners or ticket sales';
  END IF;
END $$;

-- Venues that are no place a fan can go to (a private event, a ship, a venue
-- not announced, a city name only): their candidates, their Events, then the
-- discovered Series left empty, then the Venues.

DELETE FROM concert_candidates WHERE admin_area IS NULL AND listed_venue_name IN (
    '-STAY TUNED-',
    'Asheville, NC',
    'Atlanta, GA',
    'Austin, TX',
    'Carnival Panorama Pool Deck',
    'Celebrity Silhouette',
    'Columbus, OH',
    'Hamden, CT',
    'Kings County, NY',
    'Liverpool, UK',
    'Lowell, MA',
    'Mobile, AL',
    'Morris Plains, NJ',
    'New Orleans, LA',
    'PRIVATE',
    'Philadelphia, PA',
    'Presence Arena',
    'West Chicago, IL'
);

DELETE FROM events WHERE venue_id IN (
    '019e3433-265b-7494-86c0-db9ee468a9ab',
    '01a05c53-6101-7430-b935-90847798a000',
    '01a05c53-642d-70cb-8696-0154903cee2b',
    '01a05c5b-2a1d-71aa-bcc7-fc539e7e8ce6',
    '01a12522-296a-71fc-8208-2cbab0681f7a',
    '01a1589b-d3df-7f91-8d28-c328e65f5a62',
    '01a19681-2a56-761e-ad91-4855a4750801',
    '01a19681-2bda-7597-ad0d-4fb455d2bb84',
    '01a19681-2cbc-76dc-8ef9-73fd934d8e6c',
    '01a19681-2dbc-7a8a-a9f6-42e71aad4d41',
    '01a19681-2e96-7502-b1f6-41a8182c8016',
    '01a19681-2f74-7bb9-a4a6-fed071f54ed6',
    '01a19681-3044-7666-80c0-e06bfa0366ff',
    '01a19681-313d-70ac-9e67-f63cdf7bc9e7',
    '01a19681-3215-75f6-a0a8-f552a11d9953',
    '01a19681-32f5-7518-9ba4-d245e4b153cf',
    '01a19681-33d3-7639-b200-815de87f39d2',
    '01a19681-3527-7f31-832e-d0be62b154d6'
);

DELETE FROM series s WHERE s.id IN (
    '01a05c53-6012-75f2-a5ce-b13edbc2e3c6',
    '01a05c53-6338-7249-bd57-3bf8ca9b8c46',
    '01a05c53-643b-7acb-9091-0340d913bf23',
    '01a05c5b-2948-7917-9cf6-f7be218afad0',
    '01a1589b-d2f4-7ff8-938c-41ec58635d75',
    '01a19681-294d-72be-b770-2222f9c3d201',
    '01a19681-33e8-7ebd-bee2-6dda3a6045f0'
) AND NOT EXISTS (SELECT 1 FROM events e WHERE e.series_id = s.id)
  AND NOT EXISTS (SELECT 1 FROM concert_candidates c WHERE c.series_id = s.id);

-- Two rows of one venue: move the Events to the row that has the area. An
-- Event whose slot both rows hold stays, and so does its row, which then has no
-- area for the next migration's check to find.

-- Grand Theatre at The Grand Sierra Resort
UPDATE events AS e SET venue_id = '01a1347e-e9eb-7d6a-b6cd-eb95bc4941df' WHERE e.venue_id = '01a05c4e-c104-7791-b8d2-8b453f5bcd9a'
  AND NOT EXISTS (SELECT 1 FROM events f WHERE f.venue_id = '01a1347e-e9eb-7d6a-b6cd-eb95bc4941df' AND f.local_event_date = e.local_event_date AND f.start_time IS NOT DISTINCT FROM e.start_time);
DELETE FROM venues WHERE id = '01a05c4e-c104-7791-b8d2-8b453f5bcd9a' AND NOT EXISTS (SELECT 1 FROM events e WHERE e.venue_id = '01a05c4e-c104-7791-b8d2-8b453f5bcd9a');

-- MilkBoy
UPDATE events AS e SET venue_id = '01a14e3d-e07f-7a54-9349-5a3f44708702' WHERE e.venue_id = '01a05c51-2089-7c71-917b-e49f9363c708'
  AND NOT EXISTS (SELECT 1 FROM events f WHERE f.venue_id = '01a14e3d-e07f-7a54-9349-5a3f44708702' AND f.local_event_date = e.local_event_date AND f.start_time IS NOT DISTINCT FROM e.start_time);
DELETE FROM venues WHERE id = '01a05c51-2089-7c71-917b-e49f9363c708' AND NOT EXISTS (SELECT 1 FROM events e WHERE e.venue_id = '01a05c51-2089-7c71-917b-e49f9363c708');

DELETE FROM venues WHERE id IN (
    '019e3433-265b-7494-86c0-db9ee468a9ab',
    '01a05c53-6101-7430-b935-90847798a000',
    '01a05c53-642d-70cb-8696-0154903cee2b',
    '01a05c5b-2a1d-71aa-bcc7-fc539e7e8ce6',
    '01a12522-296a-71fc-8208-2cbab0681f7a',
    '01a1589b-d3df-7f91-8d28-c328e65f5a62',
    '01a19681-2a56-761e-ad91-4855a4750801',
    '01a19681-2bda-7597-ad0d-4fb455d2bb84',
    '01a19681-2cbc-76dc-8ef9-73fd934d8e6c',
    '01a19681-2dbc-7a8a-a9f6-42e71aad4d41',
    '01a19681-2e96-7502-b1f6-41a8182c8016',
    '01a19681-2f74-7bb9-a4a6-fed071f54ed6',
    '01a19681-3044-7666-80c0-e06bfa0366ff',
    '01a19681-313d-70ac-9e67-f63cdf7bc9e7',
    '01a19681-3215-75f6-a0a8-f552a11d9953',
    '01a19681-32f5-7518-9ba4-d245e4b153cf',
    '01a19681-33d3-7639-b200-815de87f39d2',
    '01a19681-3527-7f31-832e-d0be62b154d6'
);

-- Venues no Event uses.
DELETE FROM venues v WHERE v.id IN (
    '019e3432-7c14-749c-9556-d96e1f2cf4a1',
    '019e3432-7cb4-7410-99e7-fcab122c7932',
    '019e3432-7e44-7bcf-9241-a66984b6007b',
    '019e3432-89a4-7269-ad55-aab89b681550',
    '019e3432-8a84-7b08-8dc2-701c1b108a09',
    '019e3432-af7f-7b3c-a834-a1574c3f6c42',
    '019e3a37-148d-7ed1-b647-769f2d9920c9',
    '019e3a5f-9da8-7c16-91d8-eef2a65a336f',
    '019e3a5f-9eaf-788d-90d6-f5d5ce128db6',
    '019e3a5f-9f4d-773c-9750-e9004418f685',
    '019e44d6-0df7-72a4-b789-4440d08cb8ac',
    '019e4633-0e64-73ff-a2cb-fa9f82af81a3',
    '019e4eeb-122e-7e85-827a-c6a06d74f89c',
    '019e68b3-3098-7070-8626-af92889758bf',
    '019e782c-766f-76b5-a996-276bc8ee579e',
    '019e826e-f3fa-7c4d-bf40-b2e171947ebd',
    '019e8275-3bcd-736d-a1b9-fde9fcf08b6a',
    '019e8275-3cfc-7b6c-bc9c-7983051a7cf6',
    '019e8275-3ec3-705d-b6e6-a991252b1e31',
    '019e8275-3f9f-7db1-9a25-ec569b24083a',
    '019e8275-406a-7652-9226-469cb1d16c52',
    '019e8275-411d-72e3-836e-4f2641142e7d',
    '019e8275-4205-7f3e-aa3e-a29dbe84e24b',
    '019e8275-42d1-7635-a29b-d097da83656c',
    '01a05c5a-4e18-7566-a94e-06371e62c8a5',
    '01a05c91-9ccc-7bdc-8e55-f74491b116db',
    '01a06731-5c91-7778-b5ff-e3ad47c4759a',
    '01a06733-4352-77e7-a425-5b62023a7694',
    '01a124b2-f6ad-7fca-8c2f-2c979f9f07ab',
    '01a124ce-568b-7a2d-8e6d-a745632d4905',
    '01a126c0-409b-7609-ba95-cd0995e3146c'
) AND NOT EXISTS (SELECT 1 FROM events e WHERE e.venue_id = v.id);

-- A candidate that resolved to one of those places loses it too, so approving
-- it cannot give the place back. A candidate that would then collide with
-- another of the same artist, date and listed name without a place (only one
-- such row can exist) is removed first.
DELETE FROM concert_candidates AS c
WHERE c.resolved_place_id IN (
  SELECT google_place_id FROM venues WHERE google_place_id IS NOT NULL AND id IN (
    '019e3939-1296-7e42-8b05-0e7856b20248',
    '019e4633-0d88-7d5b-9479-7ea13cd56582',
    '01a05c58-44f7-7b48-b295-f9647a40ba34',
    '01a05c58-46c0-70e4-81c2-fd1d607d1f83',
    '01a05c58-4ea9-7d51-9aed-ffba0daf3428',
    '01a05c58-5059-7258-8e1a-59160b92f541',
    '01a05c5c-5d5d-780e-a4da-1337f3197192',
    '01a05c5c-669b-7c22-8695-9cc4cde5ae6f',
    '01a05c5c-69f9-7c02-82ce-6c422a914e97',
    '01a05c5c-6e60-764d-b1a2-8fa0cc3c7252',
    '01a05c5c-6fd8-7fea-b069-05efd25741db',
    '01a05c5c-7110-766f-a678-d68ab202749a',
    '01a05c5c-740d-71ef-95e2-d5f537957366',
    '01a05c5e-d627-7cdc-b572-8ebf334cf73e',
    '01a05c5e-dafc-7ea8-aa5e-6d7397ac8d9f',
    '01a05c64-3bfa-7ca6-800c-ab48275e248f',
    '01a05c64-3d9f-7e96-839c-8d58b9dd1aae',
    '01a05c6c-9942-782f-b355-0fa8cf5126f6',
    '01a05c6c-9be4-753e-812b-7f69d8ed39e5',
    '01a05c7e-e9c5-7dc8-abbb-92d27e647918',
    '01a05c80-b567-78cf-933a-23c6c8a5d787',
    '01a0610f-d79e-701d-af64-fda58f1c92e6',
    '01a10f24-dd38-775a-add4-125aef8206cc',
    '01a11b3d-1f8d-7423-bcce-15bf5679b1e6',
    '01a11b3d-2077-747b-99f6-2c99fbe90fa6',
    '01a11b3d-2257-71e5-a9f4-5b7dd44fef64',
    '01a1251d-e9ed-7bad-8782-0eaca65e2807',
    '01a1347e-fdca-7704-b916-478a9a312f9d',
    '01a14e3d-dc4f-7e81-9532-ad829ff6d39f',
    '01a1de80-d750-758d-a120-142047446825'
  )
) AND EXISTS (
  SELECT 1 FROM concert_candidates o
  WHERE o.artist_id = c.artist_id AND o.local_date = c.local_date AND o.listed_venue_name = c.listed_venue_name
    AND (o.resolved_place_id IS NULL OR (o.id < c.id AND o.resolved_place_id IN (
  SELECT google_place_id FROM venues WHERE google_place_id IS NOT NULL AND id IN (
    '019e3939-1296-7e42-8b05-0e7856b20248',
    '019e4633-0d88-7d5b-9479-7ea13cd56582',
    '01a05c58-44f7-7b48-b295-f9647a40ba34',
    '01a05c58-46c0-70e4-81c2-fd1d607d1f83',
    '01a05c58-4ea9-7d51-9aed-ffba0daf3428',
    '01a05c58-5059-7258-8e1a-59160b92f541',
    '01a05c5c-5d5d-780e-a4da-1337f3197192',
    '01a05c5c-669b-7c22-8695-9cc4cde5ae6f',
    '01a05c5c-69f9-7c02-82ce-6c422a914e97',
    '01a05c5c-6e60-764d-b1a2-8fa0cc3c7252',
    '01a05c5c-6fd8-7fea-b069-05efd25741db',
    '01a05c5c-7110-766f-a678-d68ab202749a',
    '01a05c5c-740d-71ef-95e2-d5f537957366',
    '01a05c5e-d627-7cdc-b572-8ebf334cf73e',
    '01a05c5e-dafc-7ea8-aa5e-6d7397ac8d9f',
    '01a05c64-3bfa-7ca6-800c-ab48275e248f',
    '01a05c64-3d9f-7e96-839c-8d58b9dd1aae',
    '01a05c6c-9942-782f-b355-0fa8cf5126f6',
    '01a05c6c-9be4-753e-812b-7f69d8ed39e5',
    '01a05c7e-e9c5-7dc8-abbb-92d27e647918',
    '01a05c80-b567-78cf-933a-23c6c8a5d787',
    '01a0610f-d79e-701d-af64-fda58f1c92e6',
    '01a10f24-dd38-775a-add4-125aef8206cc',
    '01a11b3d-1f8d-7423-bcce-15bf5679b1e6',
    '01a11b3d-2077-747b-99f6-2c99fbe90fa6',
    '01a11b3d-2257-71e5-a9f4-5b7dd44fef64',
    '01a1251d-e9ed-7bad-8782-0eaca65e2807',
    '01a1347e-fdca-7704-b916-478a9a312f9d',
    '01a14e3d-dc4f-7e81-9532-ad829ff6d39f',
    '01a1de80-d750-758d-a120-142047446825'
  )
  )))
);

UPDATE concert_candidates SET resolved_place_id = NULL WHERE resolved_place_id IN (
  SELECT google_place_id FROM venues WHERE google_place_id IS NOT NULL AND id IN (
    '019e3939-1296-7e42-8b05-0e7856b20248',
    '019e4633-0d88-7d5b-9479-7ea13cd56582',
    '01a05c58-44f7-7b48-b295-f9647a40ba34',
    '01a05c58-46c0-70e4-81c2-fd1d607d1f83',
    '01a05c58-4ea9-7d51-9aed-ffba0daf3428',
    '01a05c58-5059-7258-8e1a-59160b92f541',
    '01a05c5c-5d5d-780e-a4da-1337f3197192',
    '01a05c5c-669b-7c22-8695-9cc4cde5ae6f',
    '01a05c5c-69f9-7c02-82ce-6c422a914e97',
    '01a05c5c-6e60-764d-b1a2-8fa0cc3c7252',
    '01a05c5c-6fd8-7fea-b069-05efd25741db',
    '01a05c5c-7110-766f-a678-d68ab202749a',
    '01a05c5c-740d-71ef-95e2-d5f537957366',
    '01a05c5e-d627-7cdc-b572-8ebf334cf73e',
    '01a05c5e-dafc-7ea8-aa5e-6d7397ac8d9f',
    '01a05c64-3bfa-7ca6-800c-ab48275e248f',
    '01a05c64-3d9f-7e96-839c-8d58b9dd1aae',
    '01a05c6c-9942-782f-b355-0fa8cf5126f6',
    '01a05c6c-9be4-753e-812b-7f69d8ed39e5',
    '01a05c7e-e9c5-7dc8-abbb-92d27e647918',
    '01a05c80-b567-78cf-933a-23c6c8a5d787',
    '01a0610f-d79e-701d-af64-fda58f1c92e6',
    '01a10f24-dd38-775a-add4-125aef8206cc',
    '01a11b3d-1f8d-7423-bcce-15bf5679b1e6',
    '01a11b3d-2077-747b-99f6-2c99fbe90fa6',
    '01a11b3d-2257-71e5-a9f4-5b7dd44fef64',
    '01a1251d-e9ed-7bad-8782-0eaca65e2807',
    '01a1347e-fdca-7704-b916-478a9a312f9d',
    '01a14e3d-dc4f-7e81-9532-ad829ff6d39f',
    '01a1de80-d750-758d-a120-142047446825'
  )
);

-- The map search had picked a same-named place elsewhere: drop that place so
-- the Venue keeps only its area.
UPDATE venues SET google_place_id = NULL, latitude = NULL, longitude = NULL, coordinates_fetch_time = NULL WHERE id IN (
    '019e3939-1296-7e42-8b05-0e7856b20248',
    '019e4633-0d88-7d5b-9479-7ea13cd56582',
    '01a05c58-44f7-7b48-b295-f9647a40ba34',
    '01a05c58-46c0-70e4-81c2-fd1d607d1f83',
    '01a05c58-4ea9-7d51-9aed-ffba0daf3428',
    '01a05c58-5059-7258-8e1a-59160b92f541',
    '01a05c5c-5d5d-780e-a4da-1337f3197192',
    '01a05c5c-669b-7c22-8695-9cc4cde5ae6f',
    '01a05c5c-69f9-7c02-82ce-6c422a914e97',
    '01a05c5c-6e60-764d-b1a2-8fa0cc3c7252',
    '01a05c5c-6fd8-7fea-b069-05efd25741db',
    '01a05c5c-7110-766f-a678-d68ab202749a',
    '01a05c5c-740d-71ef-95e2-d5f537957366',
    '01a05c5e-d627-7cdc-b572-8ebf334cf73e',
    '01a05c5e-dafc-7ea8-aa5e-6d7397ac8d9f',
    '01a05c64-3bfa-7ca6-800c-ab48275e248f',
    '01a05c64-3d9f-7e96-839c-8d58b9dd1aae',
    '01a05c6c-9942-782f-b355-0fa8cf5126f6',
    '01a05c6c-9be4-753e-812b-7f69d8ed39e5',
    '01a05c7e-e9c5-7dc8-abbb-92d27e647918',
    '01a05c80-b567-78cf-933a-23c6c8a5d787',
    '01a0610f-d79e-701d-af64-fda58f1c92e6',
    '01a10f24-dd38-775a-add4-125aef8206cc',
    '01a11b3d-1f8d-7423-bcce-15bf5679b1e6',
    '01a11b3d-2077-747b-99f6-2c99fbe90fa6',
    '01a11b3d-2257-71e5-a9f4-5b7dd44fef64',
    '01a1251d-e9ed-7bad-8782-0eaca65e2807',
    '01a1347e-fdca-7704-b916-478a9a312f9d',
    '01a14e3d-dc4f-7e81-9532-ad829ff6d39f',
    '01a1de80-d750-758d-a120-142047446825'
);

-- A listed name cut short.
UPDATE venues SET name = 'SINGAPORE INDOOR STADIUM', listed_venue_name = 'SINGAPORE INDOOR STADIUM' WHERE id = '01a15364-9f67-70a7-a514-5b8582020c94';

-- The reviewed area and time zone of each Venue.
CREATE TEMPORARY TABLE venue_review (id UUID PRIMARY KEY, admin_area TEXT NOT NULL, time_zone TEXT NOT NULL);

INSERT INTO venue_review (id, admin_area, time_zone) VALUES
    ('019e3432-7d64-76ee-8b4b-89a41e5b4652', 'TW-TPE', 'Asia/Taipei'),
    ('019e3432-7ef5-7dfb-8387-e55cc90ed81a', 'KR-11', 'Asia/Seoul'),
    ('019e3432-881a-73e9-a1b2-e8e8ee3c0598', 'KR-41', 'Asia/Seoul'),
    ('019e3432-88f5-77be-af98-29a5399c3b69', 'HK', 'Asia/Hong_Kong'),
    ('019e3432-8b57-741f-85c1-0569885c1460', 'US-MA', 'America/New_York'),
    ('019e3432-8c0d-7049-b605-808fe83ddfa5', 'US-NY', 'America/New_York'),
    ('019e3432-8cb2-7d06-9a71-a8ab89ebc4fa', 'CA-ON', 'America/Toronto'),
    ('019e3432-8d63-7cc6-8590-37d4c7a502ef', 'US-WA', 'America/Los_Angeles'),
    ('019e3432-8e23-724e-9cac-b02b59d5e22b', 'US-CA', 'America/Los_Angeles'),
    ('019e3432-8edd-7001-bc21-6c798f7a84e7', 'US-CA', 'America/Los_Angeles'),
    ('019e3432-ad83-727e-969e-fc710797f060', 'SG-01', 'Asia/Singapore'),
    ('019e3432-ae21-7411-aef3-a73e8b9ba580', 'KR-41', 'Asia/Seoul'),
    ('019e3432-aec3-75fb-b1a0-34431b3d562b', 'TH-10', 'Asia/Bangkok'),
    ('019e3432-cbb0-7e3d-83a5-17446bdc42b1', 'SG-05', 'Asia/Singapore'),
    ('019e3432-cd9c-75d8-9d17-6229b09f6d2a', 'TH-10', 'Asia/Bangkok'),
    ('019e3433-24ef-7118-9e56-f1732f9adfd9', 'KR-28', 'Asia/Seoul'),
    ('019e3939-1296-7e42-8b05-0e7856b20248', 'SG-01', 'Asia/Singapore'),
    ('019e3939-1338-785c-88b7-cba6c07ed1b4', 'HK', 'Asia/Hong_Kong'),
    ('019e3939-1683-785a-a42f-dfb1965dc0ea', 'TW-NWT', 'Asia/Taipei'),
    ('019e3a5f-a00c-7a11-8e7d-b59a7d16f2a5', 'US-CA', 'America/Los_Angeles'),
    ('019e44d6-0f08-76d3-972c-eb05a44095c1', 'TW-NWT', 'Asia/Taipei'),
    ('019e4633-0d88-7d5b-9479-7ea13cd56582', 'HK', 'Asia/Hong_Kong'),
    ('019e68b3-317a-716c-a5f7-225e25832e50', 'HK', 'Asia/Hong_Kong'),
    ('019e781e-1b47-7cc5-a7c2-177d6cf271da', 'HK', 'Asia/Hong_Kong'),
    ('019e782a-9176-72b3-af69-bca72025f5b1', 'US-IL', 'America/Chicago'),
    ('019e8275-43de-7b34-a578-5bcc79fa4b3c', 'US-PA', 'America/New_York'),
    ('019e8275-44e9-73b1-8497-750a33c69100', 'US-MA', 'America/New_York'),
    ('019e8275-45e9-7676-bf8a-19f2635d0c59', 'US-IN', 'America/Indiana/Indianapolis'),
    ('019e8275-46f5-7024-8fa7-b01f11628f23', 'US-FL', 'America/New_York'),
    ('019e8275-47e3-752c-8b81-3d9d71b10e7a', 'US-LA', 'America/Chicago'),
    ('019e8275-48be-70c5-8a75-acf90fec8afd', 'US-FL', 'America/New_York'),
    ('019e8275-4990-7297-ab05-b72e1ee37833', 'US-TX', 'America/Chicago'),
    ('019e8275-4a91-748a-bab3-ecb27a759885', 'US-CO', 'America/Denver'),
    ('019e8275-4b93-785f-baa3-efb38e655756', 'US-CA', 'America/Los_Angeles'),
    ('019e8275-4cbf-7017-96e2-952b94ed896b', 'US-CA', 'America/Los_Angeles'),
    ('019e8275-4ddd-7413-aeb3-4388ef85bb31', 'CA-BC', 'America/Vancouver'),
    ('019e8275-4ee4-748f-a7a9-d8b438fb2c6c', 'MX-CMX', 'America/Mexico_City'),
    ('019f8cde-240d-740f-8c7e-9190eac1fb43', 'AT-3', 'Europe/Vienna'),
    ('019f8cde-44b1-711a-adb8-536e720cdfd2', 'FR-IDF', 'Europe/Paris'),
    ('019f8cde-7668-75e5-b9b3-894308eca305', 'NL-NB', 'Europe/Amsterdam'),
    ('019f8cde-884e-723f-9df3-9568ff7eca77', 'DE-HE', 'Europe/Berlin'),
    ('019f8cde-8d14-70fe-ad68-43a172824a9e', 'GB-ENG', 'Europe/London'),
    ('019fa1b7-d59d-72f3-9068-e23fc3507b13', 'TW-KHH', 'Asia/Taipei'),
    ('019ffa93-0c8d-7476-a818-b08b7c88c712', 'TW-TPE', 'Asia/Taipei'),
    ('019ffa98-140f-7328-b190-929b266eb420', 'JP-19', 'Asia/Tokyo'),
    ('01a05c37-de0c-7010-bd97-cc9c2577494c', 'KR-11', 'Asia/Seoul'),
    ('01a05c3a-7cd4-7c10-8c03-f3c83490ad60', 'US-WA', 'America/Los_Angeles'),
    ('01a05c3a-7dfc-7041-b547-15072b361a58', 'CA-BC', 'America/Vancouver'),
    ('01a05c3a-7f2a-7bb8-b317-0c934b3e5402', 'US-OR', 'America/Los_Angeles'),
    ('01a05c3a-804e-7576-bd3c-2ef07343f9d5', 'US-OR', 'America/Los_Angeles'),
    ('01a05c3a-817c-7297-8462-b3a8a197c690', 'US-WA', 'America/Los_Angeles'),
    ('01a05c3a-82a9-76f2-8e82-292283626e45', 'US-CA', 'America/Los_Angeles'),
    ('01a05c3a-83a1-7468-a132-6c0a9cc0e2d0', 'US-CA', 'America/Los_Angeles'),
    ('01a05c3a-84b2-773e-a4a6-6c9b4536cf7c', 'US-CA', 'America/Los_Angeles'),
    ('01a05c3a-85b8-7b0b-9dbd-3ae86f7cc39e', 'US-AZ', 'America/Phoenix'),
    ('01a05c3a-86ed-7aac-b78d-c998950caca0', 'US-UT', 'America/Denver'),
    ('01a05c3a-87ff-78d0-87c3-e93d622e8b3b', 'US-CO', 'America/Denver'),
    ('01a05c3a-890b-7bf1-b265-829fdb4f6f9a', 'US-MN', 'America/Chicago'),
    ('01a05c3a-8a1e-7949-a6d4-aa705cc35f58', 'US-MO', 'America/Chicago'),
    ('01a05c3a-8c1d-7953-8a17-80f0a3e6f0a1', 'CA-ON', 'America/Toronto'),
    ('01a05c3a-8d0e-7363-a68b-bb553d74918f', 'CA-QC', 'America/Toronto'),
    ('01a05c3a-8e05-7d52-bb61-c0bd85a91f08', 'US-MA', 'America/New_York'),
    ('01a05c3a-8f08-7302-87e1-ba8c64955e54', 'US-NY', 'America/New_York'),
    ('01a05c3a-9045-7fb9-acd7-22bd446cc896', 'US-NY', 'America/New_York'),
    ('01a05c3a-9175-7114-a4fa-92320e3bacae', 'US-PA', 'America/New_York'),
    ('01a05c3a-9259-777e-a8d0-9d3dd8f167a2', 'US-DC', 'America/New_York'),
    ('01a05c3a-936c-7d32-8cc1-09a407abfe1e', 'US-NC', 'America/New_York'),
    ('01a05c3a-947a-7f7c-9762-6c794f05d1f3', 'US-GA', 'America/New_York'),
    ('01a05c3b-93c7-7f61-88a4-70d3e5be7f9a', 'AU-SA', 'Australia/Adelaide'),
    ('01a05c3b-94b2-710f-b549-a92919bb2cb5', 'AU-QLD', 'Australia/Brisbane'),
    ('01a05c3b-95ff-772f-b1da-d6bae6240bc6', 'AU-WA', 'Australia/Perth'),
    ('01a05c3b-9831-778c-8f4d-e1ac138eb902', 'KR-11', 'Asia/Seoul'),
    ('01a05c3c-b80c-745a-ab79-65048dfc4ac7', 'AU-WA', 'Australia/Perth'),
    ('01a05c3c-b8cf-7bf0-a86d-2a6598c23126', 'AU-VIC', 'Australia/Melbourne'),
    ('01a05c3c-b9cf-7c81-8451-4f0765548bee', 'AU-QLD', 'Australia/Brisbane'),
    ('01a05c3c-ba9f-7c94-ac35-f9c2b53515ca', 'AU-NSW', 'Australia/Sydney'),
    ('01a05c3c-bbb3-79e4-9cbb-c83ac7dca298', 'NZ-AUK', 'Pacific/Auckland'),
    ('01a05c3c-be93-782d-8685-ff1ffed0f4d3', 'TW-KHH', 'Asia/Taipei'),
    ('01a05c3c-c02a-73fb-a4b7-0df30afb4962', 'PH-03', 'Asia/Manila'),
    ('01a05c44-0c5f-7dcc-9819-a595e479c80b', 'US-NV', 'America/Los_Angeles'),
    ('01a05c46-751f-7475-a8b8-cd4fb729bac3', 'US-MD', 'America/New_York'),
    ('01a05c47-d119-7df4-935e-7ec2a359967d', 'US-WA', 'America/Los_Angeles'),
    ('01a05c47-d22c-7a29-9823-bacdd2574c2d', 'US-CA', 'America/Los_Angeles'),
    ('01a05c47-d30e-79d8-92cd-d5b358fce490', 'US-AZ', 'America/Phoenix'),
    ('01a05c47-d3e8-767c-a64e-24586cc06b49', 'US-TX', 'America/Chicago'),
    ('01a05c47-d4c0-797d-aeb9-a075ad8a855c', 'US-FL', 'America/New_York'),
    ('01a05c47-d5a1-7f91-aed7-f34bd6deaecc', 'US-IL', 'America/Chicago'),
    ('01a05c47-d68c-71ed-8917-0896baacc819', 'US-DC', 'America/New_York'),
    ('01a05c47-d75f-7509-abac-a2ce2c2a3cf5', 'US-NJ', 'America/New_York'),
    ('01a05c47-d8ba-7d0c-8bd1-502c1cb2260f', 'GB-ENG', 'Europe/London'),
    ('01a05c47-d97e-7f60-bb84-a8f3d04b838b', 'NL-NH', 'Europe/Amsterdam'),
    ('01a05c47-da51-79c5-a713-1fcd3dc5b9c9', 'FR-IDF', 'Europe/Paris'),
    ('01a05c47-db47-7f1c-ac40-fcc46a625167', 'DK-84', 'Europe/Copenhagen'),
    ('01a05c47-dc27-7450-9345-7ab40078f36f', 'DE-BE', 'Europe/Berlin'),
    ('01a05c47-dcef-7ec0-961c-f84da78fca11', 'MO', 'Asia/Macau'),
    ('01a05c47-ddd5-78ae-8d60-4fe89a1dd178', 'TW-TAO', 'Asia/Taipei'),
    ('01a05c4b-c725-7ea6-b831-600fd1eeafcf', 'AU-WA', 'Australia/Perth'),
    ('01a05c4b-c832-7619-ba77-f16bcd8fffcf', 'AU-SA', 'Australia/Adelaide'),
    ('01a05c4b-c927-7691-a050-06c1c99e402a', 'AU-VIC', 'Australia/Melbourne'),
    ('01a05c4b-ca49-746d-b978-f3e076702b1b', 'AU-NSW', 'Australia/Sydney'),
    ('01a05c4b-cb34-79c4-adab-875eebd4390a', 'AU-QLD', 'Australia/Brisbane'),
    ('01a05c4d-a551-79c9-a5bc-dbd4b2fa8589', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4d-a645-7a04-989a-b188038bc7ec', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4d-a71d-7e98-9c05-fcc355026f55', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4d-a807-7226-89e2-57212b4c4335', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4e-b611-7ced-9318-0168d7246771', 'US-AL', 'America/Chicago'),
    ('01a05c4e-b6f9-7a16-9f93-7effcacee5c6', 'US-OH', 'America/New_York'),
    ('01a05c4e-b7cc-7ad5-a479-7d5fd7830a76', 'US-GA', 'America/New_York'),
    ('01a05c4e-b89a-7632-9878-a2efa2988fa9', 'US-FL', 'America/New_York'),
    ('01a05c4e-b9d3-749d-a3d6-b98602d59509', 'US-FL', 'America/New_York'),
    ('01a05c4e-ba93-7d5f-a782-c575042856dc', 'US-TN', 'America/Chicago'),
    ('01a05c4e-bb64-738c-ba7a-70e91720531a', 'US-LA', 'America/Chicago'),
    ('01a05c4e-bc3a-7934-8576-3462305edd89', 'US-TX', 'America/Chicago'),
    ('01a05c4e-bd14-7764-982d-95517843145f', 'US-TX', 'America/Chicago'),
    ('01a05c4e-be08-758d-a239-128feb8768d6', 'US-TX', 'America/Chicago'),
    ('01a05c4e-beea-7555-9f83-b3c7fa767fba', 'US-AZ', 'America/Phoenix'),
    ('01a05c4e-bff8-7e37-aa79-c1f393ba659d', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4e-c1e1-781b-a1b3-0c82b40fe669', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4e-c2c3-732e-8ce8-2cf99c3356f8', 'US-CA', 'America/Los_Angeles'),
    ('01a05c4e-c3a4-7294-8924-93b9605965eb', 'CA-BC', 'America/Vancouver'),
    ('01a05c4e-c4cf-70a4-bcb0-301f1d9d38d4', 'US-WA', 'America/Los_Angeles'),
    ('01a05c4e-c596-72e0-a366-d4f8fe11f7d1', 'US-OR', 'America/Los_Angeles'),
    ('01a05c4e-c66d-7e96-a1dd-1a06deaf03dc', 'US-UT', 'America/Denver'),
    ('01a05c4e-c74e-7a6d-bf4b-2d65c8621d94', 'US-CO', 'America/Denver'),
    ('01a05c4e-c851-76fc-b407-3b4184c85bf2', 'US-IA', 'America/Chicago'),
    ('01a05c4e-c96a-7443-aad0-3de274ba6762', 'US-WI', 'America/Chicago'),
    ('01a05c4e-ca37-741c-b389-427ff5d9b2fd', 'US-MN', 'America/Chicago'),
    ('01a05c4e-caf8-73c8-9a11-c6bb9de59cbd', 'US-MO', 'America/Chicago'),
    ('01a05c4e-cbee-79b9-8777-f696adc5243d', 'US-AR', 'America/Chicago'),
    ('01a05c4e-ccbc-7576-97d0-d47cf8525357', 'US-KY', 'America/Kentucky/Louisville'),
    ('01a05c4e-cd86-7a0f-aa2d-013ff7af9a2c', 'US-PA', 'America/New_York'),
    ('01a05c51-1f98-773b-b8ae-155fc782893a', 'US-MA', 'America/New_York'),
    ('01a05c51-21bb-754f-bd38-3ca1ce33f050', 'US-IL', 'America/Chicago'),
    ('01a05c52-3480-72fa-998f-e10b7bd6fcc4', 'KR-11', 'Asia/Seoul'),
    ('01a05c53-6200-7110-b174-34518be17529', 'US-CA', 'America/Los_Angeles'),
    ('01a05c53-6323-7de3-9a4d-f33207eba4bb', 'US-TN', 'America/Chicago'),
    ('01a05c55-65d1-7710-9bc3-71d5f78f79a5', 'GB-ENG', 'Europe/London'),
    ('01a05c55-6687-7051-8dc5-5667dafd078b', 'GB-ENG', 'Europe/London'),
    ('01a05c58-285c-7ed2-a59a-eb3529d52e5c', 'US-CA', 'America/Los_Angeles'),
    ('01a05c58-294c-7539-bb8b-aacabae5da33', 'US-CA', 'America/Los_Angeles'),
    ('01a05c58-2a34-7fe1-9db2-8823aa5710cc', 'US-OR', 'America/Los_Angeles'),
    ('01a05c58-2b1c-7ce2-b320-38aa0bcbca50', 'CA-BC', 'America/Vancouver'),
    ('01a05c58-2cbe-7967-a3f1-6e2027988634', 'US-UT', 'America/Denver'),
    ('01a05c58-2dc9-7597-8861-6bf62db457da', 'US-CO', 'America/Denver'),
    ('01a05c58-2ea7-791c-85d8-6bf599795029', 'US-MN', 'America/Chicago'),
    ('01a05c58-2f8c-76cb-a384-535cd73f274a', 'US-IL', 'America/Chicago'),
    ('01a05c58-3068-72ee-9928-aa8f2adb90eb', 'US-MI', 'America/Detroit'),
    ('01a05c58-313e-718d-814b-0a79916acdec', 'CA-ON', 'America/Toronto'),
    ('01a05c58-323f-7898-abc1-af04af3a5c06', 'CA-QC', 'America/Toronto'),
    ('01a05c58-340a-73a7-bd6f-c1801a647e48', 'US-PA', 'America/New_York'),
    ('01a05c58-3654-75b0-a50a-ffa4b3e8b475', 'US-NC', 'America/New_York'),
    ('01a05c58-3744-7a47-9141-e7dcfe7b5f8e', 'US-NC', 'America/New_York'),
    ('01a05c58-380f-72cc-a9a1-36c87ed6239d', 'US-OH', 'America/New_York'),
    ('01a05c58-391b-7524-95aa-a1ba0391bfb0', 'US-WI', 'America/Chicago'),
    ('01a05c58-39e7-7096-88e3-49bf69758d87', 'US-TN', 'America/Chicago'),
    ('01a05c58-3acf-7725-acb1-3718189506bc', 'US-GA', 'America/New_York'),
    ('01a05c58-3c62-7a2d-8c3d-c62fb4dcd26b', 'US-FL', 'America/New_York'),
    ('01a05c58-3d38-7e35-9291-1c5381c0024e', 'US-FL', 'America/New_York'),
    ('01a05c58-3e36-7e5e-a421-61acd3d01c20', 'US-TX', 'America/Chicago'),
    ('01a05c58-3f2a-7485-a2e8-a7ed3785ef3e', 'US-TX', 'America/Chicago'),
    ('01a05c58-4011-7bf3-b399-bbf38bf9a09e', 'US-TX', 'America/Chicago'),
    ('01a05c58-419b-7604-9504-4679bb8640da', 'US-CA', 'America/Los_Angeles'),
    ('01a05c58-4261-7dd0-a2ed-e6452a9efce9', 'US-NV', 'America/Los_Angeles'),
    ('01a05c58-44f7-7b48-b295-f9647a40ba34', 'JP-26', 'Asia/Tokyo'),
    ('01a05c58-46c0-70e4-81c2-fd1d607d1f83', 'JP-23', 'Asia/Tokyo'),
    ('01a05c58-4791-76fb-a0b0-8480abae029d', 'IE-L', 'Europe/Dublin'),
    ('01a05c58-485d-7ccb-8a85-4e494a0046f1', 'GB-ENG', 'Europe/London'),
    ('01a05c58-495b-799e-9dac-9388d004a041', 'GB-ENG', 'Europe/London'),
    ('01a05c58-4a37-780d-9d1b-6cba36dd6eb3', 'GB-ENG', 'Europe/London'),
    ('01a05c58-4b13-709a-88c0-a0c2d803b856', 'GB-SCT', 'Europe/London'),
    ('01a05c58-4bd0-758b-8121-2318b0d571ac', 'GB-ENG', 'Europe/London'),
    ('01a05c58-4ca2-7688-9967-5533b1be4a15', 'GB-ENG', 'Europe/London'),
    ('01a05c58-4db5-77db-8483-87c6c0ea5855', 'GB-WLS', 'Europe/London'),
    ('01a05c58-4ea9-7d51-9aed-ffba0daf3428', 'FR-IDF', 'Europe/Paris'),
    ('01a05c58-4f56-70a7-a2f3-48ad1709c878', 'NL-NH', 'Europe/Amsterdam'),
    ('01a05c58-5059-7258-8e1a-59160b92f541', 'NO-03', 'Europe/Oslo'),
    ('01a05c58-511d-7479-8023-1920d46436d1', 'SE-AB', 'Europe/Stockholm'),
    ('01a05c58-51db-7df4-aeb4-e5190beb558b', 'FI-18', 'Europe/Helsinki'),
    ('01a05c59-f853-72ec-b8a3-464b65508340', 'US-GA', 'America/New_York'),
    ('01a05c5b-287a-7571-9cba-628d0571228b', 'US-NV', 'America/Los_Angeles'),
    ('01a05c5c-5c5b-7f4a-a2a4-fd0d6333cd46', 'US-CA', 'America/Los_Angeles'),
    ('01a05c5c-5d5d-780e-a4da-1337f3197192', 'US-CA', 'America/Los_Angeles'),
    ('01a05c5c-5e58-7ad3-9acb-ae9079ea386b', 'US-OR', 'America/Los_Angeles'),
    ('01a05c5c-5f32-77e1-9589-28c2f0428f49', 'CA-BC', 'America/Vancouver'),
    ('01a05c5c-600e-7df8-a1d1-268177a1c37e', 'US-WA', 'America/Los_Angeles'),
    ('01a05c5c-60e6-79fa-bb75-58b0b298c6d8', 'US-UT', 'America/Denver'),
    ('01a05c5c-61c6-79de-b6db-ff7bad1e3d3e', 'US-MN', 'America/Chicago'),
    ('01a05c5c-62b8-7f43-9b11-b1a8900b5caa', 'US-IL', 'America/Chicago'),
    ('01a05c5c-638c-74e7-a0db-4ef9641e2f63', 'CA-ON', 'America/Toronto'),
    ('01a05c5c-6461-7c2a-96c8-1e002bb0dd4f', 'CA-QC', 'America/Toronto'),
    ('01a05c5c-655b-7141-9558-dfa751cd9687', 'US-MA', 'America/New_York'),
    ('01a05c5c-669b-7c22-8695-9cc4cde5ae6f', 'US-PA', 'America/New_York'),
    ('01a05c5c-6754-74be-b868-054af4cfc615', 'US-CT', 'America/New_York'),
    ('01a05c5c-6829-750b-9ca5-1f17a5ee94bb', 'US-NY', 'America/New_York'),
    ('01a05c5c-68fd-7a53-9093-2672880b04bf', 'US-VA', 'America/New_York'),
    ('01a05c5c-69f9-7c02-82ce-6c422a914e97', 'US-NC', 'America/New_York'),
    ('01a05c5c-6ade-7aff-ae5d-aef50bbecc74', 'US-GA', 'America/New_York'),
    ('01a05c5c-6ba7-7546-b0b0-15b246f82a7a', 'US-TN', 'America/Chicago'),
    ('01a05c5c-6c94-7394-9e44-412650e79b7c', 'US-TX', 'America/Chicago'),
    ('01a05c5c-6d6a-76a3-8582-9d9bd76264d7', 'DE-BE', 'Europe/Berlin'),
    ('01a05c5c-6e60-764d-b1a2-8fa0cc3c7252', 'DE-NW', 'Europe/Berlin'),
    ('01a05c5c-6f1d-715f-a687-3a379f34e3ac', 'NL-NH', 'Europe/Amsterdam'),
    ('01a05c5c-6fd8-7fea-b069-05efd25741db', 'BE-BRU', 'Europe/Brussels'),
    ('01a05c5c-7110-766f-a678-d68ab202749a', 'GB-ENG', 'Europe/London'),
    ('01a05c5c-722e-7213-a3f5-fdd6e4b12e03', 'GB-SCT', 'Europe/London'),
    ('01a05c5c-72ee-78d2-a24e-2822fd21d93a', 'GB-ENG', 'Europe/London'),
    ('01a05c5c-740d-71ef-95e2-d5f537957366', 'IE-L', 'Europe/Dublin'),
    ('01a05c5e-d51e-7e90-85f0-87d01ce76c17', 'MX-NLE', 'America/Monterrey'),
    ('01a05c5e-d627-7cdc-b572-8ebf334cf73e', 'MX-CMX', 'America/Mexico_City'),
    ('01a05c5e-d708-7d15-9526-a084edb668b5', 'MX-CMX', 'America/Mexico_City'),
    ('01a05c5e-d7ce-7473-9349-bfa18e3db7ad', 'CO-DC', 'America/Bogota'),
    ('01a05c5e-d882-77f1-aaca-4108f9abec52', 'BR-SP', 'America/Sao_Paulo'),
    ('01a05c5e-d945-7d9d-acbc-9a8859e588ec', 'BR-RJ', 'America/Sao_Paulo'),
    ('01a05c5e-da21-7cb5-9ae4-51e48a5d506e', 'AR-C', 'America/Argentina/Buenos_Aires'),
    ('01a05c5e-dafc-7ea8-aa5e-6d7397ac8d9f', 'CL-RM', 'America/Santiago'),
    ('01a05c5e-dbc7-7d70-bbdf-38a76bafc149', 'CL-RM', 'America/Santiago'),
    ('01a05c5e-dc89-77b9-9bfa-3afd5fced828', 'PE-LMA', 'America/Lima'),
    ('01a05c5f-ea16-7266-9e44-95958c8a84be', 'US-TN', 'America/New_York'),
    ('01a05c5f-eaeb-7653-b77f-9dfd25aef2fb', 'US-FL', 'America/New_York'),
    ('01a05c5f-ebc0-76b3-8370-c86ba4255d87', 'US-MD', 'America/New_York'),
    ('01a05c5f-ec9e-7151-a4a7-f0ce18e064e3', 'US-KY', 'America/Kentucky/Louisville'),
    ('01a05c5f-ed97-736d-8c55-23e19306b00a', 'US-TX', 'America/Chicago'),
    ('01a05c64-3bfa-7ca6-800c-ab48275e248f', 'US-CA', 'America/Los_Angeles'),
    ('01a05c64-3d9f-7e96-839c-8d58b9dd1aae', 'US-CA', 'America/Los_Angeles'),
    ('01a05c6c-93b0-7916-a4eb-246ddcf1e23a', 'US-NY', 'America/New_York'),
    ('01a05c6c-94d0-768a-b9d4-5b39376c5f0b', 'CA-QC', 'America/Toronto'),
    ('01a05c6c-95c9-72d7-9256-e0c9491b39f2', 'US-IL', 'America/Chicago'),
    ('01a05c6c-978b-7747-900a-59a9c09e4ec9', 'US-CO', 'America/Denver'),
    ('01a05c6c-985a-71de-910e-c58735c0b0c4', 'US-AZ', 'America/Phoenix'),
    ('01a05c6c-9942-782f-b355-0fa8cf5126f6', 'US-TX', 'America/Chicago'),
    ('01a05c6c-9a00-7e8f-ae4f-f6b1c26b34d1', 'US-TX', 'America/Chicago'),
    ('01a05c6c-9ace-73d0-8c25-c153b1145be6', 'US-CA', 'America/Los_Angeles'),
    ('01a05c6c-9be4-753e-812b-7f69d8ed39e5', 'US-WA', 'America/Los_Angeles'),
    ('01a05c6c-9cd0-7f2a-9376-167fb0d359ee', 'CA-ON', 'America/Toronto'),
    ('01a05c6c-9da4-7959-b736-6e38ab23530e', 'US-NV', 'America/Los_Angeles'),
    ('01a05c6e-4e1d-7cfe-9dae-3d1bd500a549', 'US-OH', 'America/New_York'),
    ('01a05c7e-e867-7057-8857-99708accc05d', 'US-IL', 'America/Chicago'),
    ('01a05c7e-e9c5-7dc8-abbb-92d27e647918', 'US-MN', 'America/Chicago'),
    ('01a05c7e-eadc-7b83-8921-f93846a9abce', 'US-MO', 'America/Chicago'),
    ('01a05c7e-ecbc-7cc3-87d5-2ea93f2ae25f', 'US-LA', 'America/Chicago'),
    ('01a05c7e-ed9c-77ae-9bf1-18c0a008d5a0', 'US-TN', 'America/Chicago'),
    ('01a05c7e-ee99-7c64-b5e8-255e6da23d58', 'US-GA', 'America/New_York'),
    ('01a05c7e-efbc-7112-b3f4-9db5682b67a3', 'US-NJ', 'America/New_York'),
    ('01a05c80-9a7e-77c7-a51b-adef0eeccca9', 'US-IN', 'America/Indiana/Indianapolis'),
    ('01a05c80-a8ca-7a5a-886d-38ae7bec38a1', 'US-UT', 'America/Denver'),
    ('01a05c80-ac2b-77e0-9362-3351b5ac3ac7', 'US-CA', 'America/Los_Angeles'),
    ('01a05c80-afed-767c-a17a-5c02eb04aa6d', 'GB-ENG', 'Europe/London'),
    ('01a05c80-b204-7fd6-96df-838b3d78900d', 'FR-IDF', 'Europe/Paris'),
    ('01a05c80-b2df-7584-88e7-e7b58f770735', 'BE-BRU', 'Europe/Brussels'),
    ('01a05c80-b473-7282-a5fd-b8e79a23df25', 'DE-NW', 'Europe/Berlin'),
    ('01a05c80-b567-78cf-933a-23c6c8a5d787', 'DE-BE', 'Europe/Berlin'),
    ('01a05c80-b70e-7150-aac0-845d6c204b18', 'SE-AB', 'Europe/Stockholm'),
    ('01a05c81-8dee-737e-b911-5d029395ee38', 'US-CA', 'America/Los_Angeles'),
    ('01a05c84-d27d-7a40-892e-427576dfa111', 'AU-VIC', 'Australia/Melbourne'),
    ('01a05c84-d397-71ef-a9df-8938efbfadf9', 'AU-NSW', 'Australia/Sydney'),
    ('01a05c84-e0c0-7025-8883-810ff40021e1', 'JP-36', 'Asia/Tokyo'),
    ('01a05c86-7b2d-7b9d-a1b9-da256a355795', 'US-CA', 'America/Los_Angeles'),
    ('01a05c86-7c7e-7757-bb41-7ceaedf2fc23', 'US-CA', 'America/Los_Angeles'),
    ('01a05c8a-7f05-7594-9de5-e9c408006f13', 'US-CA', 'America/Los_Angeles'),
    ('01a05c8b-1660-7e11-847b-2c7a611e93c8', 'US-CA', 'America/Los_Angeles'),
    ('01a05c8b-1b92-7cd2-bb63-a128999e10eb', 'US-NV', 'America/Los_Angeles'),
    ('01a05c8b-ae71-7177-b3ee-6699f98277cd', 'US-CA', 'America/Los_Angeles'),
    ('01a06bac-18f6-71d5-a1c2-6b6f5374ac54', 'TW-TPE', 'Asia/Taipei'),
    ('01a070d2-5407-7dfc-8186-f5d700e10498', 'US-PA', 'America/New_York'),
    ('01a070d2-5626-7fdf-b6dc-baf5f5bd6add', 'US-NY', 'America/New_York'),
    ('01a10f24-d183-75a3-bb0f-d41ee3f3f6a1', 'CA-BC', 'America/Vancouver'),
    ('01a10f2c-a44f-711a-94d1-56b95428874a', 'SG-04', 'Asia/Singapore'),
    ('01a10f3f-00c3-737a-94ad-75dccd7c1efe', 'US-FL', 'America/New_York'),
    ('01a11b3c-9524-720a-8874-31e88825840e', 'US-TN', 'America/Chicago'),
    ('01a11b3c-9891-79c2-898a-ea478f9e801b', 'US-FL', 'America/New_York'),
    ('01a11b3c-9d62-7971-b03f-2cb91c042c44', 'US-NE', 'America/Chicago'),
    ('01a11b3c-a077-72b1-9a9d-fd76d76269f7', 'US-KY', 'America/Kentucky/Louisville'),
    ('01a11b3c-a9a0-7877-a3f4-00abffcc5a42', 'US-TN', 'America/New_York'),
    ('01a11b3c-b16b-71f6-9071-b1b9b745243b', 'US-ID', 'America/Boise'),
    ('01a11b3c-cba7-7833-b515-c1271b880ee8', 'US-IN', 'America/Indiana/Indianapolis'),
    ('01a11b3c-d2ce-7a1f-af71-efca43e5f7e9', 'CA-ON', 'America/Toronto'),
    ('01a11b3c-d3c0-7765-9f58-932f32a1103d', 'US-MI', 'America/Detroit'),
    ('01a11b3d-1623-75f4-8f44-ec50f4fc280d', 'SI-061', 'Europe/Ljubljana'),
    ('01a11b3e-4530-7939-92d2-0fbd2ae831d3', 'US-FL', 'America/New_York'),
    ('01a11b3e-4ef4-71a7-9575-7e5e20c5f78d', 'US-IN', 'America/Indiana/Indianapolis'),
    ('01a11b3e-5469-72f7-879f-336e371dfeee', 'AU-WA', 'Australia/Perth'),
    ('01a11b3e-56c8-7604-b1d1-4e4b643ac5f2', 'AU-QLD', 'Australia/Brisbane'),
    ('01a11b3e-5808-72e8-95f6-512a0ab184b0', 'AU-NSW', 'Australia/Sydney'),
    ('01a1347e-f2a7-75db-8d35-59c9dc783e30', 'CA-ON', 'America/Toronto'),
    ('01a1347e-f614-794c-9006-99e6c452fb71', 'CA-ON', 'America/Toronto'),
    ('01a1347e-f993-7361-810b-f5852bb13801', 'US-TN', 'America/Chicago'),
    ('01a14e3d-daa0-77da-8fe1-1ad615ac0241', 'US-TX', 'America/Chicago'),
    ('01a15364-9f67-70a7-a514-5b8582020c94', 'SG-01', 'Asia/Singapore'),
    ('01a1589b-ce6d-7f23-875a-c1f7067dd17c', 'AU-NSW', 'Australia/Sydney'),
    ('01a1589b-d1b1-760b-9f72-dc71bb81b768', 'AU-WA', 'Australia/Perth'),
    ('01a19676-1323-7d1a-8dff-645efa85e094', 'US-TX', 'America/Chicago'),
    ('01a19678-7146-74a4-997b-8dda6e80ce93', 'US-FL', 'America/New_York'),
    ('01a1de79-5864-71c6-bf5b-d8a88c31e187', 'US-NE', 'America/Chicago'),
    ('01a1de79-5a41-7bcc-b419-383850742f3a', 'US-MI', 'America/Detroit'),
    ('01a1de79-61f5-7744-854d-c94d6f9f2413', 'CA-QC', 'America/Toronto'),
    ('01a1de79-6336-759f-994e-ba9912717b2a', 'CA-ON', 'America/Toronto'),
    ('01a1de79-64f1-7e1b-b18d-486c65766e94', 'US-FL', 'America/New_York'),
    ('01a1de79-66fb-73d8-b2e8-a80267d09ee5', 'US-TX', 'America/Chicago'),
    ('01a1e2f1-28c1-7d24-ab2b-838d21427f18', 'US-TN', 'America/New_York');

-- Discovery may have stored a Venue under the same listed name and the
-- reviewed area since the review: it is the same Venue, so the Events move to
-- it and the old row goes. A slot both rows hold leaves the old row, and the
-- update below then fails on the listed name and area index.
UPDATE events AS e SET venue_id = d.id
FROM venue_review AS r
JOIN venues AS v ON v.id = r.id
JOIN venues AS d ON d.listed_venue_name = v.listed_venue_name AND d.admin_area = r.admin_area AND d.id <> v.id
WHERE e.venue_id = v.id
  AND NOT EXISTS (SELECT 1 FROM events f WHERE f.venue_id = d.id AND f.local_event_date = e.local_event_date AND f.start_time IS NOT DISTINCT FROM e.start_time);

DELETE FROM venues AS v USING venue_review AS r, venues AS d
WHERE v.id = r.id AND d.listed_venue_name = v.listed_venue_name AND d.admin_area = r.admin_area AND d.id <> v.id
  AND NOT EXISTS (SELECT 1 FROM events e WHERE e.venue_id = v.id);

UPDATE venues AS v SET admin_area = r.admin_area, time_zone = r.time_zone
FROM venue_review AS r
WHERE v.id = r.id;

DROP TABLE venue_review;
