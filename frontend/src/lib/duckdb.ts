import * as duckdb from "@duckdb/duckdb-wasm";
import type { WidgetData } from "../types/dashboard";

let dbInstance: duckdb.AsyncDuckDB | null = null;
let initPromise: Promise<void> | null = null;

/**
 * Initialize DuckDB-WASM and register all base-table Parquet files as views.
 * Safe to call multiple times — the actual work only happens on the first call.
 */
export async function initDuckDBDynamic(
  tables: Record<string, string>,
): Promise<void> {
  if (initPromise) return initPromise;

  initPromise = (async () => {
    const JSDELIVR_BUNDLES = duckdb.getJsDelivrBundles();
    const bundle = await duckdb.selectBundle(JSDELIVR_BUNDLES);

    const worker_url = URL.createObjectURL(
      new Blob([`importScripts("${bundle.mainWorker}");`], {
        type: "text/javascript",
      }),
    );
    const worker = new Worker(worker_url);
    const logger = new duckdb.ConsoleLogger();
    const db = new duckdb.AsyncDuckDB(logger, worker);
    await db.instantiate(bundle.mainModule, bundle.pthreadWorker);

    const conn = await db.connect();
    try {
      for (const [tableKey, path] of Object.entries(tables)) {
        const response = await fetch(path);
        if (!response.ok) {
          throw new Error(
            `Failed to fetch parquet ${path}: ${response.status}`,
          );
        }
        const buffer = new Uint8Array(await response.arrayBuffer());
        const fileName = `${tableKey}.parquet`;
        await db.registerFileBuffer(fileName, buffer);

        // tableKey is "<connection>.<schema>.<table>" or "<connection>.<table>".
        // The first part is always the connection prefix added by buildDynamicTables.
        const parts = tableKey.split(".");
        const tableRef = parts.length > 1 ? parts.slice(1).join(".") : parts[0];

        if (tableRef.includes(".")) {
          // Schema-qualified name: e.g. "schema.table" or "catalog.schema.table"
          const refParts = tableRef.split(".");
          const schema = refParts.slice(0, -1).join(".");
          const table = refParts[refParts.length - 1];
          await conn.query(`CREATE SCHEMA IF NOT EXISTS "${schema}"`);
          await conn.query(
            `CREATE OR REPLACE VIEW "${schema}"."${table}" AS SELECT * FROM read_parquet('${fileName}')`,
          );
        } else {
          await conn.query(
            `CREATE OR REPLACE VIEW "${tableRef}" AS SELECT * FROM read_parquet('${fileName}')`,
          );
        }
      }
    } finally {
      await conn.close();
    }

    dbInstance = db;
  })();

  return initPromise;
}

/**
 * Render a raw SQL template by substituting `{{ filters.<name> }}` with the
 * current filter values, then execute it in DuckDB-WASM.
 */
