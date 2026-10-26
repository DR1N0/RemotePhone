-- Soft-delete user accounts so audit foreign keys and owned instances remain
-- valid. A removed account cannot log in and its username is released, so the
-- owner can issue a fresh access code for a new account with the same name.
ALTER TABLE users ADD COLUMN deleted_at TEXT;
