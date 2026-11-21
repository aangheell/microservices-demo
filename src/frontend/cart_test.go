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
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/golang-jwt/jwt/v5"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

// fakeCarts mimics cartservice: AddItem adds to an existing quantity.
type fakeCarts struct {
	carts     map[string]map[string]int32
	failAddOn string // product ID whose insert fails
}

func newFakeCarts() *fakeCarts { return &fakeCarts{carts: map[string]map[string]int32{}} }

func (f *fakeCarts) getCart(_ context.Context, id string) ([]*pb.CartItem, error) {
	var out []*pb.CartItem
	for p, q := range f.carts[id] {
		out = append(out, &pb.CartItem{ProductId: p, Quantity: q})
	}
	return out, nil
}

func (f *fakeCarts) insertCart(_ context.Context, id, p string, q int32) error {
	if p == f.failAddOn {
		return errors.New("boom")
	}
	if f.carts[id] == nil {
		f.carts[id] = map[string]int32{}
	}
	f.carts[id][p] += q
	return nil
}

func (f *fakeCarts) emptyCart(_ context.Context, id string) error {
	delete(f.carts, id)
	return nil
}

func withSession(r *http.Request, id string) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), ctxKeySessionID{}, id))
}

func TestCartOwner(t *testing.T) {
	userAuthEnabled, jwtSecret = true, testSecret
	defer func() { userAuthEnabled, jwtSecret = false, nil }()

	r := withSession(httptest.NewRequest(http.MethodGet, "/", nil), "sess-1")
	if got := cartOwner(r); got != "guest:sess-1" {
		t.Errorf("anonymous owner = %q", got)
	}

	r.AddCookie(&http.Cookie{Name: cookieUserJWT, Value: signToken(t, jwt.SigningMethodHS256, testSecret, nil)})
	if got := cartOwner(r); got != "user:6f1c2a52-0000-4000-8000-000000000001" {
		t.Errorf("signed-in owner = %q", got)
	}

	// A forged session cookie can never address a user's cart.
	r = withSession(httptest.NewRequest(http.MethodGet, "/", nil), "user:6f1c2a52-0000-4000-8000-000000000001")
	if got := cartOwner(r); !strings.HasPrefix(got, cartKeyGuestPrefix) {
		t.Errorf("session cookie escaped the guest namespace: %q", got)
	}

	// An invalid JWT falls back to the guest cart.
	r = withSession(httptest.NewRequest(http.MethodGet, "/", nil), "sess-2")
	r.AddCookie(&http.Cookie{Name: cookieUserJWT, Value: "not-a-jwt"})
	if got := cartOwner(r); got != "guest:sess-2" {
		t.Errorf("invalid token owner = %q", got)
	}
}

func TestMergeCarts(t *testing.T) {
	ctx := context.Background()
	f := newFakeCarts()
	f.carts["guest:s"] = map[string]int32{"A": 2, "B": 1}
	f.carts["user:u"] = map[string]int32{"A": 1, "C": 5}

	if err := mergeCarts(ctx, f, "guest:s", "user:u"); err != nil {
		t.Fatal(err)
	}
	want := map[string]int32{"A": 3, "B": 1, "C": 5}
	for p, q := range want {
		if f.carts["user:u"][p] != q {
			t.Errorf("user cart %s = %d, want %d", p, f.carts["user:u"][p], q)
		}
	}
	if len(f.carts["guest:s"]) != 0 {
		t.Errorf("guest cart not emptied: %v", f.carts["guest:s"])
	}

	// Merging an empty guest cart, or a cart into itself, changes nothing.
	if err := mergeCarts(ctx, f, "guest:none", "user:u"); err != nil {
		t.Fatal(err)
	}
	if err := mergeCarts(ctx, f, "user:u", "user:u"); err != nil || f.carts["user:u"]["C"] != 5 {
		t.Fatalf("self-merge changed the cart: %v %v", err, f.carts["user:u"])
	}
}

func TestMergeCartsKeepsGuestCartOnFailure(t *testing.T) {
	f := newFakeCarts()
	f.carts["guest:s"] = map[string]int32{"BAD": 1}
	f.failAddOn = "BAD"
	if err := mergeCarts(context.Background(), f, "guest:s", "user:u"); err == nil {
		t.Fatal("expected an error")
	}
	if f.carts["guest:s"]["BAD"] != 1 {
		t.Error("guest cart was emptied although the copy failed")
	}
}
