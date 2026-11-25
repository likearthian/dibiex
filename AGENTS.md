# dibiex

dibiex is a native desktop SQL client built with Go and MyGo. It shares the UI
engine and visual direction of the sibling GoRex project. Keep the native UI
unless the user explicitly chooses a different rendering approach.

## Structure

Desktop source paths below are relative to `internal/app/` unless they start with
`internal/` or `assets/`, or name the root `main.go`.

- `main.go`: native entry point and embedded MyGo manifest.
- `internal/app/app.go`: lifecycle, UI state, background work orchestration.
- `view.go`: DataGrip-inspired native explorer, query, and result views.
- `editor_completion.go`: bounded native SQL autocomplete and targeted asynchronous metadata loading.
- `editor_inference.go`: scoped aliases, CTE/derived-table projection inference and conservative bounded column resolution.
- `editor_signatures.go`: curated dialect function signatures and the native active-argument helper.
- `alter_table.go`: native PostgreSQL column editing and table-edit navigation, backed by transactional `internal/database/alter_postgres.go`.
- `edit_table_designer.go`, `table_designer_indexes.go`: shared create/edit designer, live drafts, ALTER previews and index controls. `internal/database/table_design.go` loads bounded snapshots and `table_design_changes.go` applies only explicit differences.
- `files.go`: native query tabs and the session-only SQL console Files pane.
- `saved_queries.go`: persistent query library, query naming, and bounded asynchronous SQL file imports.
- `workspace_tabs.go`: combined tab navigation with explicit owning connection and safe cross-connection closing.
- `inspector_objects.go`: sequence and database/server object selection with native metadata in Inspector.
- `inspector_columns.go`: column selection and refreshed metadata in Inspector.
- `inspector_table_objects.go`: selectable table objects and their details in Inspector.
- `inspector_style.go`: shared Inspector headings, property fields and SQL blocks.
- `right_sidebar.go`: Files/Inspector switching in docked panes and narrow drawers.
- `breadcrumb.go`: selected object paths and horizontally scrolling footer breadcrumbs.
- `tree_controls.go`: separate chevron disclosure controls; row bodies select without expanding.
- `explorer.go`: schema/dataset tree, collapse state, and asynchronous dataset loading.
- `explorer_objects.go`: PostgreSQL database/server groups and asynchronous extended catalog discovery.
- `table_tree.go`: table property branches, nested property rendering and asynchronous optional section loading.
- `table_rows.go`: compact key/index/trigger rows and SQL display annotations.
- `titlebar.go`: draggable header and GoRex-style window controls.
- `style.go`: light/dark workspace colors and shared controls.
- `connections.go`: native connection form and asynchronous connection switching.
- `create_table.go`, `create_table_rows.go`: native PostgreSQL table designer and asynchronous creation.
- `connection_workspace.go`: independent live connection workspaces and shared saved-connection coordination.
- `query_execution.go`: per-tab execution, cancellation, Services results, pagination and SQL-session lifetime.
- `result_tabs.go`: pinned in-memory result snapshots with independent grid state and Output-first navigation.
- `internal/database`: SQLite, PostgreSQL (pgx), MySQL (go-sql-driver) and BigQuery connections and query/result handling; no UI imports.
- `assets/fonts`: bundled Inter UI fonts, JetBrains Mono editor font and their licenses.

Keep UI state on the main thread. Capture query inputs before starting a worker;
apply background results through `Window.Update` or `mygo.RunOnMain`. Never block
UI callbacks with queries or file/network IO. Keep results bounded and pass
contexts into database operations. Preserve SQL NULL and BLOB presentation.
The PostgreSQL table designer uses internal/database.PostgreSQLTable for validated
DDL and its SQL preview. Apply creation, comments, security and ownership in one
transaction; reject an existing editor transaction instead of committing it.
Role/tablespace loading and creation stay off the UI thread. Refresh the catalog
after creation and keep errors in the dialog so the draft can be corrected.
Keep the reference-inspired charcoal inset panes, compact icon toolbars and
monochrome column datatype icons. Files lists open drafts and explicitly saved
queries grouped by connection. Saved queries persist in queries.json under the
user config directory; saving, renaming, deleting and SQL imports run on workers.
Closing a tab never removes its saved record. Render an empty editor when no tabs
remain; selecting database objects must not create consoles. Tab close controls
appear inside their own tabs only on hover. Keep query ownership on reopen and
never retarget saved drafts to another connection. Do not add placeholder capabilities.
PostgreSQL has a real database node beneath its Connection, plus native routines,
types, sequences, Database Objects and Server Objects. Keep their discovery
bounded and cached off the UI thread. Never invent selection ratios or counts.

