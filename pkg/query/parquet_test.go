package query

import (
	"bytes"
	"testing"
)

func TestWriteQueryResultToParquet(t *testing.T) {
	qr := &QueryResult{
		Columns: []ColumnInfo{
			{Name: "id", Type: "BIGINT"},
			{Name: "name", Type: "VARCHAR"},
			{Name: "score", Type: "DOUBLE"},
			{Name: "active", Type: "BOOLEAN"},
		},
		Rows: [][]interface{}{
			{float64(1), "alice", float64(95.5), true},
			{float64(2), "bob", float64(82.0), false},
			{float64(3), "charlie", nil, true},
		},
	}

	var buf bytes.Buffer
	if err := WriteQueryResultToParquet(qr, &buf); err != nil {
		t.Fatalf("WriteQueryResultToParquet failed: %v", err)
	}

	data := buf.Bytes()
	if len(data) == 0 {
		t.Fatal("expected non-empty parquet output")
	}

	// Parquet files start and end with the magic bytes "PAR1".
	if !bytes.HasPrefix(data, []byte("PAR1")) {
		t.Fatalf("output does not start with parquet magic bytes")
	}
	if !bytes.HasSuffix(data, []byte("PAR1")) {
		t.Fatalf("output does not end with parquet magic bytes")
	}
}

func TestWriteQueryResultToParquet_Empty(t *testing.T) {
	qr := &QueryResult{
		Columns: []ColumnInfo{},
		Rows:    [][]interface{}{},
	}

	var buf bytes.Buffer
	if err := WriteQueryResultToParquet(qr, &buf); err != nil {
		t.Fatalf("WriteQueryResultToParquet failed for empty result: %v", err)
	}

	data := buf.Bytes()
	if len(data) == 0 {
		t.Fatal("expected non-empty parquet output even for empty schema")
	}

	if !bytes.HasPrefix(data, []byte("PAR1")) {
		t.Fatalf("output does not start with parquet magic bytes")
	}
	if !bytes.HasSuffix(data, []byte("PAR1")) {
		t.Fatalf("output does not end with parquet magic bytes")
	}
}
