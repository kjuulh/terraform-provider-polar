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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

const (
	fakeDiscountID             = "11111111-1111-4111-8111-111111111111"
	fakeDiscountProductID      = "22222222-2222-4222-8222-222222222222"
	fakeOfferOrganizationID    = "33333333-3333-4333-8333-333333333333"
	fakeDiscountOrganizationID = "55555555-5555-4555-8555-555555555555"
)

func TestDiscountResource_importPreservesRentalContract(t *testing.T) {
	for _, startsAt := range []string{"null", `"2025-01-01T00:00:00Z"`} {
		t.Run(startsAt, func(t *testing.T) {
			server, api := newDiscountContractServer(t, startsAt, true, true)
			defer server.Close()
			initial := testDiscountContractConfig("Rental credit", startsAt, "1", `["`+fakeDiscountProductID+`"]`)
			updated := testDiscountContractConfig("Rental credit updated", startsAt, "2", `["`+fakeDiscountProductID+`"]`)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: fakePolarProviderFactories(server),
				Steps: []resource.TestStep{
					{
						Config:             initial,
						ResourceName:       "polar_discount.rental",
						ImportState:        true,
						ImportStateId:      fakeDiscountID,
						ImportStatePersist: true,
						ImportStateCheck: func(states []*terraform.InstanceState) error {
							if len(states) != 1 {
								return fmt.Errorf("expected one imported discount, got %d", len(states))
							}
							attributes := states[0].Attributes
							for key, want := range map[string]string{
								"id":                           fakeDiscountID,
								"type":                         "percentage",
								"duration":                     "forever",
								"basis_points":                 "10000",
								"max_redemptions":              "1",
								"max_redemptions_per_customer": "1",
								"products.#":                   "1",
								"products.0":                   fakeDiscountProductID,
								"metadata.%":                   "1",
								"metadata.organization_id":     fakeOfferOrganizationID,
								"code":                         "",
								"ends_at":                      "",
							} {
								if got := attributes[key]; got != want {
									return fmt.Errorf("imported %s = %q, want %q", key, got, want)
								}
							}
							return nil
						},
					},
					{Config: initial, PlanOnly: true},
					{Config: updated, Check: testDiscountContractState("Rental credit updated", "2", true)},
					{Config: updated, PlanOnly: true},
				},
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 0 || len(api.updates) != 1 {
				t.Fatalf("adoption must only update once: creates=%d updates=%d", api.creates, len(api.updates))
			}
			assertDiscountWrite(t, api.updates[0], "Rental credit updated", float64(2), true)
		})
	}
}

func TestDiscountResource_createAndRemovePerCustomerLimit(t *testing.T) {
	server, api := newDiscountContractServer(t, "null", false, true)
	defer server.Close()
	initial := testDiscountContractConfig("Rental credit", "null", "1", `["`+fakeDiscountProductID+`"]`)
	unlimited := testDiscountContractConfig("Rental credit", "null", "null", `["`+fakeDiscountProductID+`"]`)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: fakePolarProviderFactories(server),
		Steps: []resource.TestStep{
			{Config: initial, Check: testDiscountContractState("Rental credit", "1", true)},
			{ResourceName: "polar_discount.rental", ImportState: true, ImportStateVerify: true},
			{Config: initial, PlanOnly: true},
			{Config: unlimited, Check: resource.ComposeAggregateTestCheckFunc(
				testDiscountContractState("Rental credit", "", true),
				resource.TestCheckNoResourceAttr("polar_discount.rental", "max_redemptions_per_customer"),
			)},
			{Config: unlimited, PlanOnly: true},
		},
	})
	api.mu.Lock()
	defer api.mu.Unlock()
	if api.creates != 1 || len(api.updates) != 1 {
		t.Fatalf("expected one creation and one update, got %d and %d", api.creates, len(api.updates))
	}
	assertDiscountWrite(t, api.creation, "Rental credit", float64(1), true)
	assertDiscountWrite(t, api.updates[0], "Rental credit", nil, true)
	if _, present := api.updates[0]["max_redemptions_per_customer"]; !present {
		t.Fatal("removing the per-customer limit must send an explicit null")
	}
}

