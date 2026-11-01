// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"testing"
)

func TestProductMetadataRefusesUnrepresentableValues(t *testing.T) {
	for _, value := range []string{`null`, `[]`, `{"n":null}`, `{"n":[]}`, `{"n":{}}`, `{"n":9223372036854775808}`, `{"n":1e999}`} {
		t.Run(value, func(t *testing.T) {
			var diags diag.Diagnostics
			validateProductMetadataJSON(jsontypes.NewNormalizedValue(value), &diags)
			if !diags.HasError() {
				t.Fatalf("accepted metadata outside Polar scalar contract: %s", value)
			}
		})
	}
}
