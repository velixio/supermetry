package storageusage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
)

func TestSourceRegistryClassifiesRepresentativeStorage(t *testing.T) {
	want := map[string]Category{
		"events":                CategoryHot,
		"sessions":              CategoryHot,
		"sessions_closed":       CategoryRetained,
		"rollup_month_dim":      CategoryRetained,
		"imports":               CategoryAuxiliary,
		"search_console_rows":   CategoryAuxiliary,
		"replay_sessions":       CategoryAuxiliary,
		"link_clicks_daily":     CategoryAuxiliary,
		"pixel_views_daily":     CategoryAuxiliary,
		"data_purge_sessions":   CategoryAuxiliary,
		"profile_erasure_blobs": CategoryAuxiliary,
	}
	got := make(map[string]Category)
	for _, source := range SourceRegistry() {
		if source.ExcludedReason == "" {
			got[source.Table] = source.Category
		}
	}
	for table, category := range want {
		if got[table] != category {
			t.Errorf("source %s category = %q, want %q", table, got[table], category)
		}
	}
	for _, table := range []string{"storage_usage_run_sites", "storage_usage_stage", "site_storage_usage"} {
		var reason string
		for _, source := range SourceRegistry() {
			if source.Table == table {
				reason = source.ExcludedReason
			}
		}
		if reason == "" {
			t.Errorf("source %s has no deliberate exclusion reason", table)
		}
	}
}

func TestSourceRegistryFingerprintTracksDefinition(t *testing.T) {
	scanner := NewPostgresScanner(nil, time.Second)
	before := scanner.Fingerprint()
	original := sourceRegistry
	sourceRegistry = append(append([]Source(nil), sourceRegistry...), Source{Table: "future_site_table", Category: CategoryAuxiliary})
	t.Cleanup(func() { sourceRegistry = original })
	if after := scanner.Fingerprint(); after == before {
		t.Fatal("registry definition change did not change scanner fingerprint")
	}
}

func TestCatalogEverySiteIDTableIsClassified(t *testing.T) {
	_, pool, _ := newStoreFixture(t, 0)
	rows, err := pool.Query(context.Background(), `
		select c.relname
		from pg_class c
		join pg_namespace n on n.oid=c.relnamespace
		join pg_attribute a on a.attrelid=c.oid and a.attname='site_id' and not a.attisdropped
		where n.nspname=current_schema()
		  and c.relkind in ('r','p')
		  and not c.relispartition
		order by c.relname`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	classified := make(map[string]bool)
	for _, source := range SourceRegistry() {
		classified[source.Table] = true
	}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		if !classified[table] {
			t.Errorf("site-owned table %q is not measured or deliberately excluded", table)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRangedSourcesHaveLeadingSiteIDIndex(t *testing.T) {
	_, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	checked := map[string]bool{}
	for _, source := range SourceRegistry() {
		if source.Partitioned || source.ExcludedReason != "" {
			continue
		}
		table := source.Table
		if source.OwnerTable != "" {
			table = source.OwnerTable
		}
		if checked[table] {
			continue
		}
		checked[table] = true
		var indexed bool
		if err := pool.QueryRow(ctx, `
			select exists(
				select 1 from pg_index i
				join pg_attribute a on a.attrelid=i.indrelid and a.attnum=i.indkey[0]
				where i.indrelid=to_regclass($1) and i.indisvalid and a.attname='site_id'
			)`, table).Scan(&indexed); err != nil {
			t.Fatal(err)
		}
		if !indexed {
			t.Errorf("ranged source owner %s has no leading site_id index", table)
		}
	}
}

func TestPostgresWorkUnitsUseOneLeafRelation(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 0)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := db.EnsurePartitions(ctx, pool, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, 3*time.Second)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var eventUnits int
	for _, unit := range units {
		if unit.Table != "events" {
			continue
		}
		eventUnits++
		if unit.Relation == "events" || !strings.HasPrefix(unit.Relation, "events_r") {
			t.Errorf("events work unit relation = %q, want leaf partition", unit.Relation)
		}
		query := scanner.scanSQL(unit)
		if strings.Count(query, " from ") != 1 || strings.Contains(query, " events ") {
			t.Errorf("scan query is not one bounded relation: %s", query)
		}
	}
	if eventUnits == 0 {
		t.Fatal("no events leaf work units discovered")
	}
}

func TestPostgresScanMeasuresLogicalRowsBySite(t *testing.T) {
	store, pool, sites := newStoreFixture(t, 3)
	ctx := context.Background()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, from, from.AddDate(0, 0, 2)); err != nil {
		t.Fatal(err)
	}
	large := strings.Repeat("large-value-", 20_000)
	if _, err := pool.Exec(ctx, `
		insert into events(site_id,retention_class,local_day,ts,visitor_id,session_id,kind,props)
		values($1,7,'2026-10-01','2026-10-01T01:00:00Z',1,1,0,jsonb_build_object('large',$3::text)),
		      ($2,7,'2026-10-01','2026-10-01T02:00:00Z',2,2,0,'{}')`, sites[0], sites[1], large); err != nil {
		t.Fatal(err)
	}
	var importID int64
	if err := pool.QueryRow(ctx, `insert into imports(site_id,source,status,file_name) values($1,'umami','completed','fixture.json') returning id`, sites[0]).Scan(&importID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into imported_day(site_id,import_id,day,visitors) values($1,$2,'2026-10-01',12)`, sites[0], importID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into annotations(site_id,day,text) values($1,'2026-10-01','deploy')`, sites[1]); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, from)
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, 3*time.Second)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	measured := make(map[int32]int64)
	for _, unit := range units {
		if unit.Table != "events" || !strings.Contains(unit.Relation, "20261001") {
			continue
		}
		values, err := scanner.Scan(ctx, run.ID, unit)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range values[0].Values {
			measured[value.SiteID] += value.Bytes
		}
	}
	for _, siteID := range sites[:2] {
		var want int64
		if err := pool.QueryRow(ctx, `select coalesce(sum(pg_column_size(e)),0)::bigint from events e where site_id=$1`, siteID).Scan(&want); err != nil {
			t.Fatal(err)
		}
		if measured[siteID] != want || want <= 0 {
			t.Errorf("site %d event bytes = %d, want %d", siteID, measured[siteID], want)
		}
	}
	if measured[sites[2]] != 0 {
		t.Errorf("empty site bytes = %d", measured[sites[2]])
	}

	// Unpartitioned retained and auxiliary sources are each complete work units.
	for _, table := range []string{"imported_day", "annotations"} {
		unit := findWorkUnit(t, units, table)
		values, err := scanner.Scan(ctx, run.ID, unit)
		if err != nil {
			t.Fatal(err)
		}
		if len(values) != 1 || len(values[0].Values) != 1 || values[0].Values[0].Bytes <= 0 {
			t.Errorf("%s contributions = %+v", table, values)
		}
	}
}