func TestDiscountResource_unrestrictedProductsRemainUnrestricted(t *testing.T) {
	for _, products := range []string{"null", "[]"} {
		t.Run(products, func(t *testing.T) {
			server, api := newDiscountContractServer(t, "null", false, false)
			defer server.Close()
			initial := testDiscountContractConfig("Rental credit", "null", "null", products)
			updated := testDiscountContractConfig("Unrestricted updated", "null", "null", products)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: fakePolarProviderFactories(server),
				Steps: []resource.TestStep{
					{Config: initial, Check: testDiscountContractState("Rental credit", "", false)},
					{Config: initial, PlanOnly: true},
					{Config: updated, Check: testDiscountContractState("Unrestricted updated", "", false)},
					{Config: updated, PlanOnly: true},
				},
			})
			api.mu.Lock()
			defer api.mu.Unlock()
			if api.creates != 1 || len(api.updates) != 1 {
				t.Fatalf("expected one creation and one update, got %d and %d", api.creates, len(api.updates))
			}
			assertDiscountWrite(t, api.creation, "Rental credit", nil, false)
			assertDiscountWrite(t, api.updates[0], "Unrestricted updated", nil, false)
		})
	}
}

func testDiscountContractState(name, limit string, scoped bool) resource.TestCheckFunc {
	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttr("polar_discount.rental", "id", fakeDiscountID),
		resource.TestCheckResourceAttr("polar_discount.rental", "name", name),
		resource.TestCheckResourceAttr("polar_discount.rental", "type", "percentage"),
		resource.TestCheckResourceAttr("polar_discount.rental", "duration", "forever"),
		resource.TestCheckResourceAttr("polar_discount.rental", "basis_points", "10000"),
		resource.TestCheckResourceAttr("polar_discount.rental", "max_redemptions", "1"),
		resource.TestCheckResourceAttr("polar_discount.rental", "metadata.organization_id", fakeOfferOrganizationID),
		resource.TestCheckNoResourceAttr("polar_discount.rental", "code"),
		resource.TestCheckNoResourceAttr("polar_discount.rental", "ends_at"),
	}
	if limit != "" {
		checks = append(checks, resource.TestCheckResourceAttr("polar_discount.rental", "max_redemptions_per_customer", limit))
	}
	if scoped {
		checks = append(checks,
			resource.TestCheckResourceAttr("polar_discount.rental", "products.#", "1"),
			resource.TestCheckResourceAttr("polar_discount.rental", "products.0", fakeDiscountProductID),
		)
	}
	return resource.ComposeAggregateTestCheckFunc(checks...)
}

func testDiscountContractConfig(name, startsAt, limit, products string) string {
	return fmt.Sprintf(`
provider "polar" {
  access_token = "fake-token"
  server       = "sandbox"
}
resource "polar_discount" "rental" {
  name                         = %q
  type                         = "percentage"
  duration                     = "forever"
  basis_points                 = 10000
  code                         = null
  starts_at                    = %s
  ends_at                      = null
  max_redemptions               = 1
  max_redemptions_per_customer  = %s
  products                     = %s
  metadata = {
    organization_id = %q
  }
}
`, name, startsAt, limit, products, fakeOfferOrganizationID)
}

type discountContractAPI struct {
	mu       sync.Mutex
	state    map[string]any
	exists   bool
	creates  int
	creation map[string]any
	updates  []map[string]any
}

