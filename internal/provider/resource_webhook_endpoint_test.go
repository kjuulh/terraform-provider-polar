// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"
)

func TestWebhookEndpointRead_preservesKnownAndFutureEvents(t *testing.T) {
	const endpointID = "endpoint-id"
	const body = `{"id":"endpoint-id","url":"https://example.com/webhook","format":"raw","secret":"test-secret","enabled":true,"created_at":"2026-10-13T00:00:00Z","organization_id":"test-org","events":["order.created","subscription.cycled","subscription.future_event"]}`
	updatedEvents := make(chan []string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/webhooks/endpoints/"+endpointID {
			t.Errorf("unexpected webhook request: %s %s", req.Method, req.URL.Path)
			http.NotFound(w, req)
			return
		}
		if got := req.Header.Get("Authorization"); got != "Bearer test-token" {
			t.Errorf("unexpected authorization header: %q", got)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if req.Method == http.MethodPatch {
			var payload struct {
				Events []string `json:"events"`
			}
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
				t.Errorf("decoding webhook update: %v", err)
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			updatedEvents <- payload.Events
		} else if req.Method != http.MethodGet {
			t.Errorf("unexpected webhook method: %s", req.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()

	ctx := context.Background()
	r, ok := NewWebhookEndpointResource().(*WebhookEndpointResource)
	if !ok {
		t.Fatal("unexpected webhook resource type")
	}
	r.webhooks = &webhookEndpointHTTP{baseURL: server.URL, token: "test-token", client: server.Client()}
	var schemaResp fwresource.SchemaResponse
	r.Schema(ctx, fwresource.SchemaRequest{}, &schemaResp)
	state := tfsdk.State{Schema: schemaResp.Schema}
	if diags := state.Set(ctx, &WebhookEndpointResourceModel{ID: types.StringValue(endpointID), Events: types.SetNull(types.StringType)}); diags.HasError() {
		t.Fatalf("initializing state: %v", diags)
	}

	expected := []string{"order.created", "subscription.cycled", "subscription.future_event"}
	sort.Strings(expected)
	for range 2 {
		resp := fwresource.ReadResponse{State: state}
		r.Read(ctx, fwresource.ReadRequest{State: state}, &resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("reading endpoint: %v", resp.Diagnostics)
		}
		var model WebhookEndpointResourceModel
		if diags := resp.State.Get(ctx, &model); diags.HasError() {
			t.Fatalf("decoding state: %v", diags)
		}
		if model.ID.ValueString() != endpointID || model.URL.ValueString() != "https://example.com/webhook" {
			t.Fatalf("endpoint fields changed during read: id=%q url=%q", model.ID.ValueString(), model.URL.ValueString())
		}
		var actual []string
		if diags := model.Events.ElementsAs(ctx, &actual, false); diags.HasError() {
			t.Fatalf("decoding events: %v", diags)
		}
		sort.Strings(actual)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatalf("event set changed: got %q, want %q", actual, expected)
		}
		state = resp.State
	}
	var model WebhookEndpointResourceModel
	if diags := state.Get(ctx, &model); diags.HasError() {
		t.Fatalf("decoding plan: %v", diags)
	}
	plan := tfsdk.Plan{Schema: schemaResp.Schema}
	if diags := plan.Set(ctx, &model); diags.HasError() {
		t.Fatalf("setting plan: %v", diags)
	}
	update := fwresource.UpdateResponse{State: state}
	r.Update(ctx, fwresource.UpdateRequest{Plan: plan, State: state}, &update)
	if update.Diagnostics.HasError() {
		t.Fatalf("updating endpoint: %v", update.Diagnostics)
	}
	sent := <-updatedEvents
	sort.Strings(sent)
	if !reflect.DeepEqual(sent, expected) {
		t.Fatalf("update dropped events: got %q, want %q", sent, expected)
	}
	if diags := update.State.Get(ctx, &model); diags.HasError() {
		t.Fatalf("decoding updated state: %v", diags)
	}
	var actual []string
	if diags := model.Events.ElementsAs(ctx, &actual, false); diags.HasError() {
		t.Fatalf("decoding updated events: %v", diags)
	}
	sort.Strings(actual)
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("updated state dropped events: got %q, want %q", actual, expected)
	}
}

func TestWebhookEndpointResource_importRefreshPreservesUnknownEvents(t *testing.T) {
	const endpointID = "33333333-3333-4333-8333-333333333333"
	const body = `{"id":"33333333-3333-4333-8333-333333333333","url":"https://example.com/webhook","format":"raw","secret":"fake-webhook-secret","enabled":true,"created_at":"2026-10-13T00:00:00Z","modified_at":null,"organization_id":"22222222-2222-4222-8222-222222222222","events":["order.created","subscription.cycled","subscription.future_event"]}`
	var reads, deletes, forbidden atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/webhooks/endpoints/"+endpointID || req.Header.Get("Authorization") != "Bearer fake-token" {
			forbidden.Add(1)
			t.Errorf("unexpected webhook request: %s %s", req.Method, req.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case http.MethodGet:
			reads.Add(1)
			if deletes.Load() != 0 {
				http.NotFound(w, req)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprint(w, body)
		case http.MethodDelete:
			// The acceptance harness destroys the fixture after all steps.
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			forbidden.Add(1)
			t.Errorf("adoption must not issue %s", req.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	const config = `
provider "polar" {
  access_token = "fake-token"
  server       = "sandbox"
}
resource "polar_webhook_endpoint" "adopted" {
  url     = "https://example.com/webhook"
  format  = "raw"
  enabled = true
  events  = ["order.created", "subscription.cycled", "subscription.future_event"]
}
`
	readOnly := func() error {
		if forbidden.Load() != 0 || deletes.Load() != 0 {
			return fmt.Errorf("import/refresh mutated fixture: forbidden=%d deletes=%d", forbidden.Load(), deletes.Load())
		}
		return nil
	}
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: fakePolarProviderFactories(server),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "polar_webhook_endpoint.adopted",
				ImportState:        true,
				ImportStateId:      endpointID,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].ID != endpointID {
						return fmt.Errorf("expected only imported endpoint %s, got %#v", endpointID, states)
					}
					events := make(map[string]bool)
					for key, value := range states[0].Attributes {
						if strings.HasPrefix(key, "events.") && key != "events.#" {
							events[value] = true
						}
					}
					if len(events) != 3 || !events["order.created"] || !events["subscription.cycled"] || !events["subscription.future_event"] {
						return fmt.Errorf("import changed webhook events: %#v", events)
					}
					return readOnly()
				},
			},
			{
				RefreshState: true,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "id", endpointID),
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "url", "https://example.com/webhook"),
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "format", "raw"),
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "enabled", "true"),
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "secret", "fake-webhook-secret"),
					resource.TestCheckResourceAttr("polar_webhook_endpoint.adopted", "events.#", "3"),
					resource.TestCheckTypeSetElemAttr("polar_webhook_endpoint.adopted", "events.*", "order.created"),
					resource.TestCheckTypeSetElemAttr("polar_webhook_endpoint.adopted", "events.*", "subscription.cycled"),
					resource.TestCheckTypeSetElemAttr("polar_webhook_endpoint.adopted", "events.*", "subscription.future_event"),
					func(_ *terraform.State) error { return readOnly() },
				),
			},
			{
				Config:   config,
				PlanOnly: true,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PostApplyPreRefresh:  []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
			},
		},
	})
	if reads.Load() < 2 || forbidden.Load() != 0 || deletes.Load() != 1 {
		t.Errorf("expected import/refresh GETs and one cleanup DELETE only: reads=%d forbidden=%d deletes=%d", reads.Load(), forbidden.Load(), deletes.Load())
	}
}

