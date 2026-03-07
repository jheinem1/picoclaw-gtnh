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

func TestRenderBoardMessage_FieldNames(t *testing.T) {
	cfg := BoardConfig{Title: "GTNH Kanban", MaxItemsPerColumn: 10}
	board := BoardPayload{}
	msg := renderBoardMessage(cfg, board)
	if len(msg.Embeds) != 1 || len(msg.Embeds[0].Fields) != 4 {
		t.Fatalf("unexpected embed shape: %#v", msg)
	}
	if msg.Embeds[0].Fields[0].Name != "Backlog (0)" {
		t.Fatalf("unexpected field name: %q", msg.Embeds[0].Fields[0].Name)
	}
}

func TestFormatStatusUpdates_Empty(t *testing.T) {
	got := formatStatusUpdates(nil, 8)
	if got != "No status updates yet." {
		t.Fatalf("unexpected placeholder: %q", got)
	}
}

func TestFormatStatusUpdates_UsesMostRecentFirst(t *testing.T) {
	updates := []StatusUpdate{
		{Timestamp: "2026-03-06T00:00:00Z", Text: "first"},
		{Timestamp: "2026-03-06T00:10:00Z", Text: "second"},
		{Timestamp: "2026-03-06T00:20:00Z", Text: "third"},
	}
	got := formatStatusUpdates(updates, 2)
	if !strings.Contains(got, "third") || !strings.Contains(got, "second") {
		t.Fatalf("expected most recent updates, got %q", got)
	}
	if strings.Contains(got, "first") {
		t.Fatalf("expected oldest update to be capped out, got %q", got)
	}
}

func TestRenderInProgressMessage_IncludesDescriptionAndPlaceholder(t *testing.T) {
	cfg := InProgressConfig{MaxUpdates: 8}
	msg := renderInProgressMessage(cfg, InProgressTask{
		ID:          7,
		Title:       "Build quad purifier",
		Owner:       "exx",
		Priority:    "high",
		Area:        "steam",
		UpdatedAt:   "2026-03-06T01:00:00Z",
		Description: "Needs heater count and steam budget.",
	})
	if len(msg.Embeds) != 1 {
		t.Fatalf("unexpected embed count: %#v", msg)
	}
	embed := msg.Embeds[0]
	if embed.Description != "Needs heater count and steam budget." {
		t.Fatalf("unexpected description: %q", embed.Description)
	}
	if len(embed.Fields) < 5 {
		t.Fatalf("expected metadata + updates fields, got %#v", embed.Fields)
	}
	if embed.Fields[4].Name != "Status Updates" {
		t.Fatalf("unexpected updates field: %#v", embed.Fields)
	}
	if embed.Fields[4].Value != "No status updates yet." {
		t.Fatalf("unexpected updates placeholder: %q", embed.Fields[4].Value)
	}
}
