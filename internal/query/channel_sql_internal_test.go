package query

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5"

	"supermetry/internal/db"
	"supermetry/internal/enrich"
	"supermetry/internal/testpg"
)

func TestChannelSQLMatchesEnrichChannel(t *testing.T) {
	pool, _ := testpg.New(t)
	ctx := context.Background()
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	sources := []string{"Google", "google", "GOOGLE", "Bing", "Facebook", "Reddit", "ChatGPT", "chatgpt", "Claude",
		"Gemini", "Copilot", "Perplexity", "GitHub", "newsletter", "example.com", "Direct", "Foo", "O'Reilly"}
	mediums := []string{"organic", "search", "social", "paid", "cpc", "paid-search", "paid_search", "paid_social",
		"email", "Email", "referral", "banner"}
	if _, err := pool.Exec(ctx, `insert into dict(kind, value) select 2, unnest($1::text[])
union all select 3, unnest($2::text[]) on conflict do nothing`, sources, mediums); err != nil {
		t.Fatal(err)
	}
	type pair struct{ Source, Medium, Got string }
	rows, err := pool.Query(ctx, `
with src(v, id) as (select ''::text, 0 union all select value, id from dict where kind=2 and value = any($1)),
     med(v, id) as (select ''::text, 0 union all select value, id from dict where kind=3 and value = any($2))
select src.v, med.v, `+channelSQL("src.id", "med.id")+` from src cross join med`, sources, mediums)
	if err != nil {
		t.Fatal(err)
	}
	pairs, err := pgx.CollectRows(rows, pgx.RowToStructByPos[pair])
	if err != nil || len(pairs) != (len(sources)+1)*(len(mediums)+1) {
		t.Fatalf("rows = %d, err = %v", len(pairs), err)
	}
	for _, p := range pairs {
		if want := enrich.Channel(p.Source, p.Medium); p.Got != want {
			t.Errorf("SQL(%q, %q) = %q, Go = %q", p.Source, p.Medium, p.Got, want)
		}
	}
}