func TestAccWebhookEndpointResource_basic(t *testing.T) {
	rSuffix := acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	createURL := fmt.Sprintf("https://example.com/webhook/tf-acc-%s", rSuffix)
	updateURL := fmt.Sprintf("https://example.com/webhook/tf-acc-%s-updated", rSuffix)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccWebhookEndpointConfig(createURL, "raw", `["order.created"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("url"),
						knownvalue.StringExact(createURL),
					),
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("format"),
						knownvalue.StringExact("raw"),
					),
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("enabled"),
						knownvalue.Bool(true),
					),
				},
			},
			// ImportState
			{
				ResourceName:            "polar_webhook_endpoint.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret"},
			},
			// Update URL and events
			{
				Config: testAccWebhookEndpointConfig(updateURL, "raw", `["order.created", "subscription.created"]`),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("url"),
						knownvalue.StringExact(updateURL),
					),
				},
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccWebhookEndpointResource_disabled(t *testing.T) {
	rSuffix := acctest.RandStringFromCharSet(8, acctest.CharSetAlphaNum)
	webhookURL := fmt.Sprintf("https://example.com/webhook/tf-acc-%s-disabled", rSuffix)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create disabled endpoint
			{
				Config: testAccWebhookEndpointConfigWithEnabled(webhookURL, "raw", `["order.created"]`, false),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("enabled"),
						knownvalue.Bool(false),
					),
				},
			},
			// Enable it
			{
				Config: testAccWebhookEndpointConfigWithEnabled(webhookURL, "raw", `["order.created"]`, true),
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue(
						"polar_webhook_endpoint.test",
						tfjsonpath.New("enabled"),
						knownvalue.Bool(true),
					),
				},
			},
		},
	})
}

func TestWebhookEndpointResource_rejectsHTTP(t *testing.T) {
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccWebhookEndpointConfig("http://example.com/webhook", "raw", `["order.created"]`),
				ExpectError: regexp.MustCompile(`webhook URL must use HTTPS`),
			},
		},
	})
}

func testAccWebhookEndpointConfig(url, format, events string) string {
	return fmt.Sprintf(`
resource "polar_webhook_endpoint" "test" {
  url    = %[1]q
  format = %[2]q
  events = %[3]s
}
`, url, format, events)
}

func testAccWebhookEndpointConfigWithEnabled(url, format, events string, enabled bool) string {
	return fmt.Sprintf(`
resource "polar_webhook_endpoint" "test" {
  url     = %[1]q
  format  = %[2]q
  events  = %[3]s
  enabled = %[4]t
}
`, url, format, events, enabled)
}
