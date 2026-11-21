// Copyright 2024 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"testing"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

func TestRenderMoney(t *testing.T) {
	cases := []struct {
		code  string
		units int64
		nanos int32
		want  string
	}{
		{"USD", 19, 990000000, "$19.99"},
		{"EUR", 5, 50000000, "€5.05"},
		{"RON", 94, 870000000, "94.87 lei"},
	}
	for _, c := range cases {
		got := renderMoney(pb.Money{CurrencyCode: c.code, Units: c.units, Nanos: c.nanos})
		if got != c.want {
			t.Errorf("renderMoney(%s) = %q, want %q", c.code, got, c.want)
		}
	}
	if !whitelistedCurrencies["RON"] {
		t.Error("RON is not offered in the currency picker")
	}
}
