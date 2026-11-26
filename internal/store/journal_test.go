package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestJournalInsertListAndFilter(t *testing.T) {
	ctx := context.Background()
	db, err := OpenMemory(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for i := 1; i <= 4; i++ {
		kind := "poke"
		if i == 2 {
			kind = "alert"
		}
		if _, err := db.InsertJournal(ctx, JournalRecord{At: time.Unix(int64(1000+i), 0), Kind: kind, Summary: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	rows, total, err := db.ListJournal(ctx, JournalQuery{Limit: 2})
	if err != nil || total != 4 || len(rows) != 2 || rows[0].Summary != "4" || rows[0].DetailJSON != "{}" {
		t.Fatalf("page %+v total %d %v", rows, total, err)
	}
	rows, _, _ = db.ListJournal(ctx, JournalQuery{Limit: 10, BeforeID: rows[1].ID})
	if len(rows) != 2 || rows[0].Summary != "2" {
		t.Fatalf("older %+v", rows)
	}
	rows, total, _ = db.ListJournal(ctx, JournalQuery{Kinds: []string{"alert"}, Limit: 10})
	if total != 1 || len(rows) != 1 || rows[0].Summary != "2" || rows[0].At.Unix() != 1002 {
		t.Fatalf("filtered %+v", rows)
	}
}
