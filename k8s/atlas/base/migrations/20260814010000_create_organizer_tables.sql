-- Create "organizers" table
CREATE TABLE "organizers" (
  "id" uuid NOT NULL,
  "name" text NOT NULL,
  "operator_email" text NOT NULL,
  "zitadel_org_id" text NULL,
  "status" smallint NOT NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "chk_organizers_id_uuidv7" CHECK ("substring"((id)::text, 15, 1) = '7'),
  CONSTRAINT "chk_organizers_name_non_empty" CHECK (name <> ''),
  CONSTRAINT "chk_organizers_operator_email_non_empty" CHECK (operator_email <> ''),
  CONSTRAINT "chk_organizers_status" CHECK (status BETWEEN 1 AND 3)
);
-- Create index "uq_organizers_zitadel_org_id" to table: "organizers"
CREATE UNIQUE INDEX "uq_organizers_zitadel_org_id" ON "organizers" ("zitadel_org_id") WHERE (zitadel_org_id IS NOT NULL);
-- Create "organizer_artists" table
CREATE TABLE "organizer_artists" (
  "organizer_id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  PRIMARY KEY ("organizer_id", "artist_id"),
  CONSTRAINT "organizer_artists_organizer_id_fkey" FOREIGN KEY ("organizer_id") REFERENCES "organizers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "organizer_artists_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
-- Enforce that each artist is represented by at most one organizer. A deactivated
-- organizer holds no association rows (deactivation deletes them), so this plain
-- unique index realizes the intended "at most one non-deactivated organizer per
-- artist" and frees the artists for re-association.
CREATE UNIQUE INDEX "uq_organizer_artists_artist_id" ON "organizer_artists" ("artist_id");
-- Create index "idx_organizer_artists_organizer_id" to table: "organizer_artists"
CREATE INDEX "idx_organizer_artists_organizer_id" ON "organizer_artists" ("organizer_id");
