// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	fwprovider "github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	polargo "github.com/polarsource/polar-go"
)

// Keep production configuration and resource implementations, replacing only
// their HTTP connections. Even an unexpected redirect cannot reach a real API.
func fakePolarProviderFactories(server *httptest.Server) map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"polar": func() (tfprotov6.ProviderServer, error) {
			return providerserver.NewProtocol6WithError(&fakePolarProvider{
				Provider: New("test")(),
				server:   server,
			})()
		},
	}
}

type fakePolarProvider struct {
	fwprovider.Provider
	server *httptest.Server
}

func (p *fakePolarProvider) Configure(ctx context.Context, req fwprovider.ConfigureRequest, resp *fwprovider.ConfigureResponse) {
	p.Provider.Configure(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		return
	}
	client := *p.server.Client()
	client.Transport = fakePolarTransport{origin: p.server.URL, transport: client.Transport}
	data, ok := resp.ResourceData.(*PolarProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected provider data", fmt.Sprintf("Expected PolarProviderData, got %T", resp.ResourceData))
		return
	}
	data.Client = polargo.New(
		polargo.WithServerURL(p.server.URL),
		polargo.WithSecurity("fake-token"),
		polargo.WithClient(&client),
	)
	data.Webhooks = &webhookEndpointHTTP{baseURL: p.server.URL, token: "fake-token", client: &client}
}

type fakePolarTransport struct {
	origin    string
	transport http.RoundTripper
}

func (t fakePolarTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme+"://"+req.URL.Host != t.origin {
		return nil, fmt.Errorf("fake Polar test refused non-fixture request: %s", req.URL)
	}
	transport := t.transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(req)
}

func TestMeterResource_importRefreshPreservesUsageCentsAndUnknownMetadata(t *testing.T) {
	const meterID = "11111111-1111-4111-8111-111111111111"
	const body = `{"id":"11111111-1111-4111-8111-111111111111","created_at":"2026-10-13T00:00:00Z","modified_at":null,"name":"Usage cents","organization_id":"22222222-2222-4222-8222-222222222222","filter":{"conjunction":"and","clauses":[{"property":"name","operator":"eq","value":"usage_cents"}]},"aggregation":{"func":"sum","property":"cents"},"metadata":{"future_annotation":"retained-as-string","numeric_text":"00123"},"archived_at":null}`
	var reads, archives, forbidden atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Path != "/v1/meters/"+meterID || req.Header.Get("Authorization") != "Bearer fake-token" {
			forbidden.Add(1)
			t.Errorf("unexpected meter request: %s %s", req.Method, req.URL.Path)
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		switch req.Method {
		case http.MethodGet:
			reads.Add(1)
		case http.MethodPatch:
			// The acceptance harness archives the fixture only after all steps.
			var payload map[string]any
			if err := json.NewDecoder(req.Body).Decode(&payload); err != nil || len(payload) != 1 || payload["is_archived"] != true {
				forbidden.Add(1)
				t.Errorf("unexpected meter mutation: %#v (decode error: %v)", payload, err)
				http.Error(w, "unexpected mutation", http.StatusBadRequest)
				return
			}
			archives.Add(1)
		default:
			forbidden.Add(1)
			t.Errorf("adoption must not issue %s", req.Method)
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()

	const config = `
provider "polar" {
  access_token = "fake-token"
  server       = "sandbox"
}
resource "polar_meter" "adopted" {
  name = "Usage cents"
  filter = {
    conjunction = "and"
    clauses = [{ property = "name", operator = "eq", value = "usage_cents" }]
  }
  aggregation = { func = "sum", property = "cents" }
}
`
	readOnly := func() error {
		if forbidden.Load() != 0 || archives.Load() != 0 {
			return fmt.Errorf("import/refresh mutated fixture: forbidden=%d archives=%d", forbidden.Load(), archives.Load())
		}
		return nil
	}
	checkState := resource.ComposeAggregateTestCheckFunc(
		resource.TestCheckResourceAttr("polar_meter.adopted", "id", meterID),
		resource.TestCheckResourceAttr("polar_meter.adopted", "filter.clauses.0.property", "name"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "filter.clauses.0.operator", "eq"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "filter.clauses.0.value", "usage_cents"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "aggregation.func", "sum"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "aggregation.property", "cents"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "metadata.future_annotation", "retained-as-string"),
		resource.TestCheckResourceAttr("polar_meter.adopted", "metadata.numeric_text", "00123"),
		func(_ *terraform.State) error { return readOnly() },
	)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: fakePolarProviderFactories(server),
		Steps: []resource.TestStep{
			{
				Config:             config,
				ResourceName:       "polar_meter.adopted",
				ImportState:        true,
				ImportStateId:      meterID,
				ImportStatePersist: true,
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 || states[0].ID != meterID {
						return fmt.Errorf("expected only imported meter %s, got %#v", meterID, states)
					}
					for key, want := range map[string]string{
						"filter.clauses.0.value":     "usage_cents",
						"aggregation.func":           "sum",
						"aggregation.property":       "cents",
						"metadata.future_annotation": "retained-as-string",
						"metadata.numeric_text":      "00123",
					} {
						if got := states[0].Attributes[key]; got != want {
							return fmt.Errorf("imported %s = %q, want %q", key, got, want)
						}
					}
					return readOnly()
				},
			},
			{RefreshState: true, Check: checkState},
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
	if reads.Load() < 2 || forbidden.Load() != 0 || archives.Load() != 1 {
		t.Errorf("expected import/refresh GETs and one cleanup archive only: reads=%d forbidden=%d archives=%d", reads.Load(), forbidden.Load(), archives.Load())
	}
}
