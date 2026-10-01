package render

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bruin-data/dac/pkg/dashboard"
	"github.com/bruin-data/dac/pkg/query"
	"github.com/bruin-data/dac/pkg/server"
	"github.com/bruin-data/dac/pkg/theme"
)

// Config holds the configuration for a static build.
type Config struct {
	DashboardDir string
	Dashboard    string // dashboard name
	OutputDir    string
	Filters      map[string]any // filter overrides (merged over defaults)
	TemplateName string
	ConfigFile   string
	Environment  string
	Frontend     fs.FS // embedded frontend FS
	Dynamic      bool  // when true, write base tables as Parquet instead of embedding widget data
}

// Build produces a self-contained static directory for a single dashboard.
// The output contains the React SPA with query results baked into index.html
// via a window.__DAC_STATIC__ payload.
func Build(ctx context.Context, cfg Config) error {
	paths := dashboard.ResolveProjectPaths(cfg.DashboardDir)

	// Load dashboards.
	dashboards, err := dashboard.LoadDir(cfg.DashboardDir)
	if err != nil {
		return fmt.Errorf("loading dashboards: %w", err)
	}
	if err := dashboard.ValidateAll(dashboards); err != nil {
		return fmt.Errorf("validating dashboards: %w", err)
	}

	// Find the target dashboard.
	var d *dashboard.Dashboard
	for _, dash := range dashboards {
		if dash.Name == cfg.Dashboard {
			d = dash
			break
		}
	}
	if d == nil {
		return fmt.Errorf("dashboard not found: %q", cfg.Dashboard)
	}

	// Create query backend.
	backend := &query.BruinCLIBackend{
		ConfigFile:  cfg.ConfigFile,
		Environment: cfg.Environment,
	}

	// Set up theme registry.
	themes := theme.NewRegistry()
	templateName := cfg.TemplateName
	if strings.HasSuffix(templateName, ".yml") || strings.HasSuffix(templateName, ".yaml") {
		t, err := theme.LoadFile(templateName)
		if err != nil {
			return fmt.Errorf("loading template file: %w", err)
		}
		themes.Add(t)
		templateName = t.Name
	}
	if paths.ThemesDir != "" {
		if err := themes.LoadUserThemes(paths.ThemesDir); err != nil {
			log.Printf("Warning: could not load user themes: %v", err)
		}
	}

	// Build config payload.
	configPayload := map[string]any{
		"template":      templateName,
		"admin_enabled": false,
	}
	if t, ok := themes.Get(templateName); ok {
		configPayload["tokens"] = t.Tokens
	}

	// Merge filters over dashboard defaults.
	filters := d.DefaultFilters()
	for k, v := range cfg.Filters {
		filters[k] = v
	}

	// Resolve widget jobs.
	jobs, err := server.ResolveWidgetJobs(d, filters)
	if err != nil {
		return fmt.Errorf("resolving widget jobs: %w", err)
	}

	// Build dashboard summaries.
	summaries := make([]server.DashboardSummary, 0, len(dashboards))
	for _, dash := range dashboards {
		summaries = append(summaries, server.MakeDashboardSummary(dash))
	}

	// Build the static payload.
	var payload map[string]any

	if cfg.Dynamic {
		// Dynamic mode: query base tables and write Parquet files.
		tableFiles, err := buildDynamicTables(ctx, backend, jobs, cfg.OutputDir)
		if err != nil {
			return err
		}

		widgetQueries := make(map[string]string)
		widgetRawQueries := make(map[string]string)
		for _, j := range jobs {
			if j.SQL != "" {
				widgetQueries[j.ID] = j.SQL
			}
			if j.RawSQL != "" {
				widgetRawQueries[j.ID] = j.RawSQL
			}
		}

		payload = map[string]any{
			"config":     configPayload,
			"dashboard":  d,
			"dashboards": summaries,
			"filters":    filters,
			"tables":     tableFiles,
			"queries":    widgetQueries,
			"rawQueries": widgetRawQueries,
		}
	} else {
		// Static mode: execute all widget queries and embed results.
		results := make(map[string]*server.WidgetQueryResult)
		var mu sync.Mutex
		var wg sync.WaitGroup

		sem := make(chan struct{}, 8)
		for _, j := range jobs {
			wg.Add(1)
			go func(j server.WidgetJob) {
				defer wg.Done()
				sem <- struct{}{}
				wr := server.ExecuteWidgetQuery(ctx, backend, j)
				<-sem
				mu.Lock()
				results[j.ID] = wr
				mu.Unlock()
			}(j)
		}
		wg.Wait()

		payload = map[string]any{
			"config":     configPayload,
			"dashboard":  d,
			"dashboards": summaries,
			"widgetData": results,
			"filters":    filters,
		}
	}

	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshaling static payload: %w", err)
	}

	// Read index.html from the frontend FS.
	indexBytes, err := fs.ReadFile(cfg.Frontend, "index.html")
	if err != nil {
		return fmt.Errorf("reading index.html from frontend: %w", err)
	}

	// Inject the payload before </head>.
	scriptTag := fmt.Sprintf("<script>window.__DAC_STATIC__=%s;</script>", payloadJSON)
	modifiedIndex := strings.Replace(string(indexBytes), "</head>", scriptTag+"</head>", 1)

	if err := writeStaticOutput(cfg.OutputDir, cfg.Frontend, modifiedIndex); err != nil {
		return err
	}

	return nil
}

