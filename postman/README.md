# BigQuery API simulation

This local stand-in covers only dibiex's paginated datasets, dataset metadata, paginated tables,
query job submission, completion, result pages, and cancellation paths. It is
based on Google's REST documentation and the official Go client's calls; it is
not a Google-supported emulator. Unknown endpoints return 501.

Public API discovery (Orbit) supplied table-listing guidance only. Job operations
were checked against Google's official API documentation and SDK source. The
installed Postman CLI does not support `postman status`, so authenticated owner
search was skipped; no account or workspace was created or changed.

Run from the repository root in one terminal:

```powershell
postman simulation run postman/simulations/bigquery.sim.yaml --port 4010 --no-history --output ndjson
```

In another terminal:

```powershell
$env:DBCRAFT_BIGQUERY_ENDPOINT = 'http://localhost:4010/bigquery/'
go test ./internal/database -run TestBigQuerySimulation -count=1 -v
```

The test uses `WithoutAuthentication` only with its explicit local SDK endpoint.
Production connections use the selected ADC, browser, or service account credentials
and Google's normal endpoint. The mock
fixtures cover project-only queries with no default dataset, paginated datasets,
empty/denied datasets, table/view kinds, result types, row-limit pagination, syntax/permission failures,
execution completion, and timeout cancellation. The simulation's context file
records the limits of this evidence. Nothing needs publishing.

The fixture also covers REST table metadata with recursive/repeated RECORD fields,
keys and references, time/range/ingestion partition specs, clustering order,
metadata 403/404/deadline errors, cache persistence across introspector restarts,
and DML affected-row job statistics. It asserts that base schema reads create no
INFORMATION_SCHEMA jobs, then explicitly loads partition statistics, search/vector
indexes, row-access policies and version-3 table IAM bindings with conditions.
These optional sections are checked across a cache restart. These fixed fixtures do not establish real
IAM behavior or live catalog contents.
On 2026-11-05 the Postman CLI dataset daemon crashed at startup with Windows error
0xc0000008. The same fixture was run directly with Node on port 4013 and
`DBCRAFT_BIGQUERY_ENDPOINT=http://127.0.0.1:4013`; the SDK test passed. This fallback
checks the HTTP client against the fixture, without the Postman simulation router.

References:
- https://docs.cloud.google.com/bigquery/docs/reference/rest/v2/jobs/insert
- https://docs.cloud.google.com/bigquery/docs/reference/rest/v2/jobs/getQueryResults
- https://docs.cloud.google.com/bigquery/docs/reference/rest/v2/jobs/cancel
- https://docs.cloud.google.com/bigquery/docs/reference/rest/v2/tables/list
