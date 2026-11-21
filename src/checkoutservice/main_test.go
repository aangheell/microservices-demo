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
	"testing"
)

func TestOrderHistoryUserID(t *testing.T) {
	cases := map[string]string{
		"user:6f1c2a9e-0000-4000-8000-000000000001": "6f1c2a9e-0000-4000-8000-000000000001",
		"guest:abc": "",
		"abc":       "", // older frontends sent the bare session ID
		"":          "",
	}
	for owner, want := range cases {
		if got := orderHistoryUserID(owner); got != want {
			t.Errorf("orderHistoryUserID(%q) = %q, want %q", owner, got, want)
		}
	}
}

// Without USER_SERVICE_ADDR there is no connection, and recording is a no-op.
func TestRecordOrderWithoutUserService(t *testing.T) {
	cs := &checkoutService{}
	if err := cs.recordOrder(context.Background(), "user:x", nil, nil); err != nil {
		t.Fatalf("recordOrder = %v, want nil", err)
	}
}