// buildDynamicTables queries every unique base table referenced by the widget
// jobs and writes the results as Parquet files under <outputDir>/tables/.
// It returns a map of table identifier to relative file path.
func buildDynamicTables(ctx context.Context, backend query.Backend, jobs []server.WidgetJob, outputDir string) (map[string]string, error) {
	type tableRef struct {
		name       string
		connection string
	}
	uniqueTables := make(map[tableRef]bool)
	for _, j := range jobs {
		for _, t := range j.BaseTables {
			uniqueTables[tableRef{name: t, connection: j.Connection}] = true
		}
	}

	if len(uniqueTables) == 0 {
		return map[string]string{}, nil
	}

	tablesDir := filepath.Join(outputDir, "tables")
	if err := os.RemoveAll(tablesDir); err != nil {
		return nil, fmt.Errorf("clearing stale tables: %w", err)
	}
	if err := os.MkdirAll(tablesDir, 0o755); err != nil {
		return nil, fmt.Errorf("creating tables directory: %w", err)
	}

	tableFiles := make(map[string]string, len(uniqueTables))
	for ref := range uniqueTables {
		connDir := ref.connection
		if connDir == "" {
			connDir = "default"
		}
		relPath := filepath.Join(connDir, ref.name+".parquet")
		outPath := filepath.Join(tablesDir, relPath)

		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return nil, fmt.Errorf("creating table directory: %w", err)
		}

		sql := "SELECT * FROM " + ref.name
		qr, err := backend.Execute(ctx, ref.connection, sql)
		if err != nil {
			continue
			// TODO: FIX THE SQL TBALE SELECTEOR
			// return nil, fmt.Errorf("querying base table %q on connection %q: %w", ref.name, ref.connection, err)
		}

		f, err := os.Create(outPath)
		if err != nil {
			return nil, fmt.Errorf("creating parquet file for %q: %w", ref.name, err)
		}
		// The parquet writer closes the underlying writer on success or error;
		// defer is here as a safety net for early returns before the writer is created.
		defer func() { _ = f.Close() }()
		if err := query.WriteQueryResultToParquet(qr, f); err != nil {
			return nil, fmt.Errorf("writing parquet for %q: %w", ref.name, err)
		}

		key := ref.name
		if ref.connection != "" {
			key = ref.connection + "." + ref.name
		}
		tableFiles[key] = filepath.Join("tables", relPath)
	}

	return tableFiles, nil
}

// writeStaticOutput copies the whole frontend tree (not just index.html's direct
// references) so lazy chunks like vega-embed are included, clearing stale
// content-hashed chunks from prior rebuilds first.
func writeStaticOutput(outputDir string, frontend fs.FS, indexHTML string) error {
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		return fmt.Errorf("creating output directory: %w", err)
	}
	if err := os.RemoveAll(filepath.Join(outputDir, "assets")); err != nil {
		return fmt.Errorf("clearing stale assets: %w", err)
	}

	if err := os.WriteFile(filepath.Join(outputDir, "index.html"), []byte(indexHTML), 0o644); err != nil {
		return fmt.Errorf("writing index.html: %w", err)
	}

	err := fs.WalkDir(frontend, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || path == "index.html" {
			return nil
		}
		data, err := fs.ReadFile(frontend, path)
		if err != nil {
			return fmt.Errorf("reading asset %s: %w", path, err)
		}
		outPath := filepath.Join(outputDir, path)
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return fmt.Errorf("creating directory for %s: %w", path, err)
		}
		if err := os.WriteFile(outPath, data, 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", path, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("copying frontend assets: %w", err)
	}

	return nil
}
