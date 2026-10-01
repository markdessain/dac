package query

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/apache/arrow/go/v17/arrow"
	"github.com/apache/arrow/go/v17/arrow/array"
	"github.com/apache/arrow/go/v17/arrow/memory"
	"github.com/apache/arrow/go/v17/parquet"
	"github.com/apache/arrow/go/v17/parquet/pqarrow"
)

// WriteQueryResultToParquet writes a QueryResult as a Parquet file.
// Column types are inferred from the metadata in qr.Columns; values are
// adapted from the JSON-decoded interface{} types returned by the bruin
// backend.
func WriteQueryResultToParquet(qr *QueryResult, w io.Writer) error {
	if len(qr.Columns) == 0 {
		schema := arrow.NewSchema([]arrow.Field{}, nil)
		writer, err := pqarrow.NewFileWriter(schema, w, parquet.NewWriterProperties(), pqarrow.DefaultWriterProps())
		if err != nil {
			return fmt.Errorf("creating parquet writer for empty result: %w", err)
		}
		return writer.Close()
	}

	// 1. Build Arrow schema from column metadata.
	fields := make([]arrow.Field, len(qr.Columns))
	for i, col := range qr.Columns {
		fields[i] = arrow.Field{
			Name:     col.Name,
			Type:     inferArrowType(col.Type),
			Nullable: true,
		}
	}
	schema := arrow.NewSchema(fields, nil)

	// 2. Build record batch from rows.
	pool := memory.NewGoAllocator()
	builders := make([]array.Builder, len(fields))
	for i, f := range fields {
		builders[i] = array.NewBuilder(pool, f.Type)
	}

	for _, row := range qr.Rows {
		for i, val := range row {
			if val == nil {
				builders[i].AppendNull()
			} else {
				appendValue(builders[i], val, fields[i].Type)
			}
		}
	}

	columns := make([]arrow.Array, len(builders))
	for i, b := range builders {
		columns[i] = b.NewArray()
	}
	record := array.NewRecord(schema, columns, int64(len(qr.Rows)))
	defer record.Release()

	// 3. Write to parquet.
	writer, err := pqarrow.NewFileWriter(schema, w, parquet.NewWriterProperties(), pqarrow.DefaultWriterProps())
	if err != nil {
		return fmt.Errorf("creating parquet writer: %w", err)
	}
	if err := writer.Write(record); err != nil {
		writer.Close()
		return fmt.Errorf("writing parquet record: %w", err)
	}
	return writer.Close()
}

func inferArrowType(bruinType string) arrow.DataType {
	lower := strings.ToLower(bruinType)
	switch {
	case strings.Contains(lower, "bigint"), strings.Contains(lower, "int64"):
		return arrow.PrimitiveTypes.Int64
	case strings.Contains(lower, "integer"), strings.Contains(lower, "int32"), strings.Contains(lower, "int"):
		return arrow.PrimitiveTypes.Int32
	case strings.Contains(lower, "smallint"), strings.Contains(lower, "int16"):
		return arrow.PrimitiveTypes.Int16
	case strings.Contains(lower, "tinyint"), strings.Contains(lower, "int8"):
		return arrow.PrimitiveTypes.Int8
	case strings.Contains(lower, "double"), strings.Contains(lower, "float8"):
		return arrow.PrimitiveTypes.Float64
	case strings.Contains(lower, "real"), strings.Contains(lower, "float"),
		strings.Contains(lower, "numeric"), strings.Contains(lower, "decimal"):
		return arrow.PrimitiveTypes.Float64
	case strings.Contains(lower, "boolean"), strings.Contains(lower, "bool"):
		return arrow.FixedWidthTypes.Boolean
	case strings.Contains(lower, "date"):
		return arrow.FixedWidthTypes.Date32
	case strings.Contains(lower, "timestamp"), strings.Contains(lower, "datetime"):
		return arrow.FixedWidthTypes.Timestamp_us
	case strings.Contains(lower, "time"):
		return arrow.FixedWidthTypes.Time64us
	default:
		// JSON, structs, blobs, unknown types → string.
		return arrow.BinaryTypes.String
	}
}

