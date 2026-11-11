package main

import "testing"

func TestReviewCommandNeedsOnlyRepositoryPath(t *testing.T) {
	inv, err := parseInvocation([]string{"review", "/repo"})
	if err != nil || !inv.review || inv.dir != "/repo" {
		t.Fatalf("single review command: %+v %v", inv, err)
	}
	if _, err := parseInvocation([]string{"review"}); err == nil {
		t.Fatal("missing review target accepted")
	}
	if _, err := parseInvocation([]string{"review", "/repo", "/other"}); err == nil {
		t.Fatal("extra repository silently ignored")
	}
	inv, err = parseInvocation([]string{"-dir", "/repo"})
	if err != nil || inv.review || inv.dir != "/repo" {
		t.Fatal("static browsing must remain non-executing")
	}
	inv, err = parseInvocation([]string{"open", "/retained/review.json"})
	if err != nil || inv.saved != "/retained/review.json" || inv.review {
		t.Fatal("reopening a report must not rerun repository code")
	}
}