SQLite uses modernc.org/sqlite (no cgo), PostgreSQL uses pgx, MySQL uses
go-sql-driver/mysql (no cgo), and BigQuery uses
Google's official Go client with ADC. Preserve engine-specific quoting, bounded
results, BigQuery billing caps, and best-effort remote cancellation.

MySQL uses backtick quoting (`QuoteIdentifierFor`), user databases as schemas, and
`information_schema` for catalog discovery, columns, constraints, indexes,
triggers, partitions and row estimates. Exact DDL comes from `SHOW CREATE TABLE`;
never synthesize a "complete" definition when it is unavailable. Text and JSON
cells arrive as bytes and are converted for display; BLOB/BINARY/GEOMETRY/BIT
stay size-tagged. PostgreSQL-only tools (alter dialogs, table designer, data
editing, cursors, ERD, pg_dump backup) remain unavailable for MySQL; generic
menus emit editable SQL instead.

Create new SQLite files only through the explicit New file action, off the UI
thread. Use exclusive creation and never replace an existing file. Normal SQLite
connections continue to require an existing file.
BigQuery's default dataset is optional; browse datasets at project scope and load
their objects on expansion. Keep all catalog IO off the UI thread.
Passwords may be kept in memory until restart, omitted entirely, or explicitly
saved forever using Windows DPAPI user-account encryption in credentials.json.
Never put plaintext passwords in connection settings, schema snapshots or logs.
Credential protection and file IO run on workers. Saved PostgreSQL and MySQL
connections without remembered credentials use the compact User/Password login
dialog; Modify connection opens the complete settings form. Disconnect/Reconnect target
only their owning workspace, and reconnect retains its SQL drafts.
Named connections save non-secret settings
in the OS user config directory; keep connection IO off the UI thread. Each
connection has one root row; merge the active tree under its matching saved entry.
Multiple connections can remain active at once. Each owns its query tabs, results,
catalog state, Inspector selection and worker contexts. Switching the selected
connection must never cancel another connection's work or retarget its callbacks.
Query tabs own their results and execution state. Run tabs concurrently, including
on the same connection, using lazily pinned SQL sessions (up to 16 per connection).
Keep one dedicated catalog connection. Stop/close cancel only their tab; disconnect
and shutdown cancel all tabs. Never return session-local settings or unfinished
transactions to another tab. BigQuery jobs retain per-tab billing caps and cancellation.
Services keeps Output first, then result tabs. Pinned results must survive later
executions; reuse only an unpinned selected result. Retain pagination and row
selection per result, bound history to 16 results per query tab, and never persist
query output rows to disk. Completing a query must not overwrite a pinned result
that the user selected while the query was running.
Show all connections' query tabs in one shared strip, with a visible connection
name cue. Files groups in-memory consoles under connection folders. Selecting a
tab or console activates its owner; use stable distinct UI keys across connections
even when console names and local tab IDs match. Pane visibility stays shared.
Share saved settings and persistence busy state across the workspaces; shutdown
must cancel and close every live client.
Deleting an active connection must cancel its workers, close its session off the
UI thread, clear its tree/results, and never drop the remote database or SQLite file.
Start disconnected; do not reintroduce a separate saved-profile layer or automatic
Demo workspace. Keep existing connections.json files compatible. Query tabs use
MyGo's native ui/editor with SQL highlighting and line numbers, not a text area or
webview. Autocomplete uses MyGo's native completion callbacks with bounded local
SQL analysis and connection-scoped metadata. Keep CTE/derived-table inference
bounded, preserve alias shadowing and UNION-branch isolation, and infer names
only from explicit projections or known metadata. Do not invent expression types,
table-function outputs or ambiguous columns. Function hints use a curated dialect
catalog, not server UDF discovery. Do not claim language-server diagnostics. Table metadata
loads asynchronously; retain engine quoting, metadata bounds and billing caps.
`internal/database.Introspector` owns versioned, connection-scoped schema snapshots
under the OS user cache directory. Never call its IO/locking methods on the UI
thread. Keep BigQuery table metadata on the REST table API, preserve nested fields,
partition specs and ordered clustering, and keep dataset object discovery lazy.
Keep table properties in separate Columns/Indexes/Partitions/Constraints/etc.
branches. BigQuery's optional index/partition INFORMATION_SCHEMA queries run only
on explicit expansion/refresh with billing caps; policies and table IAM use REST.
Never fetch catalog metadata during rendering. Preserve unsupported/unloaded/error
states, native provider attributes and secret redaction in schema-v2 snapshots.
Contextual Refresh bypasses cache at its scope. Preserve the last good snapshot
on remote errors and exclude credentials, query rows and session-only SQLite
attachments from persisted cache. Query result limits are separate from catalog
limits. DDL previews must remain labeled as previews.

