package store

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// TestListsAndDeliveryLeaveTheFilesBehind is R1b-C09: an Inbox list and the deliverer read every
// question's metadata, never its attachments, and still know how many files there are.
func TestListsAndDeliveryLeaveTheFilesBehind(t *testing.T) {
	db, err := OpenMemory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	big := strings.Repeat("x", 64<<10)
	files, _ := json.Marshal([]map[string]string{{"filename": "a.go", "original": big, "final": big}, {"filename": "b.go"}})
	now := time.Unix(1_800_000_000, 0)
	id, err := db.InsertQuestion(ctx, QuestionRecord{Title: "Diff", BodyMD: "body", Kind: "ack", Urgency: "normal", Files: files,
		SourcePane: "w1:p1", CreatedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	listed, err := db.ListQuestions(ctx, nil, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("list: %v %v", listed, err)
	}
	if string(listed[0].Files) != "[]" || listed[0].FileCount != 2 {
		t.Fatalf("list carried %d bytes of files, count %d", len(listed[0].Files), listed[0].FileCount)
	}
	if err := db.SettleQuestion(ctx, id, "answered", json.RawMessage(`"ack"`), "", "type", now); err != nil {
		t.Fatal(err)
	}
	ids, err := db.PendingDeliveryIDs(ctx, 0, 10)
	if err != nil || len(ids) != 1 || ids[0] != id {
		t.Fatalf("pending ids %v %v", ids, err)
	}
	delivery, err := db.GetDelivery(ctx, id)
	if err != nil || string(delivery.Files) != "[]" || delivery.BodyMD != "" || delivery.Title != "Diff" {
		t.Fatalf("delivery projection %+v %v", delivery, err)
	}
	full, err := db.GetQuestion(ctx, id)
	if err != nil || len(full.Files) < 2*len(big) || full.FileCount != 2 {
		t.Fatalf("the detail lost its files: %d bytes, count %d, %v", len(full.Files), full.FileCount, err)
	}
}
