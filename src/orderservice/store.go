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
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/protobuf/encoding/protojson"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// ErrNotFound lo devuelve get cuando el número de rastreo no existe en Redis.
// No es una falla del sistema: un pedido inexistente es una respuesta normal.
var ErrNotFound = errors.New("order not found")

// noExpiration deja la llave sin tiempo de vida. Los pedidos se pueden consultar
// durante toda la vida del pod de redis-orders, que usa emptyDir y no es permanente.
const noExpiration time.Duration = 0

// redisClient es el pedacito de go-redis que usa el store. Tenerlo como interfaz
// permite probar la serialización y la idempotencia sin levantar Redis.
type redisClient interface {
	SetNX(ctx context.Context, key string, value interface{}, expiration time.Duration) *redis.BoolCmd
	Get(ctx context.Context, key string) *redis.StringCmd
}

// El pedido se guarda como JSON generado desde el proto, con los nombres de campo
// tal cual los declara demo.proto, para poder leerlo con `redis-cli GET`.
// Ver docs/modelo-del-pedido.md §1 (D1).
var (
	marshalOrder   = protojson.MarshalOptions{UseProtoNames: true}
	unmarshalOrder = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// store escribe y lee pedidos en Redis. Un pedido completo es un solo valor
// bajo la llave de su número de rastreo (ADR 0004).
type store struct {
	rdb redisClient
}

func newStore(rdb redisClient) *store {
	return &store{rdb: rdb}
}

// save guarda el pedido bajo su número de rastreo y devuelve si lo creó.
// Es idempotente: si la llave ya existe no la pisa y devuelve false, sin error.
// La exclusión la hace Redis con SET NX, en una sola operación, para que dos
// reintentos de checkoutservice casi simultáneos no se pisen entre sí.
func (s *store) save(ctx context.Context, order *pb.Order) (bool, error) {
	trackingID := order.GetShippingTrackingId()
	if trackingID == "" {
		return false, errors.New("order has no shipping tracking id")
	}

	data, err := marshalOrder.Marshal(order)
	if err != nil {
		return false, fmt.Errorf("could not serialize order %q: %w", trackingID, err)
	}

	created, err := s.rdb.SetNX(ctx, trackingID, data, noExpiration).Result()
	if err != nil {
		return false, fmt.Errorf("could not write order %q: %w", trackingID, err)
	}
	return created, nil
}

// get devuelve el pedido guardado bajo ese número de rastreo,
// o ErrNotFound si no existe.
func (s *store) get(ctx context.Context, trackingID string) (*pb.Order, error) {
	if trackingID == "" {
		return nil, errors.New("empty tracking id")
	}

	data, err := s.rdb.Get(ctx, trackingID).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("could not read order %q: %w", trackingID, err)
	}

	order := &pb.Order{}
	if err := unmarshalOrder.Unmarshal(data, order); err != nil {
		return nil, fmt.Errorf("could not deserialize order %q: %w", trackingID, err)
	}
	return order, nil
}
