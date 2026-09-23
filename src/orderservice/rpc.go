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

import (
	"context"
	"errors"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// RecordOrder guarda el pedido bajo su número de rastreo.
// Es idempotente: repetir la llamada con el mismo número no crea otro pedido
// ni devuelve error, porque checkoutservice reintenta hasta tres veces.
func (s *server) RecordOrder(ctx context.Context, req *pb.RecordOrderRequest) (*pb.RecordOrderResponse, error) {
	log.Info("[RecordOrder] received request")
	defer log.Info("[RecordOrder] completed request")

	order := req.GetOrder()
	switch {
	case order == nil:
		return nil, status.Error(codes.InvalidArgument, "order is required")
	case order.GetShippingTrackingId() == "":
		return nil, status.Error(codes.InvalidArgument, "order.shipping_tracking_id is required")
	case order.GetOrderId() == "":
		return nil, status.Error(codes.InvalidArgument, "order.order_id is required")
	case order.GetPurchasedAtUnix() <= 0:
		return nil, status.Error(codes.InvalidArgument, "order.purchased_at_unix is required")
	}

	created, err := s.orders.save(ctx, order)
	if err != nil {
		log.Errorf("[RecordOrder] could not save order: %v", err)
		return nil, status.Error(codes.Internal, "could not record order")
	}

	// Un reintento termina bien, pero se distingue en el log: el contador de
	// RecordOrder que define #34 necesita poder separar altas de reintentos.
	log.WithFields(map[string]interface{}{
		"tracking_id": order.GetShippingTrackingId(),
		"created":     created,
	}).Info("[RecordOrder] order recorded")

	return &pb.RecordOrderResponse{OrderId: order.GetOrderId()}, nil
}

// GetOrderByTrackingId devuelve el pedido con su estado calculado en este
// instante. El estado no se lee de Redis: sale de la fecha de compra.
func (s *server) GetOrderByTrackingId(ctx context.Context, req *pb.GetOrderByTrackingIdRequest) (*pb.TrackedOrder, error) {
	log.Info("[GetOrderByTrackingId] received request")
	defer log.Info("[GetOrderByTrackingId] completed request")

	trackingID := req.GetTrackingId()
	if trackingID == "" {
		return nil, status.Error(codes.InvalidArgument, "tracking_id is required")
	}

	order, err := s.orders.get(ctx, trackingID)
	if errors.Is(err, ErrNotFound) {
		// Un pedido inexistente es una respuesta normal, no una falla:
		// por eso va en info y no en error (ADR 0009: el número es adivinable).
		log.Infof("[GetOrderByTrackingId] no order with tracking id %q", trackingID)
		return nil, status.Errorf(codes.NotFound, "no order with tracking id %s", trackingID)
	}
	if err != nil {
		log.Errorf("[GetOrderByTrackingId] could not read order: %v", err)
		return nil, status.Error(codes.Internal, "could not read order")
	}

	purchasedAt := order.GetPurchasedAtUnix()
	now := s.now().UTC().Unix()

	// Solo lo que el ADR 0009 permite mostrar: ni dirección, ni correo,
	// ni nombres de producto. El frontend resuelve los product_id al pintar.
	return &pb.TrackedOrder{
		ShippingTrackingId: order.GetShippingTrackingId(),
		Status:             StatusAt(purchasedAt, now, s.durations),
		Items:              order.GetItems(),
		PurchasedAtUnix:    purchasedAt,
		Total:              order.GetTotal(),
		Timeline:           TimelineFor(purchasedAt, s.durations),
	}, nil
}