func newDiscountContractServer(t *testing.T, startsAt string, exists, scoped bool) (*httptest.Server, *discountContractAPI) {
	t.Helper()
	var start any
	if err := json.Unmarshal([]byte(startsAt), &start); err != nil {
		t.Fatal(err)
	}
	products := []any{}
	if scoped {
		products = append(products, map[string]any{
			"id":              fakeDiscountProductID,
			"created_at":      "2025-01-01T00:00:00Z",
			"modified_at":     nil,
			"metadata":        map[string]any{},
			"name":            "Rental product",
			"visibility":      "public",
			"is_recurring":    true,
			"is_archived":     false,
			"organization_id": fakeDiscountOrganizationID,
		})
	}
	api := &discountContractAPI{exists: exists, state: map[string]any{
		"id":                           fakeDiscountID,
		"created_at":                   "2025-01-01T00:00:00Z",
		"modified_at":                  "2025-01-01T00:00:00Z",
		"name":                         "Rental credit",
		"type":                         "percentage",
		"duration":                     "forever",
		"basis_points":                 10000,
		"code":                         nil,
		"starts_at":                    start,
		"ends_at":                      nil,
		"max_redemptions":              1,
		"max_redemptions_per_customer": 1,
		"redemptions_count":            0,
		"organization_id":              fakeDiscountOrganizationID,
		"products":                     products,
		"metadata":                     map[string]any{"organization_id": fakeOfferOrganizationID},
	}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		api.mu.Lock()
		defer api.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if req.Header.Get("Authorization") != "Bearer fake-token" {
			t.Errorf("unexpected authorization header: %q", req.Header.Get("Authorization"))
			http.Error(w, `{"detail":"unauthorized"}`, http.StatusUnauthorized)
			return
		}
		collection := req.URL.Path == "/v1/discounts"
		if !collection && req.URL.Path != "/v1/discounts/"+fakeDiscountID {
			t.Errorf("unexpected request: %s %s", req.Method, req.URL.Path)
			http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
			return
		}
		if req.Method == http.MethodDelete && !collection {
			api.exists = false
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if req.Method == http.MethodGet && !collection {
			if !api.exists {
				http.Error(w, `{"detail":"not found"}`, http.StatusNotFound)
				return
			}
		} else if (req.Method == http.MethodPost && collection) || (req.Method == http.MethodPatch && !collection) {
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Errorf("decoding discount write: %v", err)
				http.Error(w, `{"detail":"bad request"}`, http.StatusBadRequest)
				return
			}
			if req.Method == http.MethodPost {
				api.creates++
				api.creation = payload
				api.exists = true
				// Omission on creation means unlimited, not the fixture's initial limit.
				api.state["max_redemptions_per_customer"] = nil
			} else {
				api.updates = append(api.updates, payload)
			}
			for key, value := range payload {
				if key != "products" {
					api.state[key] = value
					continue
				}
				ids, ok := value.([]any)
				if !ok || (len(ids) > 0 && !reflect.DeepEqual(ids, []any{fakeDiscountProductID})) {
					t.Errorf("unexpected product restriction: %#v", value)
					http.Error(w, `{"detail":"bad products"}`, http.StatusBadRequest)
					return
				}
				if len(ids) == 0 {
					api.state[key] = []any{}
				}
			}
			api.state["modified_at"] = "2025-01-02T00:00:00Z"
		} else {
			t.Errorf("unexpected discount method: %s %s", req.Method, req.URL.Path)
			http.Error(w, `{"detail":"method not allowed"}`, http.StatusMethodNotAllowed)
			return
		}
		if err := json.NewEncoder(w).Encode(api.state); err != nil {
			t.Errorf("encoding discount response: %v", err)
		}
	}))
	return server, api
}

func assertDiscountWrite(t *testing.T, payload map[string]any, name string, limit any, scoped bool) {
	t.Helper()
	for key, want := range map[string]any{
		"name":                         name,
		"basis_points":                 float64(10000),
		"max_redemptions":              float64(1),
		"max_redemptions_per_customer": limit,
		"metadata":                     map[string]any{"organization_id": fakeOfferOrganizationID},
	} {
		if got := payload[key]; !reflect.DeepEqual(got, want) {
			t.Errorf("discount write %s = %#v, want %#v", key, got, want)
		}
	}
	if scoped {
		if !reflect.DeepEqual(payload["products"], []any{fakeDiscountProductID}) {
			t.Errorf("discount write lost product scope: %#v", payload["products"])
		}
	} else if value, present := payload["products"]; present && !reflect.DeepEqual(value, []any{}) {
		t.Errorf("unrestricted discount received product scope: %#v", value)
	}
	for _, key := range []string{"code", "ends_at"} {
		if value := payload[key]; value != nil {
			t.Errorf("discount write unexpectedly set %s: %#v", key, value)
		}
	}
}
