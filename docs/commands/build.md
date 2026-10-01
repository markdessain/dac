# dac build

Build a self-contained static dashboard with baked-in query results. The output is a directory with an HTML file and assets that can be deployed anywhere — no server required.

```shell
dac build [flags]
```

## Flags

| Flag | Alias | Type | Default | Description |
|------|-------|------|---------|-------------|
| `--dashboard` | `-n` | string | *required* | Dashboard name |
| `--output` | `-o` | string | `build` | Output directory |
| `--dir` | `-d` | string | `.` | Dashboard definitions directory |
| `--template` | `-t` | string | `bruin` | Theme name or path to YAML file |
| `--filters` | | string | | JSON string with filter overrides |
| `--dynamic` | | bool | `false` | Query base tables and save as Parquet instead of baking widget data |

## Examples

```shell
# Build the "Sales Analytics" dashboard
dac build --dashboard "Sales Analytics"

# Custom output directory and theme
dac build --dashboard "Sales Analytics" --output dist --template bruin-dark

# Build with specific filter values baked in
dac build --dashboard "Sales Analytics" --filters '{"region": "Europe", "date_range": "last_30_days"}'

# Build in dynamic mode — execute queries in the browser via DuckDB-WASM
dac build --dashboard "Sales Analytics" --dynamic
```

## Output

### Static mode (default)

The build produces a self-contained directory with all query results baked in:

```
build/
├── index.html
└── assets/
    ├── index-[hash].js
    └── index-[hash].css
```

The HTML file includes a `window.__DAC_STATIC__` payload containing:
- The full dashboard definition
- Pre-computed query results for all widgets
- Theme tokens
- Filter defaults

Semantic widgets are compiled to SQL during the build, then the generated SQL results are baked into the static payload.

### Dynamic mode (`--dynamic`)

Instead of embedding widget data, the build queries each underlying base table with `SELECT *` and writes the results as **Parquet** files. Widget SQL is executed in the browser via **DuckDB-WASM**, so filter changes are reflected immediately without rebuilding.

```
build/
├── index.html
├── assets/
│   ├── index-[hash].js
│   └── index-[hash].css
└── tables/
    └── <connection>/
        └── <table>.parquet
```

The `window.__DAC_STATIC__` payload contains:
- The full dashboard definition
- `tables` — map of table identifier → relative Parquet path
- `queries` — rendered SQL for each widget
- `rawQueries` — unrendered SQL templates (for client-side filter substitution)
- `filters` — default filter values

Open `index.html` in a browser on any static hosting. The frontend fetches the Parquet files and instantiates DuckDB-WASM automatically.

## Dynamic mode explained

In dynamic mode:

1. Every base table discovered in widget SQL is extracted (CTEs are excluded).
2. For each unique table, the build runs `SELECT * FROM <table>` and saves the result as a Parquet file.
3. The frontend loads DuckDB-WASM from a CDN on first use.
4. Parquet files are fetched and registered as named views (`CREATE VIEW <table> AS SELECT * FROM read_parquet(...)`).
5. When filters change in the UI, the frontend substitutes `{{ filters.<name> }}` placeholders in the raw SQL template and re-executes the query in DuckDB-WASM.

**SQL templates in dynamic mode**

The frontend substitution behaves like Jinja — it inserts the raw value without adding quotes. Put quotes in the template itself where they belong:

```sql
-- String/date filters: wrap the placeholder in quotes
WHERE region = '{{ filters.region }}'
WHERE created_at <= DATE '{{ filters.end_date }}'

-- Number filters: no quotes needed
WHERE id = {{ filters.id }}

-- Multi-select / IN clauses: no quotes around the placeholder
WHERE region IN ({{ filters.region }})
```

This is ideal for dashboards where users need to explore data interactively without a running backend server.

## Use Cases

- **Scheduled reports**: Build on a cron, upload to S3, share a link
- **Offline viewing**: Send dashboards to stakeholders who don't have database access
- **Embedding**: Include dashboard HTML in other applications
- **Archival**: Snapshot a dashboard's state at a point in time
- **Interactive exploration**: Build with `--dynamic` and let users change filters in the browser without a backend
