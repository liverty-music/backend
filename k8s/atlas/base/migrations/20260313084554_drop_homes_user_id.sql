-- Modify "homes" table
ALTER TABLE "homes" DROP COLUMN "user_id", ADD COLUMN "created_at" timestamptz NOT NULL DEFAULT now(), ADD COLUMN "updated_at" timestamptz NOT NULL DEFAULT now();
