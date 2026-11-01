// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

func validateProductMetadataJSON(value jsontypes.Normalized, diags *diag.Diagnostics) {
	if value.IsNull() || value.IsUnknown() {
		return
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value.ValueString()), &fields); err != nil || fields == nil {
		diags.AddError("Invalid metadata_json", "Expected a JSON object of string, int64, finite number, or boolean values.")
		return
	}
	if len(fields) > 50 {
		diags.AddError("Invalid metadata_json", "Metadata supports at most 50 keys.")
	}
	for key, raw := range fields {
		if len(key) > 40 {
			diags.AddError("Invalid metadata_json", "Metadata keys support at most 40 characters.")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var v any
		if err := decoder.Decode(&v); err != nil {
			diags.AddError("Invalid metadata_json", err.Error())
			continue
		}
		switch v := v.(type) {
		case string:
			if len(v) > 500 {
				diags.AddError("Invalid metadata_json", "Metadata strings support at most 500 characters.")
			}
		case bool:
		case json.Number:
			if !strings.ContainsAny(v.String(), ".eE") {
				if _, err := v.Int64(); err != nil {
					diags.AddError("Invalid metadata_json", "Integer metadata values must fit in int64.")
				}
			} else if _, err := v.Float64(); err != nil {
				diags.AddError("Invalid metadata_json", "Metadata numbers must be finite.")
			}
		default:
			diags.AddError("Invalid metadata_json", "Metadata values cannot be null, arrays, or objects.")
		}
	}
}

func productMetadataFromJSON[T any](value jsontypes.Normalized, diags *diag.Diagnostics) map[string]T {
	validateProductMetadataJSON(value, diags)
	if diags.HasError() {
		return nil
	}
	var result map[string]T
	if err := json.Unmarshal([]byte(value.ValueString()), &result); err != nil {
		diags.AddError("Invalid metadata_json", fmt.Sprintf("Cannot encode metadata: %s", err))
	}
	return result
}

func productMetadataJSON[T any](metadata map[string]T, prior jsontypes.Normalized, diags *diag.Diagnostics) jsontypes.Normalized {
	if metadata == nil {
		metadata = map[string]T{}
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		diags.AddError("Invalid metadata response", err.Error())
		return jsontypes.NewNormalizedNull()
	}
	// Keep the configured spelling when values and JSON types are unchanged.
	decode := func(raw []byte) any {
		var v any
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		_ = d.Decode(&v)
		return v
	}
	if !prior.IsUnknown() && reflect.DeepEqual(decode([]byte(prior.ValueString())), decode(encoded)) {
		return prior
	}
	return jsontypes.NewNormalizedValue(string(encoded))
}
