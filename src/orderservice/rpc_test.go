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
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// newTestServerWithClock arma el servicio con un Redis falso y un reloj
// que la prueba mueve a voluntad.
func newTestServerWithClock(t *testing.T) (*server, *fakeRedis, *fakeClock) {
	t.Helper()
	f := newFakeRedis()
	clock := &fakeClock{unix: purchasedAt}
	return &server{
		orders:    newStore(f),
		durations: demoDurations,
		now:       clock.Now,
	}, f, clock
}

type fakeClock struct{ unix int64 }

func (c *fakeClock) Now() time.Time          { return time.Unix(c.unix, 0).UTC() }
func (c *fakeClock) advance(d time.Duration) { c.unix += int64(d / time.Second) }

func recordableOrder() *pb.Order {
	o := testOrder()
	o.PurchasedAtUnix = purchasedAt
	return o
}

// Registrar y consultar devuelve el mismo pedido, con todos sus artículos.
func TestRecordThenGet(t *testing.T) {
	s, _, _ := newTestServerWithClock(t)
	order := recordableOrder()

	resp, err := s.RecordOrder(context.Background(), &pb.RecordOrderRequest{Order: order})
	if err != nil {
		t.Fatalf("RecordOrder() error = %v", err)
	}
	if resp.GetOrderId() != order.GetOrderId() {
		t.Errorf("RecordOrder() order_id = %q; want %q", resp.GetOrderId(), order.GetOrderId())
	}

	got, err := s.GetOrderByTrackingId(context.Background(),
		&pb.GetOrderByTrackingIdRequest{TrackingId: order.GetShippingTrackingId()})
	if err != nil {
		t.Fatalf("GetOrderByTrackingId() error = %v", err)
	}

	if got.GetShippingTrackingId() != order.GetShippingTrackingId() {
		t.Errorf("tracking id = %q; want %q", got.GetShippingTrackingId(), order.GetShippingTrackingId())
	}
	if got.GetPurchasedAtUnix() != order.GetPurchasedAtUnix() {
		t.Errorf("purchased_at = %d; want %d", got.GetPurchasedAtUnix(), order.GetPurchasedAtUnix())
	}
	if !proto.Equal(got.GetTotal(), order.GetTotal()) {
		t.Errorf("total = %v; want %v", got.GetTotal(), order.GetTotal())
	}
	if len(got.GetItems()) != len(order.GetItems()) {
		t.Fatalf("len(items) = %d; want %d", len(got.GetItems()), len(order.GetItems()))
	}
	for i, item := range got.GetItems() {
		if !proto.Equal(item, order.GetItems()[i]) {
			t.Errorf("items[%d] = %v; want %v", i, item, order.GetItems()[i])
		}
	}
}

// Registrar dos veces el mismo número deja un solo pedido y no falla.
func TestRecordOrderIsIdempotent(t *testing.T) {
	s, f, _ := newTestServerWithClock(t)
	order := recordableOrder()

	for i := 0; i < 3; i++ {
		if _, err := s.RecordOrder(context.Background(), &pb.RecordOrderRequest{Order: order}); err != nil {
			t.Fatalf("RecordOrder() intento %d error = %v", i+1, err)
		}
	}
	if len(f.values) != 1 {
		t.Errorf("quedaron %d pedidos en redis; want 1", len(f.values))
	}
}

// Dos consultas separadas por más de una etapa devuelven estados distintos.
func TestStatusAdvancesBetweenQueries(t *testing.T) {
	s, _, clock := newTestServerWithClock(t)
	order := recordableOrder()
	if _, err := s.RecordOrder(context.Background(), &pb.RecordOrderRequest{Order: order}); err != nil {
		t.Fatalf("RecordOrder() error = %v", err)
	}

	query := func() pb.OrderStatus {
		t.Helper()
		got, err := s.GetOrderByTrackingId(context.Background(),
			&pb.GetOrderByTrackingIdRequest{TrackingId: order.GetShippingTrackingId()})
		if err != nil {
			t.Fatalf("GetOrderByTrackingId() error = %v", err)
		}
		return got.GetStatus()
	}

	first := query()
	if first != pb.OrderStatus_ORDER_STATUS_CREATED {
		t.Fatalf("primera consulta = %v; want CREATED", first)
	}

	clock.advance(2 * time.Minute) // pasa de largo las cuatro etapas de demo
	second := query()
	if second == first {
		t.Errorf("segunda consulta = %v; esperaba un estado distinto de %v", second, first)
	}
	if second != pb.OrderStatus_ORDER_STATUS_PREPARING {
		t.Errorf("segunda consulta = %v; want PREPARING", second)
	}
}

