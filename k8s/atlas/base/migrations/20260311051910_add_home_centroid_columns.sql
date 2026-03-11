-- Modify "homes" table
ALTER TABLE "homes" ADD COLUMN "centroid_latitude" double precision NULL, ADD COLUMN "centroid_longitude" double precision NULL;
-- Set comment to table: "homes"
COMMENT ON TABLE "homes" IS 'Structured geographic home area for users. Determines proximity classification (home/nearby/away).';
-- Set comment to column: "centroid_latitude" on table: "homes"
COMMENT ON COLUMN "homes"."centroid_latitude" IS 'Approximate latitude of the home area centroid, resolved at write time from level_1. Used for proximity calculations.';
-- Set comment to column: "centroid_longitude" on table: "homes"
COMMENT ON COLUMN "homes"."centroid_longitude" IS 'Approximate longitude of the home area centroid, resolved at write time from level_1. Used for proximity calculations.';
-- Backfill centroid coordinates for existing Japanese prefecture rows
UPDATE "homes" SET centroid_latitude = c.lat, centroid_longitude = c.lng
FROM (VALUES
  ('JP-01', 43.0642, 141.3469), ('JP-02', 40.8244, 140.7400), ('JP-03', 39.7036, 141.1527),
  ('JP-04', 38.2688, 140.8721), ('JP-05', 39.7186, 140.1024), ('JP-06', 38.2404, 140.3633),
  ('JP-07', 37.7500, 140.4678), ('JP-08', 36.3419, 140.4468), ('JP-09', 36.5658, 139.8836),
  ('JP-10', 36.3911, 139.0608), ('JP-11', 35.8569, 139.6489), ('JP-12', 35.6047, 140.1233),
  ('JP-13', 35.6894, 139.6917), ('JP-14', 35.4478, 139.6425), ('JP-15', 37.9026, 139.0236),
  ('JP-16', 36.6953, 137.2114), ('JP-17', 36.5947, 136.6256), ('JP-18', 36.0652, 136.2219),
  ('JP-19', 35.6642, 138.5683), ('JP-20', 36.2333, 138.1811), ('JP-21', 35.3912, 136.7222),
  ('JP-22', 34.9769, 138.3831), ('JP-23', 35.1802, 136.9066), ('JP-24', 34.7303, 136.5086),
  ('JP-25', 35.0045, 135.8686), ('JP-26', 35.0214, 135.7556), ('JP-27', 34.6863, 135.5200),
  ('JP-28', 34.6913, 135.1830), ('JP-29', 34.6853, 135.8328), ('JP-30', 34.2260, 135.1675),
  ('JP-31', 35.5039, 134.2378), ('JP-32', 35.4723, 133.0505), ('JP-33', 34.6618, 133.9344),
  ('JP-34', 34.3966, 132.4596), ('JP-35', 34.1861, 131.4714), ('JP-36', 34.0658, 134.5593),
  ('JP-37', 34.3401, 134.0434), ('JP-38', 33.8416, 132.7661), ('JP-39', 33.5597, 133.5311),
  ('JP-40', 33.6064, 130.4183), ('JP-41', 33.2494, 130.2988), ('JP-42', 32.7448, 129.8737),
  ('JP-43', 32.7898, 130.7417), ('JP-44', 33.2382, 131.6126), ('JP-45', 31.9111, 131.4239),
  ('JP-46', 31.5602, 130.5581), ('JP-47', 26.3358, 127.8011)
) AS c(code, lat, lng)
WHERE "homes".level_1 = c.code;
