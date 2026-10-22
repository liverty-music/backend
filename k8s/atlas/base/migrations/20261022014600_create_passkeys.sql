-- fan-passkey-recovery-and-management (design D7): passkeys, the platform's
-- record of each passkey added to a fan's identity, written by the event
-- consumer from the passkey-event notices. No grant is written: the
-- read-write roles' default privileges give the consumer and fan-api full
-- CRUD, the SELECT default privilege of organizer-console-api covers the new
-- table, and reception-api gets nothing.

SET search_path TO app, public;

-- Create "passkeys" table
CREATE TABLE "passkeys" (
  "provider_passkey_id" text NOT NULL,
  "user_id" uuid NOT NULL,
  "aaguid" uuid NOT NULL,
  "created_at" timestamptz NOT NULL,
  PRIMARY KEY ("provider_passkey_id"),
  CONSTRAINT "passkeys_user_id_fkey" FOREIGN KEY ("user_id") REFERENCES "users" ("id") ON UPDATE NO ACTION ON DELETE CASCADE,
  CONSTRAINT "chk_passkeys_provider_passkey_id_not_empty" CHECK (provider_passkey_id <> ''::text)
);
-- Create index "idx_passkeys_user_id" to table: "passkeys"
CREATE INDEX "idx_passkeys_user_id" ON "passkeys" ("user_id");
-- Set comment to table: "passkeys"
COMMENT ON TABLE "passkeys" IS 'The platform''s record of a passkey registered for a user at the identity provider (Zitadel), written from the identity provider''s passkey added and removed notices. The identity provider remains the authority on which passkeys exist; a passkey registered before records were kept, or whose notice was lost, has no row';
-- Set comment to column: "provider_passkey_id" on table: "passkeys"
COMMENT ON COLUMN "passkeys"."provider_passkey_id" IS 'The identity provider''s id for the passkey (Zitadel webAuthNTokenId)';
-- Set comment to column: "user_id" on table: "passkeys"
COMMENT ON COLUMN "passkeys"."user_id" IS 'The user the passkey signs in to';
-- Set comment to column: "aaguid" on table: "passkeys"
COMMENT ON COLUMN "passkeys"."aaguid" IS 'Identifier of the passkey provider model that made the passkey; all zeros when the provider does not reveal itself';
-- Set comment to column: "created_at" on table: "passkeys"
COMMENT ON COLUMN "passkeys"."created_at" IS 'When the passkey finished registration at the identity provider';
-- Set comment to index: "idx_passkeys_user_id" on table: "passkeys"
COMMENT ON INDEX "idx_passkeys_user_id" IS 'Supports listing a user''s passkeys and the users foreign key';
