package main

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestManualPrice(t *testing.T) {
	st, err := OpenStore(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	id, _ := st.AddItem("Widget", "AUD", nil)
	st.UpsertListing(id, "https://blocked.example/p", "Blocked", "Widget", 0, "AUD", "", 0)
	st.UpsertListing(id, "https://ok.example/p", "Ok", "Widget", 120, "AUD", "", 0)
	st.MarkListingError(id, "https://blocked.example/p", "HTTP 403")

	ls, _ := st.ListListings(id)
	var blocked *Listing
	for _, l := range ls {
		if l.Retailer == "Blocked" {
			blocked = l
		}
	}
	if blocked == nil || blocked.HasPrice || blocked.LastError != "HTTP 403" {
		t.Fatalf("blocked listing: %+v", blocked)
	}

	// user checks the page by hand and enters a lower price
	if err := st.SetManualPrice(blocked.ID, 99.5, "AUD"); err != nil {
		t.Fatal(err)
	}
	st.RecordBest(id)
	ls, _ = st.ListListings(id)
	if b := bestOf("AUD", ls); b == nil || b.Retailer != "Blocked" || b.Price != 99.5 || !b.Manual {
		t.Fatalf("best should be the manual price: %+v", b)
	}

	// a failed automatic re-check keeps the manual price
	st.UpdateListing(blocked.ID, nil, errors.New("HTTP 403"))
	l, _ := st.GetListing(blocked.ID)
	if !l.HasPrice || !l.Manual {
		t.Fatalf("manual price lost after failed check: %+v", l)
	}

	// a successful automatic read replaces it
	st.UpdateListing(blocked.ID, &Result{Price: 95, Currency: "AUD"}, nil)
	if l, _ = st.GetListing(blocked.ID); l.Manual || l.Price != 95 {
		t.Fatalf("auto price should replace manual: %+v", l)
	}

	// clearing a manual price
	st.SetManualPrice(blocked.ID, 80, "AUD")
	st.SetManualPrice(blocked.ID, 0, "AUD")
	if l, _ = st.GetListing(blocked.ID); l.HasPrice {
		t.Fatalf("manual price should be cleared: %+v", l)
	}
}
