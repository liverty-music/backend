-- Enable uuid-ossp extension for UUID generation
CREATE EXTENSION IF NOT EXISTS "uuid-ossp";

-- Create "posts" table
CREATE TABLE "posts" (
  "id" uuid NOT NULL DEFAULT uuid_generate_v4(),
  "title" character varying(500) NOT NULL,
  "user_id" uuid NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);

-- Create "users" table
CREATE TABLE "users" (
  "id" uuid NOT NULL DEFAULT uuid_generate_v4(),
  "name" character varying(255) NOT NULL,
  "email" character varying(255) NOT NULL,
  "created_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_at" timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id"),
  CONSTRAINT "users_email_key" UNIQUE ("email")
);
