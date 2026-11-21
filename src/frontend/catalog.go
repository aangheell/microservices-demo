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
	"net/http"
	"sort"
	"strings"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

// primaryCategories lists, sorted, the first category of every product.
// Those make up the category bar; secondary ones (e.g. "tops", "decor") can
// still be filtered on through the URL and product-page links.
func primaryCategories(products []*pb.Product) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range products {
		if cats := p.GetCategories(); len(cats) > 0 && !seen[cats[0]] {
			seen[cats[0]] = true
			out = append(out, cats[0])
		}
	}
	sort.Strings(out)
	return out
}

func hasCategory(p *pb.Product, category string) bool {
	for _, c := range p.GetCategories() {
		if c == category {
			return true
		}
	}
	return false
}

// filterByCategory returns the products in the category, or all of them when
// the category is empty.
func filterByCategory(products []*pb.Product, category string) []*pb.Product {
	if category == "" {
		return products
	}
	var out []*pb.Product
	for _, p := range products {
		if hasCategory(p, category) {
			out = append(out, p)
		}
	}
	return out
}

// selectedCategory reads ?category= and returns it only if some product is
// in that category; anything else shows the full catalog.
func selectedCategory(r *http.Request, products []*pb.Product) string {
	c := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("category")))
	if c == "" {
		return ""
	}
	for _, p := range products {
		if hasCategory(p, c) {
			return c
		}
	}
	return ""
}

// categoryName turns a category slug like "home" into a display name.
func categoryName(slug string) string {
	return capitalize(slug)
}
