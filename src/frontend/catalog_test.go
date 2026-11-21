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
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

func testProducts() []*pb.Product {
	return []*pb.Product{
		{Id: "A", Name: "Mug", Categories: []string{"kitchen"}},
		{Id: "B", Name: "Vase", Categories: []string{"home", "decor"}},
		{Id: "C", Name: "Candle Holder", Categories: []string{"home", "decor"}},
		{Id: "D", Name: "Tank Top", Categories: []string{"clothing", "tops"}},
		{Id: "E", Name: "No category"},
	}
}

func ids(ps []*pb.Product) []string {
	var out []string
	for _, p := range ps {
		out = append(out, p.Id)
	}
	return out
}

func TestPrimaryCategories(t *testing.T) {
	got := primaryCategories(testProducts())
	want := []string{"clothing", "home", "kitchen"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("primaryCategories = %v, want %v", got, want)
	}
}

func TestFilterByCategory(t *testing.T) {
	ps := testProducts()
	cases := map[string][]string{
		"":        {"A", "B", "C", "D", "E"},
		"home":    {"B", "C"},
		"decor":   {"B", "C"}, // secondary categories filter too
		"kitchen": {"A"},
	}
	for cat, want := range cases {
		if got := ids(filterByCategory(ps, cat)); !reflect.DeepEqual(got, want) {
			t.Errorf("filterByCategory(%q) = %v, want %v", cat, got, want)
		}
	}
}

func TestSelectedCategory(t *testing.T) {
	ps := testProducts()
	cases := map[string]string{
		"/":                      "",
		"/?category=home":        "home",
		"/?category=%20Home%20":  "home",
		"/?category=tops":        "tops",
		"/?category=nonexistent": "",
		"/?category=<script>":    "",
	}
	for url, want := range cases {
		if got := selectedCategory(httptest.NewRequest("GET", url, nil), ps); got != want {
			t.Errorf("selectedCategory(%s) = %q, want %q", url, got, want)
		}
	}
}

// The templates must render with the new category data, and the platform
// banner must be gone.
func TestTemplatesRenderCategories(t *testing.T) {
	type productView struct {
		Item  *pb.Product
		Price *pb.Money
	}
	price := &pb.Money{CurrencyCode: "USD", Units: 5}
	var buf bytes.Buffer
	err := templates.ExecuteTemplate(&buf, "home", map[string]interface{}{
		"products":   []productView{{testProducts()[1], price}},
		"categories": []string{"home", "kitchen"},
		"category":   "home",
	})
	if err != nil {
		t.Fatal(err)
	}
	html := buf.String()
	for _, want := range []string{"<h3>Home</h3>", `href="/?category=kitchen"`, `category-pill active">Home<`} {
		if !strings.Contains(html, want) {
			t.Errorf("home page missing %q", want)
		}
	}
	if strings.Contains(html, "platform-flag") {
		t.Error("platform banner still rendered")
	}

	buf.Reset()
	err = templates.ExecuteTemplate(&buf, "product", map[string]interface{}{
		"product": productView{testProducts()[1], price},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), `href="/?category=decor"`) {
		t.Error("product page missing category link")
	}
}
