-- Set comment to table: "deleted_user_identities"
COMMENT ON TABLE "deleted_user_identities" IS 'Identity-provider IDs of removed users. A recorded ID gets no new user until it is forgotten, 90 seconds after the identity provider removed the identity, when no token of it is accepted any more.';
