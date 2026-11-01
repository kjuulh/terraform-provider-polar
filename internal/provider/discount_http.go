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

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/polarsource/polar-go/models/apierrors"
	"github.com/polarsource/polar-go/models/components"
)

// polar-go v0.16.0 does not model max_redemptions_per_customer. Keep its
// polymorphic discount models and add only the missing request/response field.
// Mutations are sent once: an ambiguous response must not redeem/create twice.
type discountHTTP struct {
	baseURL string
	token   string
	client  *http.Client
}

func (c *discountHTTP) get(ctx context.Context, id string) (*timestampedDiscount, error) {
	return c.request(ctx, http.MethodGet, id, nil, types.Int64Null())
}

func (c *discountHTTP) create(ctx context.Context, body components.DiscountCreate, limit types.Int64) (*timestampedDiscount, error) {
	return c.request(ctx, http.MethodPost, "", body, limit)
}

func (c *discountHTTP) update(ctx context.Context, id string, body components.DiscountUpdate, limit types.Int64) (*timestampedDiscount, error) {
	return c.request(ctx, http.MethodPatch, id, body, limit)
}

func (c *discountHTTP) request(ctx context.Context, method, id string, body any, limit types.Int64) (*timestampedDiscount, error) {
	endpointURL := strings.TrimRight(c.baseURL, "/") + "/v1/discounts"
	if id != "" {
		endpointURL += "/" + url.PathEscape(id)
	}

	var payload []byte
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding discount request: %w", err)
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(encoded, &fields); err != nil {
			return nil, fmt.Errorf("decoding discount request fields: %w", err)
		}
		// PATCH must send null when a configured per-customer limit is removed.
		// All other fields retain the SDK's existing omission semantics.
		if method == http.MethodPatch || !limit.IsNull() {
			value, err := json.Marshal(limit.ValueInt64Pointer())
			if err != nil {
				return nil, fmt.Errorf("encoding per-customer redemption limit: %w", err)
			}
			fields["max_redemptions_per_customer"] = value
		}
		payload, err = json.Marshal(fields)
		if err != nil {
			return nil, fmt.Errorf("encoding discount request fields: %w", err)
		}
	}

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
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound && strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") {
		var notFound apierrors.ResourceNotFound
		if err := json.NewDecoder(resp.Body).Decode(&notFound); err != nil {
			return nil, fmt.Errorf("decoding discount not-found response: %w", err)
		}
		return nil, &notFound
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
		return nil, apierrors.NewAPIError("discount request failed", resp.StatusCode, string(message), resp)
	}
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decoding discount response: %w", err)
	}
	var discount components.Discount
	if err := json.Unmarshal(raw, &discount); err != nil {
		return nil, fmt.Errorf("decoding discount response: %w", err)
	}
	var extra struct {
		MaxRedemptionsPerCustomer *int64 `json:"max_redemptions_per_customer"`
	}
	if err := json.Unmarshal(raw, &extra); err != nil {
		return nil, fmt.Errorf("decoding per-customer redemption limit: %w", err)
	}
	return &timestampedDiscount{Discount: &discount, MaxRedemptionsPerCustomer: extra.MaxRedemptionsPerCustomer}, nil
}
