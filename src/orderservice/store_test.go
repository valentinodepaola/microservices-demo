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

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/proto"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// fakeRedis es un Redis de mentiras: solo SET NX y GET, sin red.
type fakeRedis struct {
	values map[string]string
	err    error
}

func newFakeRedis() *fakeRedis { return &fakeRedis{values: map[string]string{}} }

func (f *fakeRedis) SetNX(_ context.Context, key string, value interface{}, _ time.Duration) *redis.BoolCmd {
	if f.err != nil {
		return redis.NewBoolResult(false, f.err)
	}
	if _, exists := f.values[key]; exists {
		return redis.NewBoolResult(false, nil)
	}
	switch v := value.(type) {
	case []byte:
		f.values[key] = string(v)
	default:
		return redis.NewBoolResult(false, errors.New("unexpected value type"))
	}
	return redis.NewBoolResult(true, nil)
}

func (f *fakeRedis) Get(_ context.Context, key string) *redis.StringCmd {
	if f.err != nil {
		return redis.NewStringResult("", f.err)
	}
	v, ok := f.values[key]
	if !ok {
		return redis.NewStringResult("", redis.Nil)
	}
	return redis.NewStringResult(v, nil)
}

func testOrder() *pb.Order {
	return &pb.Order{
		OrderId:            "3f6c1a2e-9b7d-11ef-8c1a-0242ac120002",
		ShippingTrackingId: "QK-48512-2407182",
		ShippingCost:       &pb.Money{CurrencyCode: "USD", Units: 8, Nanos: 990000000},
		ShippingAddress: &pb.Address{
			StreetAddress: "1600 Amphitheatre Parkway",
			City:          "Mountain View",
			State:         "CA",
			Country:       "United States",
			ZipCode:       94043,
		},
		Items: []*pb.OrderItem{
			{
				Item: &pb.CartItem{ProductId: "OLJCESPC7Z", Quantity: 2},
				Cost: &pb.Money{CurrencyCode: "USD", Units: 19, Nanos: 990000000},
			},
		},
		PurchasedAtUnix: 1790035200,
		Total:           &pb.Money{CurrencyCode: "USD", Units: 48, Nanos: 970000000},
	}
}

// Un pedido guardado se recupera íntegro: mismos artículos, costos y fecha.
func TestSaveAndGetRoundTrip(t *testing.T) {
	s := newStore(newFakeRedis())
	want := testOrder()

	created, err := s.save(context.Background(), want)
	if err != nil || !created {
		t.Fatalf("save() = %v, %v; want true, nil", created, err)
	}

	got, err := s.get(context.Background(), want.GetShippingTrackingId())
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	if !proto.Equal(got, want) {
		t.Errorf("el pedido recuperado no es igual al guardado:\n got = %v\nwant = %v", got, want)
	}
}

// Guardar dos veces el mismo número deja un solo pedido y no devuelve error.
func TestSaveIsIdempotent(t *testing.T) {
	f := newFakeRedis()
	s := newStore(f)
	order := testOrder()

	if created, err := s.save(context.Background(), order); err != nil || !created {
		t.Fatalf("primer save() = %v, %v; want true, nil", created, err)
	}

	// El reintento trae el mismo número de rastreo pero otro contenido:
	// no debe pisar lo ya guardado.
	retry := testOrder()
	retry.Items[0].Item.Quantity = 99
	created, err := s.save(context.Background(), retry)
	if err != nil {
		t.Fatalf("segundo save() error = %v; want nil", err)
	}
	if created {
		t.Error("segundo save() = true; want false (el pedido ya existía)")
	}
	if len(f.values) != 1 {
		t.Errorf("quedaron %d llaves en redis; want 1", len(f.values))
	}

	got, err := s.get(context.Background(), order.GetShippingTrackingId())
	if err != nil {
		t.Fatalf("get() error = %v", err)
	}
	if !proto.Equal(got, order) {
		t.Error("el reintento sobrescribió el pedido original")
	}
}

// Un número que no existe es ErrNotFound, no un error interno.
func TestGetMissingIsErrNotFound(t *testing.T) {
	s := newStore(newFakeRedis())
	if _, err := s.get(context.Background(), "NO-1-2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get() error = %v; want ErrNotFound", err)
	}
}

func TestGetEmptyTrackingID(t *testing.T) {
	s := newStore(newFakeRedis())
	_, err := s.get(context.Background(), "")
	if err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("get(\"\") error = %v; want un error que no sea ErrNotFound", err)
	}
}

func TestSaveWithoutTrackingID(t *testing.T) {
	s := newStore(newFakeRedis())
	order := testOrder()
	order.ShippingTrackingId = ""
	if _, err := s.save(context.Background(), order); err == nil {
		t.Fatal("save() sin número de rastreo = nil; want error")
	}
}

// Un fallo de Redis se propaga como error, no se confunde con "no existe".
func TestRedisFailureIsNotNotFound(t *testing.T) {
	f := newFakeRedis()
	f.err = errors.New("connection refused")
	s := newStore(f)

	if _, err := s.get(context.Background(), "QK-1-2"); err == nil || errors.Is(err, ErrNotFound) {
		t.Fatalf("get() error = %v; want un error de redis", err)
	}
	if _, err := s.save(context.Background(), testOrder()); err == nil {
		t.Fatal("save() con redis caído = nil; want error")
	}
}

// Lo guardado es JSON legible, con los nombres del proto, sin estado
// ni nombres de producto (ADR 0004 y 0005).
func TestStoredValueShape(t *testing.T) {
	f := newFakeRedis()
	s := newStore(f)
	order := testOrder()
	if _, err := s.save(context.Background(), order); err != nil {
		t.Fatalf("save() error = %v", err)
	}

	raw := f.values[order.GetShippingTrackingId()]
	for _, want := range []string{"shipping_tracking_id", "purchased_at_unix", "product_id", "total"} {
		if !strings.Contains(raw, want) {
			t.Errorf("el valor guardado no trae %q: %s", want, raw)
		}
	}
	for _, unwanted := range []string{"status", "ORDER_STATUS", "timeline", "Vintage", "product_name"} {
		if strings.Contains(raw, unwanted) {
			t.Errorf("el valor guardado trae %q y no debería: %s", unwanted, raw)
		}
	}
}