export async function executeWidgetQuery(
  sql: string,
  filters: Record<string, unknown>,
): Promise<WidgetData> {
  if (!dbInstance) {
    return { columns: [], rows: [], error: "DuckDB not initialized" };
  }

  const conn = await dbInstance.connect();
  try {
    const renderedSql = substituteFilters(sql, filters);
    console.log("[duckdb] sql:", renderedSql.substring(0, 300));
    console.log("[duckdb] filters:", JSON.stringify(filters));
    if (renderedSql.includes("{{") || renderedSql.includes("{%")) {
      console.warn("[duckdb] Unsubstituted Jinja syntax remains in SQL");
    }
    const result = await conn.query(renderedSql);

    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const resultAny = result as any;
    const schema = resultAny.schema;
    if (!schema || !schema.fields || schema.fields.length === 0) {
      console.log("[duckdb] empty schema");
      return { columns: [], rows: [], query: renderedSql };
    }

    const columns = schema.fields.map((f: { name: string }) => ({
      name: f.name,
      type: undefined as string | undefined,
    }));

    const rows: unknown[][] = [];

    // Strategy 1: toArray() — standard in Arrow JS v12+
    if (typeof resultAny.toArray === "function") {
      console.log("[duckdb] using toArray()");
      for (const rowObj of resultAny.toArray()) {
        rows.push(columns.map((c) => normalizeArrowValue(rowObj[c.name])));
      }
    }
    // Strategy 2: .data array of RecordBatches
    else if (resultAny.data && Array.isArray(resultAny.data)) {
      console.log("[duckdb] using result.data");
      for (const batch of resultAny.data) {
        extractBatchRows(batch, columns, rows);
      }
    }
    // Strategy 3: Symbol.iterator
    else if (typeof resultAny[Symbol.iterator] === "function") {
      console.log("[duckdb] using Symbol.iterator");
      for (const batch of resultAny) {
        extractBatchRows(batch, columns, rows);
      }
    } else {
      console.error(
        "[duckdb] Cannot extract rows from Arrow result:",
        resultAny,
      );
      return {
        columns,
        rows: [],
        query: renderedSql,
        error: "Unexpected Arrow result format",
      };
    }

    console.log(
      `[duckdb] ${columns.length} cols, ${rows.length} rows. first:`,
      rows[0] ?? "(none)",
    );
    return { columns, rows, query: renderedSql };
  } catch (err) {
    const msg = err instanceof Error ? err.message : String(err);
    console.error("[duckdb] query error:", msg);
    return { columns: [], rows: [], query: sql, error: msg };
  } finally {
    await conn.close();
  }
}

function extractBatchRows(
  batch: unknown,
  columns: { name: string }[],
  rows: unknown[][],
) {
  const b = batch as { numRows: number; get?: (i: number) => Record<string, unknown>; getChildAt?: (j: number) => { get: (i: number) => unknown } | null };
  if (!b || typeof b.numRows !== "number") return;
  for (let i = 0; i < b.numRows; i++) {
    let rowObj: Record<string, unknown> | null = null;
    if (typeof b.get === "function") {
      rowObj = b.get(i);
    } else if (typeof b.getChildAt === "function") {
      rowObj = {};
      for (let j = 0; j < columns.length; j++) {
        const col = b.getChildAt(j);
        rowObj[columns[j].name] = col ? col.get(i) : null;
      }
    }
    if (rowObj) {
      rows.push(columns.map((c) => normalizeArrowValue(rowObj![c.name])));
    }
  }
}

function normalizeArrowValue(val: unknown): unknown {
  if (val === null || val === undefined) return null;
  if (val instanceof Date) return val.toISOString();
  if (typeof val === "bigint") {
    if (
      val > Number.MAX_SAFE_INTEGER ||
      val < Number.MIN_SAFE_INTEGER
    ) {
      return String(val);
    }
    return Number(val);
  }
  if (
    typeof val === "number" ||
    typeof val === "string" ||
    typeof val === "boolean"
  ) {
    return val;
  }
  return String(val);
}

function substituteFilters(
  sql: string,
  filters: Record<string, unknown>,
): string {
  return sql.replace(/\{\{\s*filters\.([\w.]+)\s*\}\}/g, (_match, key) => {
    const parts = key.split(".");
    let value: unknown = filters;
    for (const part of parts) {
      if (value === null || value === undefined) break;
      value = (value as Record<string, unknown>)[part];
    }
    if (value === undefined || value === null) return "NULL";
    if (Array.isArray(value)) {
      if (value.length === 0) return "NULL";
      return value
        .map((v) =>
          typeof v === "string" ? `'${escapeSqlString(v)}'` : String(v),
        )
        .join(", ");
    }
    if (typeof value === "string") return escapeSqlString(value);
    if (typeof value === "boolean") return value ? "TRUE" : "FALSE";
    if (typeof value === "number") return String(value);
    return escapeSqlString(String(value));
  });
}

function escapeSqlString(s: string): string {
  return s.replace(/'/g, "''");
}
