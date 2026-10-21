// Copyright 2026 Google LLC
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

// Pantalla de rastreo (#32). Solo existe con ENABLE_ORDER_TRACKING=true:
// con el seguimiento apagado no se conecta a orderservice ni se registra la
// ruta (ADR 0007).

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
	"github.com/GoogleCloudPlatform/microservices-demo/src/frontend/money"
)

const (
	// Parámetro de la URL con el número de rastreo: /rastreo?id=XX-123-456.
	trackingQueryParam = "id"
	// Cuánto esperamos a orderservice antes de mostrar "intenta más tarde".
	trackingRPCTimeout = 3 * time.Second
	trackingTimeLayout = "02/01/2006 15:04 UTC"

	msgTrackingEmpty       = "Escribe el número de rastreo que viene en la confirmación de tu compra."
	msgTrackingNotFound    = "No encontramos ningún pedido con el número %s. Revisa que esté escrito igual que en tu confirmación."
	msgTrackingUnavailable = "No pudimos consultar tu pedido en este momento. Intenta de nuevo en unos minutos."
)

// Mismo interruptor y misma comparación que checkoutservice (#28).
var orderTrackingEnabled = os.Getenv("ENABLE_ORDER_TRACKING") == "true"

// Textos en pantalla de cada estado (docs/modelo-del-pedido.md §3).
var trackingStatusLabels = map[pb.OrderStatus]string{
	pb.OrderStatus_ORDER_STATUS_CREATED:    "Creado",
	pb.OrderStatus_ORDER_STATUS_PAID:       "Pagado",
	pb.OrderStatus_ORDER_STATUS_PREPARING:  "En preparación",
	pb.OrderStatus_ORDER_STATUS_IN_TRANSIT: "En tránsito",
	pb.OrderStatus_ORDER_STATUS_DELIVERED:  "Entregado",
}

func trackingStatusLabel(s pb.OrderStatus) string {
	if l, ok := trackingStatusLabels[s]; ok {
		return l
	}
	return "Desconocido"
}

// trackingOrderView es lo único que llega a la plantilla. A propósito no
// tiene dirección, correo ni ningún dato personal (ADR 0009).
type trackingOrderView struct {
	TrackingID  string
	StatusLabel string
	Delivered   bool
	PurchasedAt string
	Total       *pb.Money
	Items       []trackingItemView
	Timeline    []trackingStepView
}

type trackingItemView struct {
	ProductID string
	// Product es nil si el catálogo no pudo resolverlo: la plantilla muestra
	// entonces el product_id, sin imagen (ADR 0004).
	Product  *pb.Product
	Quantity int32
	// Lo que se pagó por la línea (precio unitario del pedido × cantidad).
	Price *pb.Money
}

type trackingStepView struct {
	Label    string
	StartsAt string
	Reached  bool
	Current  bool
}

// productLookup es fe.getProduct; los tests le pasan uno falso.
type productLookup func(ctx context.Context, id string) (*pb.Product, error)

// buildTrackingView arma la vista del pedido. Nombre e imagen se piden al
// catálogo al pintar, igual que el carrito. Si el catálogo falla para un
// producto (NotFound o cualquier otro error), esa línea se degrada al
// product_id y la pantalla no truena.
func buildTrackingView(ctx context.Context, log logrus.FieldLogger, order *pb.TrackedOrder, now time.Time, lookup productLookup) trackingOrderView {
	view := trackingOrderView{
		TrackingID:  order.GetShippingTrackingId(),
		StatusLabel: trackingStatusLabel(order.GetStatus()),
		Delivered:   order.GetStatus() == pb.OrderStatus_ORDER_STATUS_DELIVERED,
		PurchasedAt: formatTrackingTime(order.GetPurchasedAtUnix()),
		Total:       order.GetTotal(),
	}

	for _, it := range order.GetItems() {
		id := it.GetItem().GetProductId()
		item := trackingItemView{ProductID: id, Quantity: it.GetItem().GetQuantity()}
		if cost := it.GetCost(); cost != nil && item.Quantity > 0 {
			line := money.MultiplySlow(*cost, uint32(item.Quantity))
			item.Price = &line
		}
		p, err := lookup(ctx, id)
		if err != nil {
			log.WithFields(logrus.Fields{"product_id": id, "error": err}).Warn("tracking: product not resolved, showing product_id")
		} else {
			item.Product = p
		}
		view.Items = append(view.Items, item)
	}

	for _, step := range order.GetTimeline() {
		view.Timeline = append(view.Timeline, trackingStepView{
			Label:    trackingStatusLabel(step.GetStatus()),
			StartsAt: formatTrackingTime(step.GetStartsAtUnix()),
			Reached:  step.GetStartsAtUnix() <= now.Unix(),
			Current:  step.GetStatus() == order.GetStatus(),
		})
	}
	return view
}

func formatTrackingTime(unix int64) string {
	if unix <= 0 {
		return ""
	}
	return time.Unix(unix, 0).UTC().Format(trackingTimeLayout)
}

// trackingResult traduce la respuesta de orderservice a código HTTP y
// mensaje. Un número inválido se trata igual que uno que no existe: para
// quien consulta es lo mismo.
func trackingResult(trackingID string, err error) (int, string) {
	switch status.Code(err) {
	case codes.OK:
		return http.StatusOK, ""
	case codes.NotFound, codes.InvalidArgument:
		return http.StatusNotFound, fmt.Sprintf(msgTrackingNotFound, trackingID)
	default:
		return http.StatusServiceUnavailable, msgTrackingUnavailable
	}
}

func (fe *frontendServer) trackingHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)

	_, asked := r.URL.Query()[trackingQueryParam]
	trackingID := strings.TrimSpace(r.URL.Query().Get(trackingQueryParam))

	data := map[string]interface{}{
		"show_currency": false,
		"tracking_id":   trackingID,
	}
	code := http.StatusOK

	switch {
	case !asked:
		// Primera visita: solo el formulario.
	case trackingID == "":
		code = http.StatusBadRequest
		data["message"] = msgTrackingEmpty
	default:
		ctx, cancel := context.WithTimeout(r.Context(), trackingRPCTimeout)
		defer cancel()
		order, err := pb.NewOrderServiceClient(fe.orderSvcConn).
			GetOrderByTrackingId(ctx, &pb.GetOrderByTrackingIdRequest{TrackingId: trackingID})
		var msg string
		code, msg = trackingResult(trackingID, err)
		if code == http.StatusOK {
			view := buildTrackingView(r.Context(), log, order, time.Now(), fe.getProduct)
			data["order"] = &view
		} else {
			data["message"] = msg
			if code == http.StatusServiceUnavailable {
				log.WithField("error", err).Error("tracking: could not query orderservice")
			}
		}
	}

	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, "tracking", injectCommonTemplateData(r, data)); err != nil {
		log.Println(err)
	}
}
