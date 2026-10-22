-- Allow one user to own more than one managed instance.
--
-- The 0011 index enforced the original MVP model (one instance per owner,
-- one "seat" per account). The product model is now explicit multi-instance
-- per user: any number of instances can be created from a template and each
-- can be started/connected independently. Owner-scoped listing still needs
-- an index, so replace the unique one with a plain lookup index.
DROP INDEX IF EXISTS instances_unique_owner;
CREATE INDEX IF NOT EXISTS instances_owner_id ON instances(owner_id);