// El número vacío es InvalidArgument. Se comprueba el código, no el texto.
func TestGetOrderEmptyTrackingID(t *testing.T) {
	s, _, _ := newTestServerWithClock(t)
	_, err := s.GetOrderByTrackingId(context.Background(), &pb.GetOrderByTrackingIdRequest{TrackingId: ""})
	if got, want := status.Code(err), codes.InvalidArgument; got != want {
		t.Errorf("status.Code(err) = %v; want %v", got, want)
	}
}

// Un número que no existe es NotFound, no un error interno.
func TestGetOrderNotFound(t *testing.T) {
	s, _, _ := newTestServerWithClock(t)
	_, err := s.GetOrderByTrackingId(context.Background(),
		&pb.GetOrderByTrackingIdRequest{TrackingId: "NO-1-2"})
	if got, want := status.Code(err), codes.NotFound; got != want {
		t.Errorf("status.Code(err) = %v; want %v", got, want)
	}
}

// Un Redis caído sí es Internal: no se confunde con "no existe".
func TestGetOrderRedisFailureIsInternal(t *testing.T) {
	s, f, _ := newTestServerWithClock(t)
	f.err = errors.New("connection refused")
	_, err := s.GetOrderByTrackingId(context.Background(),
		&pb.GetOrderByTrackingIdRequest{TrackingId: "QK-48512-2407182"})
	if got, want := status.Code(err), codes.Internal; got != want {
		t.Errorf("status.Code(err) = %v; want %v", got, want)
	}
}

func TestRecordOrderValidation(t *testing.T) {
	sinTracking := recordableOrder()
	sinTracking.ShippingTrackingId = ""
	sinOrderID := recordableOrder()
	sinOrderID.OrderId = ""
	sinFecha := recordableOrder()
	sinFecha.PurchasedAtUnix = 0

	tests := []struct {
		name  string
		order *pb.Order
	}{
		{"sin pedido", nil},
		{"sin numero de rastreo", sinTracking},
		{"sin order_id", sinOrderID},
		{"sin fecha de compra", sinFecha},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, f, _ := newTestServerWithClock(t)
			_, err := s.RecordOrder(context.Background(), &pb.RecordOrderRequest{Order: tc.order})
			if got, want := status.Code(err), codes.InvalidArgument; got != want {
				t.Errorf("status.Code(err) = %v; want %v", got, want)
			}
			if len(f.values) != 0 {
				t.Errorf("se guardo algo en redis con una peticion invalida")
			}
		})
	}
}

// La consulta no devuelve datos personales (ADR 0009). TrackedOrder ni
// siquiera tiene dónde ponerlos, y esta prueba lo deja fijado por si el
// contrato cambia: lo que se devuelve no incluye la dirección de envío.
func TestTrackedOrderHasNoPersonalData(t *testing.T) {
	s, _, _ := newTestServerWithClock(t)
	order := recordableOrder()
	if _, err := s.RecordOrder(context.Background(), &pb.RecordOrderRequest{Order: order}); err != nil {
		t.Fatalf("RecordOrder() error = %v", err)
	}

	got, err := s.GetOrderByTrackingId(context.Background(),
		&pb.GetOrderByTrackingIdRequest{TrackingId: order.GetShippingTrackingId()})
	if err != nil {
		t.Fatalf("GetOrderByTrackingId() error = %v", err)
	}

	raw, err := marshalOrder.Marshal(got)
	if err != nil {
		t.Fatalf("no se pudo serializar la respuesta: %v", err)
	}
	for _, forbidden := range []string{"Amphitheatre", "Mountain View", "94043", "shipping_address"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("la respuesta trae %q y no deberia: %s", forbidden, raw)
		}
	}
}
