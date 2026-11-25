# dibiex

dibiex is a minimal desktop SQL client for SQLite, PostgreSQL, MySQL, and BigQuery. It gives you a place to browse schemas, run SQL, and work with the features of each database, from SQLite table options and PostgreSQL cursors to BigQuery partition metadata and query billing limits.

## Database support

| Engine | What you can do |
| --- | --- |
| **SQLite** | Open an existing database or create a new file. Inspect source DDL, indexes, triggers, constraints, and generated columns. Create tables in the main database with support for STRICT, WITHOUT ROWID, and AUTOINCREMENT. |
| **PostgreSQL** | Browse schemas, routines, sequences, roles, tablespaces, and other catalog objects. Create and edit tables and columns, edit table data, fetch query results in cursor batches, transfer CSV with COPY, run table maintenance, and diagram foreign-key relationships. |
| **MySQL** | Browse accessible user databases and inspect columns, constraints, indexes, triggers, and partitions. Read server-generated definitions with `SHOW CREATE TABLE` and open editable SQL for supported object actions. |
| **BigQuery** | Browse datasets across projects, inspect nested fields, partitioning, clustering, row-access policies, and table IAM, and run GoogleSQL with a configurable maximum bytes billed. |

Query plans are available through **Explain** for SQLite, PostgreSQL, and MySQL. PostgreSQL table definition previews are labeled as previews. PostgreSQL backup, restore, and exact CREATE script actions are currently disabled in the menu.

## Getting started

dibiex requires Go 1.27.3 or newer. The project pins MyGo v0.10.0.

From the repository root, start the app in development mode:

```powershell
go tool mygo dev
```

For a direct local run, use `go run .`. To build a Windows executable:

```powershell
go build -o build/dibiex.exe .
```

Use `go tool mygo build` for packaging, or `go tool mygo build -skip-installer` to produce a Windows executable without building an installer.

### Connect to a database

Choose **+** in Database Explorer, select an engine, and enter the connection details.

- **SQLite** — Use **Browse…** to select an existing file or **New file…** to create one. New file creation refuses to replace an existing database.
- **PostgreSQL** — Enter the host, port, database, user, and authentication details. The default SSL mode is `verify-full`.
- **MySQL** — Enter the host, port, and credentials. The explorer lists user databases visible to the account.
- **BigQuery** — Use Application Default Credentials (for example, `gcloud auth application-default login`) or select a service account JSON file. Set a billing project or let dibiex detect it from the credentials. The default dataset is optional. Use fully qualified names such as `project.dataset.table` when no default is set.

BigQuery uses a default cap of **1 GiB billed per query**. The row limit in the result grid is independent of bytes scanned. The authenticated identity needs access to the datasets and permission to create jobs in the billing project.

**Test** checks the connection. **Save connection** stores settings locally and excludes plaintext passwords. On Windows, you can explicitly save a PostgreSQL or MySQL password using user-account encryption. Multiple connections can stay active, and each keeps its own tabs, results, and catalog state.

## Working with SQL

- **Run a statement.** Press **Ctrl+Enter** (**Cmd+Enter** on macOS) to execute the selection or the statement at the caret. **Execute** collects returned rows, including rows from writes with `RETURNING`. **Execute writes** runs SQL without collecting returned rows. Either action can change data.
- **Use dialect-aware completion.** Suggestions include keywords, built-in functions, and available schema, table, and column names. Completion understands common aliases, CTEs, and derived tables. Curated function hints show the active argument.
- **Keep work in separate tabs.** SQLite, PostgreSQL, and MySQL tabs have independent sessions for transactions and temporary objects. Each BigQuery execution is a separate job, so keep its transaction statements in one script. Stop cancels the selected tab's work. BigQuery remote cancellation is best effort.
- **Save or import SQL.** Use **Save query** to keep a console in the local query library. **Open SQL file…** imports a UTF-8 SQL file as an editable draft.
- **Inspect and compare results.** The grid paginates loaded rows and supports local filtering, sorting, and copying. Pin a result to keep it across subsequent executions. Standard results retain up to 1,000 rows and 8 MiB of display text.
- **Export to a file.** **Export Data** writes the loaded snapshot or reruns its query in a separate read-only session to stream all rows. **Execute to File** runs the selected SQL directly into CSV, XLSX, JSON, or Parquet without first loading a grid. Full exports use a separate session, so editor transactions and temporary objects are unavailable there.

## Local data

Saved connections and queries live under `DBCraft/` in the operating system's user config directory. Windows uses `%APPDATA%\DBCraft`. Windows-encrypted passwords, when enabled, live in a separate `credentials.json`. For a BigQuery service account, dibiex stores the path to the key file.

Schema snapshots live under `DBCraft/schema-v2/` in the user cache directory. Use **Refresh** on a connection, schema, or object to fetch fresh metadata. Query results stay in memory unless you explicitly export them.

## Development

| Path | Responsibility |
| --- | --- |
| `main.go`, `mygo.json` | Application entry point and packaging configuration |
| `internal/app/` | Desktop workspace and background task orchestration |
| `internal/database/` | Engine clients, query sessions, catalog discovery, and database operations |
| `internal/resultexport/` | Result encoding and streaming |
| `assets/` | Embedded resources and their licenses |
| `postman/` | Local BigQuery API simulation |

Run the Windows validation commands sequentially:

```powershell
go test ./...
go vet ./...
go build -o build/dibiex.exe .
```

Optional integration checks use `DBCRAFT_POSTGRES_DSN` for read-only PostgreSQL tests and `DBCRAFT_TEST_POSTGRES_DSN` for table editing and COPY tests in an isolated test schema. For local BigQuery API testing, see the [simulation guide](postman/README.md).
