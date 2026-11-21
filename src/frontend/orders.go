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
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
	"github.com/GoogleCloudPlatform/microservices-demo/src/frontend/money"
)

type orderLineView struct {
	ProductID string
	Product   *pb.Product // nil if the product is no longer in the catalog
	Quantity  int32
	LineTotal *pb.Money
}

type orderView struct {
	ID         string
	PlacedAt   string
	TrackingID string
	Items      []orderLineView
	Shipping   *pb.Money
	Total      *pb.Money
	Address    *pb.Address
}

// buildOrderViews pairs stored orders with catalog products for display.
// Prices are the ones charged, in the currency the user paid in.
func buildOrderViews(orders []*pb.StoredOrder, products []*pb.Product) []orderView {
	byID := make(map[string]*pb.Product, len(products))
	for _, p := range products {
		byID[p.GetId()] = p
	}
	views := make([]orderView, 0, len(orders))
	for _, o := range orders {
		v := orderView{
			ID:         o.GetOrder().GetOrderId(),
			PlacedAt:   time.Unix(o.GetCreatedAtUnix(), 0).UTC().Format("2 Jan 2006, 15:04 UTC"),
			TrackingID: o.GetOrder().GetShippingTrackingId(),
			Shipping:   o.GetOrder().GetShippingCost(),
			Total:      o.GetTotal(),
			Address:    o.GetOrder().GetShippingAddress(),
		}
		for _, it := range o.GetOrder().GetItems() {
			qty := it.GetItem().GetQuantity()
			lineTotal := money.MultiplySlow(*it.GetCost(), uint32(qty))
			v.Items = append(v.Items, orderLineView{
				ProductID: it.GetItem().GetProductId(),
				Product:   byID[it.GetItem().GetProductId()],
				Quantity:  qty,
				LineTotal: &lineTotal,
			})
		}
		views = append(views, v)
	}
	return views
}

func (fe *frontendServer) listOrders(r *http.Request, userID string) ([]*pb.StoredOrder, error) {
	resp, err := pb.NewUserServiceClient(fe.userSvcConn).ListOrders(r.Context(), &pb.ListOrdersRequest{UserId: userID})
	return resp.GetOrders(), err
}

// ordersHandler shows the signed-in user's order history.
func (fe *frontendServer) ordersHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)
	user := currentUser(r)
	if user == nil {
		http.Redirect(w, r, baseUrl+"/login", http.StatusSeeOther)
		return
	}
	orders, err := fe.listOrders(r, user.Subject)
	if err != nil {
		renderHTTPError(log, r, w, errors.Wrap(err, "could not retrieve your orders"), http.StatusServiceUnavailable)
		return
	}
	// Without the catalog the page still works, showing product IDs only.
	products, err := fe.getProducts(r.Context())
	if err != nil {
		log.Warnf("orders page without product details: %v", err)
	}
	if err := templates.ExecuteTemplate(w, "orders", injectCommonTemplateData(r, map[string]interface{}{
		"show_currency": false,
		"orders":        buildOrderViews(orders, products),
	})); err != nil {
		log.Println(err)
	}
}