func appendValue(b array.Builder, val interface{}, dt arrow.DataType) {
	switch dt.ID() {
	case arrow.INT64:
		switch v := val.(type) {
		case float64:
			b.(*array.Int64Builder).Append(int64(v))
		case int64:
			b.(*array.Int64Builder).Append(v)
		case int:
			b.(*array.Int64Builder).Append(int64(v))
		case string:
			if i, err := strconv.ParseInt(v, 10, 64); err == nil {
				b.(*array.Int64Builder).Append(i)
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	case arrow.INT32:
		switch v := val.(type) {
		case float64:
			b.(*array.Int32Builder).Append(int32(v))
		case int32:
			b.(*array.Int32Builder).Append(v)
		case int:
			b.(*array.Int32Builder).Append(int32(v))
		case string:
			if i, err := strconv.ParseInt(v, 10, 32); err == nil {
				b.(*array.Int32Builder).Append(int32(i))
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	case arrow.INT16:
		switch v := val.(type) {
		case float64:
			b.(*array.Int16Builder).Append(int16(v))
		case int16:
			b.(*array.Int16Builder).Append(v)
		case int:
			b.(*array.Int16Builder).Append(int16(v))
		case string:
			if i, err := strconv.ParseInt(v, 10, 16); err == nil {
				b.(*array.Int16Builder).Append(int16(i))
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	case arrow.INT8:
		switch v := val.(type) {
		case float64:
			b.(*array.Int8Builder).Append(int8(v))
		case int8:
			b.(*array.Int8Builder).Append(v)
		case int:
			b.(*array.Int8Builder).Append(int8(v))
		case string:
			if i, err := strconv.ParseInt(v, 10, 8); err == nil {
				b.(*array.Int8Builder).Append(int8(i))
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	case arrow.FLOAT64:
		switch v := val.(type) {
		case float64:
			b.(*array.Float64Builder).Append(v)
		case float32:
			b.(*array.Float64Builder).Append(float64(v))
		case string:
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				b.(*array.Float64Builder).Append(f)
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	case arrow.BOOL:
		switch v := val.(type) {
		case bool:
			b.(*array.BooleanBuilder).Append(v)
		case string:
			switch v {
			case "true", "1", "t", "yes", "TRUE", "True":
				b.(*array.BooleanBuilder).Append(true)
			case "false", "0", "f", "no", "FALSE", "False":
				b.(*array.BooleanBuilder).Append(false)
			default:
				b.AppendNull()
			}
		case float64:
			b.(*array.BooleanBuilder).Append(v != 0)
		default:
			b.AppendNull()
		}
	case arrow.STRING:
		if s, ok := val.(string); ok {
			b.(*array.StringBuilder).Append(s)
		} else {
			b.AppendNull()
		}
	case arrow.DATE32:
		switch v := val.(type) {
		case string:
			// Try common date / datetime string formats.
			formats := []string{
				"2006-01-02",
				time.RFC3339,
				time.RFC3339Nano,
				"2006-01-02T15:04:05",
				"2006-01-02T15:04:05Z",
				"2006-01-02 15:04:05",
				"2006-01-02 15:04:05.999999",
			}
			var t time.Time
			var err error
			for _, f := range formats {
				t, err = time.Parse(f, v)
				if err == nil {
					break
				}
			}
			if err == nil {
				b.(*array.Date32Builder).Append(arrow.Date32FromTime(t))
			} else {
				b.AppendNull()
			}
		case float64:
			b.(*array.Date32Builder).Append(arrow.Date32(int32(v)))
		default:
			b.AppendNull()
		}
	case arrow.TIMESTAMP:
		tb := b.(*array.TimestampBuilder)
		switch v := val.(type) {
		case string:
			ts, err := arrow.TimestampFromString(v, arrow.Microsecond)
			if err != nil {
				// Fallback: try common SQL / ISO timestamp formats.
				var t time.Time
				formats := []string{
					"2006-01-02 15:04:05",
					"2006-01-02 15:04:05.999999",
					time.RFC3339,
					time.RFC3339Nano,
					"2006-01-02T15:04:05",
					"2006-01-02T15:04:05Z",
					"2006-01-02",
				}
				for _, f := range formats {
					t, err = time.Parse(f, v)
					if err == nil {
						break
					}
				}
				if err == nil {
					ts, _ = arrow.TimestampFromTime(t, arrow.Microsecond)
				}
			}
			if err == nil {
				tb.Append(ts)
			} else {
				b.AppendNull()
			}
		case float64:
			tb.Append(arrow.Timestamp(int64(v)))
		default:
			b.AppendNull()
		}
	case arrow.TIME64:
		tb := b.(*array.Time64Builder)
		switch v := val.(type) {
		case string:
			t, err := time.Parse("15:04:05", v)
			if err != nil {
				t, err = time.Parse("15:04:05.999999", v)
			}
			if err == nil {
				tb.Append(arrow.Time64(t.UnixNano() / 1000))
			} else {
				b.AppendNull()
			}
		default:
			b.AppendNull()
		}
	default:
		// Unhandled Arrow type — attempt string fallback.
		if s, ok := val.(string); ok {
			if sb, ok := b.(*array.StringBuilder); ok {
				sb.Append(s)
				return
			}
		}
		b.AppendNull()
	}
}
