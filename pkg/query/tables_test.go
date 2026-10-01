package query

import (
	"reflect"
	"testing"
)

func TestExtractBaseTables(t *testing.T) {
	tests := []struct {
		name string
		sql  string
		want []string
	}{
		{
			name: "simple select",
			sql:  "SELECT id, name FROM users",
			want: []string{"users"},
		},
		{
			name: "qualified table",
			sql:  "SELECT * FROM analytics.orders",
			want: []string{"analytics.orders"},
		},
		{
			name: "three-part name",
			sql:  "SELECT * FROM catalog.schema.table",
			want: []string{"catalog.schema.table"},
		},
		{
			name: "join",
			sql:  "SELECT * FROM orders JOIN customers ON orders.customer_id = customers.id",
			want: []string{"customers", "orders"},
		},
		{
			name: "left join",
			sql:  "SELECT * FROM orders LEFT JOIN customers ON orders.customer_id = customers.id",
			want: []string{"customers", "orders"},
		},
		{
			name: "subquery",
			sql:  "SELECT * FROM orders WHERE status IN (SELECT code FROM status_codes)",
			want: []string{"orders", "status_codes"},
		},
		{
			name: "CTE_masks_CTE_refs",
			sql:  "WITH a AS (SELECT * FROM raw), b AS (SELECT * FROM staging) SELECT * FROM a LEFT JOIN b",
			want: []string{"raw", "staging"},
		},
		{
			name: "CTE_and_real_table",
			sql:  "WITH cte AS (SELECT * FROM base) SELECT * FROM cte JOIN other",
			want: []string{"base", "other"},
		},
		{
			name: "no tables",
			sql:  "SELECT 1 + 1",
			want: nil,
		},
		{
			name: "multiline",
			sql: `SELECT *
				FROM orders
				JOIN customers
				ON orders.customer_id = customers.id`,
			want: []string{"customers", "orders"},
		},
		{
			name: "extract_from_not_a_table",
			sql:  "SELECT EXTRACT(HOUR FROM tracked_at) AS hour FROM events",
			want: []string{"events"},
		},
		{
			name: "extract_from_inside_cte",
			sql:  "WITH a AS (SELECT EXTRACT(DAY FROM created_at) AS d FROM raw) SELECT d FROM a",
			want: []string{"raw"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ExtractBaseTables(tt.sql)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ExtractBaseTables() = %v, want %v", got, tt.want)
			}
		})
	}
}
