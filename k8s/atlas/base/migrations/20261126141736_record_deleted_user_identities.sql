-- Create "deleted_user_identities" table
CREATE TABLE "deleted_user_identities" (
  "provider_user_id" text NOT NULL,
  PRIMARY KEY ("provider_user_id")
);
-- Set comment to table: "deleted_user_identities"
COMMENT ON TABLE "deleted_user_identities" IS 'Identity-provider IDs of removed users. A recorded ID never gets a new user: its identity can still sign in until the identity provider removes it.';
-- Set comment to column: "provider_user_id" on table: "deleted_user_identities"
COMMENT ON COLUMN "deleted_user_identities"."provider_user_id" IS 'The identity provider''s ID the removed user had';
