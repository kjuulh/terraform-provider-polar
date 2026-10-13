// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/polarsource/polar-go/models/apierrors"
	"github.com/polarsource/polar-go/models/components"
)

// polar-go v0.16.0 rejects webhook event names it does not know while decoding
// WebhookEndpoint. Use its request/response models, but decode only the events
// array as strings so newer Polar events survive a Terraform refresh.
// An alias prevents the SDK's endpoint UnmarshalJSON from invoking the strict
// WebhookEventType decoder. All other fields retain their SDK types.
type webhookEndpointFields components.WebhookEndpoint

type webhookEndpointHTTP struct {
	baseURL string
	token   string
	client  *http.Client
}

func (c *webhookEndpointHTTP) get(ctx context.Context, id string) (*components.WebhookEndpoint, error) {
	return c.request(ctx, http.MethodGet, id, nil)
}

func (c *webhookEndpointHTTP) create(ctx context.Context, body components.WebhookEndpointCreate) (*components.WebhookEndpoint, error) {
	return c.request(ctx, http.MethodPost, "", body)
}

func (c *webhookEndpointHTTP) update(ctx context.Context, id string, body components.WebhookEndpointUpdate) (*components.WebhookEndpoint, error) {
	return c.request(ctx, http.MethodPatch, id, body)
}

func (c *webhookEndpointHTTP) request(ctx context.Context, method, id string, body any) (*components.WebhookEndpoint, error) {
	endpointURL := strings.TrimRight(c.baseURL, "/") + "/v1/webhooks/endpoints"
	if id != "" {
		endpointURL += "/" + url.PathEscape(id)
	}

	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding webhook endpoint request: %w", err)
		}
	}

	// Match the SDK's retryable status codes and 120s elapsed-time limit.
	deadline := time.Now().Add(120 * time.Second)
	backoff := 500 * time.Millisecond
	for {
		req, err := http.NewRequestWithContext(ctx, method, endpointURL, bytes.NewReader(payload))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/json")
		if body != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.client.Do(req)
		if err != nil {
			return nil, err
		}
		if (resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusInternalServerError || resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout) && time.Now().Add(backoff).Before(deadline) {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff = min(time.Duration(float64(backoff)*1.5), 30*time.Second)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
			var notFound apierrors.ResourceNotFound
			if err := json.NewDecoder(resp.Body).Decode(&notFound); err != nil {
				return nil, fmt.Errorf("decoding webhook endpoint not-found response: %w", err)
			}
			return nil, &notFound
		}
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
			return nil, apierrors.NewAPIError("webhook endpoint request failed", resp.StatusCode, string(message), resp)
		}
		var endpoint struct {
			webhookEndpointFields
			Events []string `json:"events"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&endpoint); err != nil {
			return nil, fmt.Errorf("decoding webhook endpoint response: %w", err)
		}
		events := make([]components.WebhookEventType, len(endpoint.Events))
		for i, event := range endpoint.Events {
			events[i] = components.WebhookEventType(event)
		}
		result := components.WebhookEndpoint(endpoint.webhookEndpointFields)
		result.Events = events
		return &result, nil
	}
}
