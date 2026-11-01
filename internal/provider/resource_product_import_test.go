// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestProductAdoptionPreservesTypedMetadataAndPrices(t *testing.T) {
	testProductJSONLifecycle(t, true)
}

func TestProductCreationPreservesTypedMetadataAndPrices(t *testing.T) {
	testProductJSONLifecycle(t, false)
}

func testProductJSONLifecycle(t *testing.T, importing bool) {
	const id = "11111111-1111-4111-8111-111111111111"
	const meter = "22222222-2222-4222-8222-222222222222"
	const metadata = `{"cap":2900,"divisor":730,"enabled":true,"large":9007199254740993,"ratio":1.25,"size":"small"}`
	var mu sync.Mutex
	visibility := "draft"
	archived := false
	created := importing
	updates := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/v1/products/"+id && r.URL.Path != "/v1/products/" && r.URL.Path != "/v1/products" {
			t.Errorf("unexpected path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Method == http.MethodPost || r.Method == http.MethodPatch {
			var body map[string]json.RawMessage
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode: %v", err)
				w.WriteHeader(400)
				return
			}
			if string(body["is_archived"]) == "true" {
				archived = true
			} else {
				if r.Method == http.MethodPost {
					if created {
						t.Error("attempted duplicate product creation")
					}
					created = true
				} else {
					updates++
				}
				if !reflect.DeepEqual(compactJSON(body["metadata"]), compactJSON([]byte(metadata))) {
					t.Errorf("metadata types/precision changed: %s", body["metadata"])
				}
				var prices []map[string]json.RawMessage
				_ = json.Unmarshal(body["prices"], &prices)
				if len(prices) != 2 {
					t.Errorf("expected two prices: %s", body["prices"])
				} else if r.Method == http.MethodPatch {
					if string(prices[0]["id"]) != `"fixed-id"` || string(prices[1]["id"]) != `"metered-id"` {
						t.Errorf("unchanged prices recreated/archived: %s", body["prices"])
					}
				} else {
					for _, price := range prices {
						if string(price["tax_behavior"]) != `"exclusive"` {
							t.Errorf("tax lost: %s", body["prices"])
						}
					}
					if string(prices[1]["cap_amount"]) != "2900" {
						t.Errorf("cap lost: %s", body["prices"])
					}
					if string(body["recurring_interval_count"]) != "1" {
						t.Errorf("interval count lost: %s", body["recurring_interval_count"])
					}
				}
				if v, ok := body["visibility"]; ok {
					_ = json.Unmarshal(v, &visibility)
				}
			}
		} else if r.Method != http.MethodGet {
			t.Errorf("unexpected method %s", r.Method)
			w.WriteHeader(405)
			return
		}
		if r.Method == http.MethodPost {
			w.WriteHeader(http.StatusCreated)
		}
		_, _ = fmt.Fprintf(w, `{"id":%q,"created_at":"2026-01-01T00:00:00Z","modified_at":"2026-01-01T00:00:00Z","name":"Example","description":null,"visibility":%q,"recurring_interval":"month","recurring_interval_count":1,"is_recurring":true,"is_archived":%t,"organization_id":"fake-org","metadata":%s,"trial_interval":null,"trial_interval_count":null,"benefits":[],"medias":[],"attached_custom_fields":[],"prices":[
  {"id":"archived-id","source":"catalog","created_at":"2026-01-01T00:00:00Z","modified_at":null,"amount_type":"fixed","price_currency":"eur","price_amount":0,"tax_behavior":"exclusive","is_archived":true,"product_id":%q},
  {"id":"fixed-id","source":"catalog","created_at":"2026-01-01T00:00:00Z","modified_at":null,"amount_type":"fixed","price_currency":"eur","price_amount":0,"tax_behavior":"exclusive","is_archived":false,"product_id":%q},
  {"id":"metered-id","source":"catalog","created_at":"2026-01-01T00:00:00Z","modified_at":null,"amount_type":"metered_unit","price_currency":"eur","unit_amount":"1.000000000000","cap_amount":2900,"meter_id":%q,"meter":{"id":%q,"name":"Usage"},"tax_behavior":"exclusive","is_archived":false,"product_id":%q}
  ]}`, id, visibility, archived, metadata, id, id, meter, meter, id)
	}))
	defer server.Close()
	config := func(v string) string {
		return fmt.Sprintf(`
provider "polar" {
 access_token = "fake-token"
 server = "sandbox"
}
resource "polar_product" "example" {
 name = "Example"
 visibility = %q
 recurring_interval = "month"
 recurring_interval_count = 1
 metadata_json = %q
 prices = [
 { amount_type = "fixed", price_amount = 0, price_currency = "eur", tax_behavior = "exclusive" },
 { amount_type = "metered_unit", meter_id = %q, unit_amount = "1", cap_amount = 2900, price_currency = "eur", tax_behavior = "exclusive" }
 ]
}
`, v, metadata, meter)
	}
	initial := config("draft")
	if importing {
		initial += fmt.Sprintf("\nimport {\n to = polar_product.example\n id = %q\n}\n", id)
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: fakePolarProviderFactories(server),
		Steps: []resource.TestStep{
			{Config: initial, Check: resource.TestCheckResourceAttr("polar_product.example", "metadata_json", metadata)},
			{Config: config("draft"), PlanOnly: true},
			{Config: config("private")},
			{Config: config("private"), PlanOnly: true},
		},
	})
	mu.Lock()
	defer mu.Unlock()
	if updates != 1 {
		t.Errorf("expected only visibility update; got %d product mutations", updates)
	}
}

func compactJSON(raw []byte) string {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	result, _ := json.Marshal(value)
	return string(result)
}
