package github

import (
	"crypto/rand"
	"encoding/json/v2"
	"net/http"
	"testing"
)

func TestListsSelectedSkipsUnchangedAndUnselectedItems(t *testing.T) {
	token, err := NewToken(rand.Text(), nil)
	if err != nil {
		t.Fatal(err)
	}
	itemCalls := 0
	client, err := NewListsClient(token, &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		var query graphQuery
		if err := json.UnmarshalRead(request.Body, &query); err != nil {
			t.Fatal(err)
		}
		if query.Variables.ID == "" {
			return listPageResponse("first", "", false), nil
		}
		itemCalls++
		return itemPageResponse("first", "", false, 0, []map[string]any{}), nil
	})})
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.ListsSelected(t.Context(), map[string]List{"id-first": {}})
	if err != nil || itemCalls != 1 || len(first.Lists) != 1 {
		t.Fatalf("initial selected list: items=%d, error=%v", itemCalls, err)
	}
	unchanged, err := client.ListsSelected(t.Context(), map[string]List{"id-first": first.Lists[0]})
	if err != nil || itemCalls != 1 || len(unchanged.Lists) != 1 {
		t.Fatalf("unchanged list must skip items: items=%d, error=%v", itemCalls, err)
	}
	if _, err := client.ListsSelected(t.Context(), map[string]List{}); err != nil || itemCalls != 1 {
		t.Fatalf("unselected list must skip items: items=%d, error=%v", itemCalls, err)
	}
	changed := first.Lists[0]
	changed.LastAddedAt = changed.LastAddedAt.Add(-1)
	if _, err := client.ListsSelected(t.Context(), map[string]List{"id-first": changed}); err != nil || itemCalls != 2 {
		t.Fatalf("changed watermark must fetch items: items=%d, error=%v", itemCalls, err)
	}
	if _, err := client.ListsSelected(t.Context(), nil); err != nil || itemCalls != 2 {
		t.Fatalf("nil selection must discover only: items=%d, error=%v", itemCalls, err)
	}
}