PostgreSQL table tools live in `postgres_menu.go`, `table_actions.go`,
`table_data.go` and `table_erd.go`, backed by `internal/database/postgres_actions.go`
and `pg_tools.go`. Run table changes, maintenance, COPY and data editing in their
own cancellable query sessions; never reuse an editor transaction. Confirm drop,
truncate, restore, import and statistics reset in the native dialog. Row editing
requires a primary key for updates/deletes, checks original values, preserves
NULL and skips generated/identity columns. Keep 100-row server pagination and
explicit cell/transfer limits. Backup/restore and exact CREATE scripts use real
PostgreSQL CLI tools without a shell; passwords belong only in the child process
environment, never arguments or persisted options. Explain table-only restore's
dependent-object limitation. ERD uses bounded real foreign keys, native painting
and editable mxGraphModel export; do not invent relationships.
Table menus use `table_menu_ui.go` for native icon rows and submenus. Backup,
Restore and CREATE Script are temporarily disabled; keep CSV and other actions
available, since they use the live connection without PostgreSQL CLI tools.
PostgreSQL Edit table/column dialogs load live definitions off the UI thread,
preserve drafts on failures, and use separate cancellable sessions. Keep table
and column edits transactional, reject existing editor transactions, check the
original OID/column definition under a bounded lock, and refresh catalog and
metadata after success. Generated/identity columns support rename/comment edits
only here; do not silently change their generation settings or remove primary-key
nullability. Preserve parent table drafts when opening a column editor.
Edit table uses the shared table designer, not the smaller Properties form.
Keep its loaded snapshot immutable, generate an empty plan for untouched tables,
and preserve advanced/unsupported native attributes. Require explicit confirmation
for object removals, never add automatic CASCADE, and reject changed definitions
under the transaction lock. Existing partition layouts are visible but immutable;
constraint-owned indexes are managed through Constraints. Both create and edit
share the same native tabs and column/constraint/index controls.
The SQLite table designer is `create_sqlite_table.go`, backed by
`internal/database/create_sqlite_table.go`. It creates in the connection's main
database through a separate worker session and transaction. Keep type names and
identifiers quoted, validate STRICT/WITHOUT ROWID/AUTOINCREMENT combinations,
preserve drafts on errors, and refresh the catalog after success. Do not create
tables in session-only temp/attached databases through this designer.

## Validation

Run `gofmt` on changed Go files, `go test ./...`, `go vet ./...`, and
`go build -o build/dibiex.exe .` on Windows. Use `go tool mygo dev` to develop.
MyGo is pinned to v0.10.0 and requires Go 1.27.3; evaluate upgrades deliberately.

## graphify

This project has a graphify knowledge graph at graphify-out/.

Rules:
- Before answering architecture or codebase questions, read graphify-out/GRAPH_REPORT.md for god nodes and community structure
- If graphify-out/wiki/index.md exists, navigate it instead of reading raw files
- For cross-module "how does X relate to Y" questions, prefer `graphify query "<question>"`, `graphify path "<A>" "<B>"`, or `graphify explain "<concept>"` over grep — these traverse the graph's EXTRACTED + INFERRED edges instead of scanning files
- After modifying code files in this session, run `graphify update .` to keep the graph current (AST-only, no API cost)
