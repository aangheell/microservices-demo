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
	"net/http"

	"github.com/pkg/errors"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

// Cart keys are namespaced so that a client-controlled session cookie can
// never name a signed-in user's cart: a guest key always starts with "guest:",
// and a user key is only ever built from a verified JWT subject.
const (
	cartKeyGuestPrefix = "guest:"
	cartKeyUserPrefix  = "user:"
)

func guestCartKey(sessionID string) string { return cartKeyGuestPrefix + sessionID }
func userCartKey(userID string) string     { return cartKeyUserPrefix + userID }

// cartOwner returns the key the cart (and the order placed from it) belongs
// to: the signed-in user, or else the anonymous browser session.
func cartOwner(r *http.Request) string {
	if u := currentUser(r); u != nil {
		return userCartKey(u.Subject)
	}
	return guestCartKey(sessionID(r))
}

// cartOps is the subset of cartservice calls the merge needs; frontendServer
// implements it, and tests use a fake.
type cartOps interface {
	getCart(ctx context.Context, userID string) ([]*pb.CartItem, error)
	insertCart(ctx context.Context, userID, productID string, quantity int32) error
	emptyCart(ctx context.Context, userID string) error
}

// mergeCarts moves every item from one cart into another, adding quantities
// for products already present, then empties the source cart. The source is
// only emptied once every item was copied, so a failure part-way never loses
// items (at worst a retry adds some of them twice).
func mergeCarts(ctx context.Context, c cartOps, from, to string) error {
	if from == to {
		return nil
	}
	items, err := c.getCart(ctx, from)
	if err != nil {
		return errors.Wrap(err, "read guest cart")
	}
	if len(items) == 0 {
		return nil
	}
	for _, it := range items {
		if err := c.insertCart(ctx, to, it.GetProductId(), it.GetQuantity()); err != nil {
			return errors.Wrapf(err, "copy %s to user cart", it.GetProductId())
		}
	}
	return errors.Wrap(c.emptyCart(ctx, from), "empty guest cart")
}
