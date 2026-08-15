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
COMMENT ON TABLE "organizers" IS 'Vetted sellers (label / agency / promoter / self-publishing artist) that represent artists';
COMMENT ON COLUMN "organizers"."id" IS 'Unique organizer identifier (UUIDv7, application-generated)';
COMMENT ON COLUMN "organizers"."name" IS 'Organizer display name (label / agency / promoter / self-publishing artist)';
COMMENT ON COLUMN "organizers"."operator_email" IS 'Email of the operator who administers this organizer; seeded as the initial Zitadel owner user';
COMMENT ON COLUMN "organizers"."zitadel_org_id" IS 'Zitadel tenant organization ID; NULL until provisioning completes';
COMMENT ON COLUMN "organizers"."status" IS 'Lifecycle state: 1=provisioning, 2=active, 3=deactivated';
-- Create index "uq_organizers_zitadel_org_id" to table: "organizers"
CREATE UNIQUE INDEX "uq_organizers_zitadel_org_id" ON "organizers" ("zitadel_org_id") WHERE (zitadel_org_id IS NOT NULL);
COMMENT ON INDEX "uq_organizers_zitadel_org_id" IS 'One Zitadel tenant org maps to at most one Organizer; NULL while provisioning';
-- Create "organizer_artists" table
CREATE TABLE "organizer_artists" (
  "organizer_id" uuid NOT NULL,
  "artist_id" uuid NOT NULL,
  PRIMARY KEY ("organizer_id", "artist_id"),
  CONSTRAINT "organizer_artists_organizer_id_fkey" FOREIGN KEY ("organizer_id") REFERENCES "organizers" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "organizer_artists_artist_id_fkey" FOREIGN KEY ("artist_id") REFERENCES "artists" ("id") ON UPDATE NO ACTION ON DELETE CASCADE
);
COMMENT ON TABLE "organizer_artists" IS 'Links an organizer to the artists it represents';
COMMENT ON COLUMN "organizer_artists"."organizer_id" IS 'Organizer that represents the artist';
COMMENT ON COLUMN "organizer_artists"."artist_id" IS 'Artist represented by the organizer';
-- Enforce that each artist is represented by at most one organizer. A deactivated
-- organizer holds no association rows (deactivation deletes them), so this plain
-- unique index realizes the intended "at most one non-deactivated organizer per
-- artist" and frees the artists for re-association.
CREATE UNIQUE INDEX "uq_organizer_artists_artist_id" ON "organizer_artists" ("artist_id");
COMMENT ON INDEX "uq_organizer_artists_artist_id" IS 'Each artist is represented by at most one organizer';
-- Create index "idx_organizer_artists_organizer_id" to table: "organizer_artists"
CREATE INDEX "idx_organizer_artists_organizer_id" ON "organizer_artists" ("organizer_id");
COMMENT ON INDEX "idx_organizer_artists_organizer_id" IS 'Optimizes listing the artists an organizer represents';
