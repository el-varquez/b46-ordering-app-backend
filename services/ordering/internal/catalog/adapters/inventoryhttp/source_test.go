package inventoryhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProductsMapsPrivateCatalogContract(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer service-token" || request.URL.Query().Get("limit") != "25" || request.URL.Query().Get("after_id") != "cursor" {
			t.Fatalf("request = %#v", request)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"products":[{"product_id":"b4600000-0000-4000-8001-000000000001","name":"Coke","description":"Cold","price_centavos":8200,"category_id":"b4600000-0000-4000-8002-000000000001","category_name":"Drinks","available":true,"source_updated_at":"2026-09-22T00:00:00Z"}],"next_after_id":"next"}`))
	}))
	defer server.Close()
	source, err := New(server.Client(), server.URL, "service-token", 4096)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	page, err := source.Products(context.Background(), 25, "cursor")
	if err != nil {
		t.Fatalf("Products() error = %v", err)
	}
	if len(page.Products) != 1 || page.Products[0].PriceCentavos != 8200 || page.NextAfterID != "next" {
		t.Fatalf("page = %#v", page)
	}
}

func TestProductsRejectsDriftAndOversizedResponses(t *testing.T) {
	for name, response := range map[string]string{
		"unknown field": `{"products":[],"next_after_id":"","unknown":true}`,
		"oversized":     `{"products":[],"next_after_id":""}`,
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte(response)) }))
			defer server.Close()
			limit := int64(4096)
			if name == "oversized" {
				limit = 8
			}
			source, _ := New(server.Client(), server.URL, "service-token", limit)
			if _, err := source.Products(context.Background(), 25, ""); err == nil {
				t.Fatal("Products() succeeded")
			}
		})
	}
}
