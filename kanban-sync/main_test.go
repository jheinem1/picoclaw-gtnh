package main

import (
	"strings"
	"testing"
)

func TestColumnText_Empty(t *testing.T) {
	got := columnText(nil, 10)
	if !strings.Contains(got, "(none)") {
		t.Fatalf("expected empty marker, got %q", got)
	}
}

func TestColumnText_Truncates(t *testing.T) {
	tasks := make([]BoardTask, 0, 20)
	for i := 1; i <= 20; i++ {
		tasks = append(tasks, BoardTask{ID: i, Title: "A very long task name for truncation test", Priority: "med"})
	}
	got := columnText(tasks, 5)
	if !strings.Contains(got, "+15 more") {
		t.Fatalf("expected overflow marker, got %q", got)
	}
	if len(got) > 1024 {
		t.Fatalf("expected field <=1024 chars, got %d", len(got))
	}
}

func TestRenderMessage_FieldNames(t *testing.T) {
	cfg := Config{Title: "GTNH Kanban", MaxItemsPerColumn: 10}
	board := BoardPayload{}
	msg := renderMessage(cfg, board)
	if len(msg.Embeds) != 1 || len(msg.Embeds[0].Fields) != 4 {
		t.Fatalf("unexpected embed shape: %#v", msg)
	}
	if msg.Embeds[0].Fields[0].Name != "Backlog (0)" {
		t.Fatalf("unexpected field name: %q", msg.Embeds[0].Fields[0].Name)
	}
}
