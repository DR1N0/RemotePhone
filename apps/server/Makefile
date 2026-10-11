MIGRATIONS_DIR := internal/controlplane/persistence/migrations
SQLC_SCHEMA_DIR := internal/controlplane/persistence/sqlc_schema

.PHONY: sqlc-schema sqlc-generate

# sqlc-schema regenerates sqlc_schema/ (gitignored) from migrations/.
#
# sqlc's SQLite schema parser doesn't unquote backtick-quoted identifiers
# before catalog lookup (a bug in sqlc's engine/sqlite package), so any
# migration using backtick quoting breaks `sqlc generate`. This target
# mirrors every migration file into sqlc_schema/ with backticks rewritten
# as double quotes — semantically identical SQLite identifier quoting that
# sqlc's parser handles correctly. migrations/ itself (what ApplyMigrations
# actually runs at runtime) is never touched.
sqlc-schema:
	rm -rf $(SQLC_SCHEMA_DIR)
	mkdir -p $(SQLC_SCHEMA_DIR)
	for f in $(MIGRATIONS_DIR)/*.sql; do \
		sed 's/`/"/g' "$$f" > "$(SQLC_SCHEMA_DIR)/$$(basename "$$f")"; \
	done

# sqlc-generate always regenerates sqlc_schema/ first so it can never go
# stale relative to migrations/, then runs sqlc generate.
sqlc-generate: sqlc-schema
	sqlc generate
