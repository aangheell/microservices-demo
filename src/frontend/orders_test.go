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
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sirupsen/logrus"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

func testStoredOrders() []*pb.StoredOrder {
	return []*pb.StoredOrder{{
		Order: &pb.OrderResult{
			OrderId:            "11111111-2222-3333-4444-555555555555",
			ShippingTrackingId: "TR-42",
			ShippingCost:       &pb.Money{CurrencyCode: "RON", Units: 44, Nanos: 500000000},
			ShippingAddress:    &pb.Address{City: "Cluj-Napoca", Country: "Romania"},
			Items: []*pb.OrderItem{
				{Item: &pb.CartItem{ProductId: "B", Quantity: 3}, Cost: &pb.Money{CurrencyCode: "RON", Units: 10, Nanos: 250000000}},
				{Item: &pb.CartItem{ProductId: "GONE", Quantity: 1}, Cost: &pb.Money{CurrencyCode: "RON", Units: 5}},
			},
		},
		Total:         &pb.Money{CurrencyCode: "RON", Units: 80, Nanos: 250000000},
		CreatedAtUnix: 1795281897, // 21 Nov 2026, 17:24:57 UTC
	}}
}

func TestBuildOrderViews(t *testing.T) {
	views := buildOrderViews(testStoredOrders(), testProducts())
	if len(views) != 1 {
		t.Fatalf("got %d views, want 1", len(views))
	}
	v := views[0]
	if v.PlacedAt != "21 Nov 2026, 17:24 UTC" {
		t.Errorf("PlacedAt = %q", v.PlacedAt)
	}
	if v.Items[0].Product.GetName() != "Vase" || v.Items[1].Product != nil {
		t.Errorf("products not matched: %+v", v.Items)
	}
	// Line total is unit price x quantity: 3 x 10.25 = 30.75.
	if got := renderMoney(*v.Items[0].LineTotal); got != "30.75 lei" {
		t.Errorf("line total = %q, want 30.75 lei", got)
	}
}

func TestOrdersTemplate(t *testing.T) {
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "orders", map[string]interface{}{
		"orders": buildOrderViews(testStoredOrders(), testProducts()),
	})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{
		"11111111-2222-3333-4444-555555555555", "TR-42", "Cluj-Napoca, Romania",
		"80.25 lei", "44.50 lei", `href="/product/B">Vase</a>`,
		"SKU #GONE", // a product that left the catalog still shows
	} {
		if !strings.Contains(html, want) {
			t.Errorf("orders page missing %q", want)
		}
	}

	buf.Reset()
	if err := templates.ExecuteTemplate(&buf, "orders", map[string]interface{}{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "haven't placed any orders") {
		t.Error("empty history message missing")
	}
}

func TestOrdersRequiresSignIn(t *testing.T) {
	userAuthEnabled, jwtSecret = true, testSecret
	defer func() { userAuthEnabled, jwtSecret = false, nil }()

	r := httptest.NewRequest(http.MethodGet, "/orders", nil)
	r = r.WithContext(context.WithValue(r.Context(), ctxKeyLog{}, logrus.New()))
	w := httptest.NewRecorder()
	(&frontendServer{}).ordersHandler(w, r)
	if w.Code != http.StatusSeeOther || w.Header().Get("Location") != "/login" {
		t.Errorf("anonymous /orders = %d %q, want 303 to /login", w.Code, w.Header().Get("Location"))
	}
}