func TestPostgresScanRejectsForgedRelation(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	run, err := store.BeginOrResume(context.Background(), MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, time.Second)
	_, err = scanner.Scan(context.Background(), run.ID, WorkUnit{Key: "bad", Table: "events", Relation: `events; drop table sites`, Category: CategoryHot})
	if err == nil {
		t.Fatal("forged relation was accepted")
	}
	var exists bool
	if err := pool.QueryRow(context.Background(), `select to_regclass('sites') is not null`).Scan(&exists); err != nil || !exists {
		t.Fatalf("sites table after forged relation = %v, %v", exists, err)
	}
}

func TestPostgresUnpartitionedSourcesUseStableSiteRanges(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	ctx := context.Background()
	var orgID int64
	if err := pool.QueryRow(ctx, `insert into orgs(name,slug) values('Range','range') returning id`).Scan(&orgID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `insert into sites(id,org_id,public_id,name,timezone,retention_class) overriding system value select n,$1,'r'||lpad(n::text,11,'0'),'Range','UTC',30 from generate_series(1000,1100) n`, orgID); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, time.Second)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var annotationRanges []WorkUnit
	for _, unit := range units {
		if unit.Table == "annotations" {
			annotationRanges = append(annotationRanges, unit)
		}
	}
	if len(annotationRanges) != 2 {
		t.Fatalf("annotation ranges = %+v, want two site-id batches", annotationRanges)
	}
	for _, unit := range annotationRanges {
		query := scanner.scanSQL(unit)
		if !strings.Contains(query, "row_alias.site_id>=$2") || !strings.Contains(query, "row_alias.site_id<$3") {
			t.Errorf("range query = %s", query)
		}
	}
	indirect := findWorkUnit(t, units, "link_clicks_daily")
	if query := scanner.scanSQL(indirect); !strings.Contains(query, "owner.site_id>=$2") || !strings.Contains(query, "owner.site_id<$3") {
		t.Errorf("indirect range query = %s", query)
	}
}

func TestPostgresDroppedManifestLeafBecomesEmptyContribution(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	ctx := context.Background()
	from := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	if err := db.EnsurePartitions(ctx, pool, from, from.AddDate(0, 0, 1)); err != nil {
		t.Fatal(err)
	}
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, time.Second)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	var leaf WorkUnit
	for _, unit := range units {
		if unit.Table == "events" && strings.Contains(unit.Relation, "20261001") {
			leaf = unit
			break
		}
	}
	if leaf.Key == "" {
		t.Fatal("events leaf not discovered")
	}
	if _, err := pool.Exec(ctx, "drop table "+pgx.Identifier{leaf.Relation}.Sanitize()); err != nil {
		t.Fatal(err)
	}
	got, err := scanner.Scan(ctx, run.ID, leaf)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Category != CategoryHot || len(got[0].Values) != 0 {
		t.Fatalf("dropped leaf measurement = %+v", got)
	}
}

func TestPostgresEveryDiscoveredWorkUnitScans(t *testing.T) {
	store, pool, _ := newStoreFixture(t, 1)
	ctx := context.Background()
	run, err := store.BeginOrResume(ctx, MeasurementVersion, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	scanner := NewPostgresScanner(pool, 3*time.Second)
	units, err := scanner.WorkUnits(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(units) == 0 {
		t.Fatal("no PostgreSQL work units discovered")
	}
	for _, unit := range units {
		t.Run(unit.Key, func(t *testing.T) {
			values, err := scanner.Scan(ctx, run.ID, unit)
			if err != nil {
				t.Fatalf("Scan() error = %v", err)
			}
			if len(values) != 1 || len(values[0].Values) != 0 {
				t.Fatalf("empty database contributions = %+v", values)
			}
		})
	}
}

func findWorkUnit(t *testing.T, units []WorkUnit, table string) WorkUnit {
	t.Helper()
	for _, unit := range units {
		if unit.Table == table {
			return unit
		}
	}
	t.Fatalf("work unit for %s not found", table)
	return WorkUnit{}
}
