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
	"math"
	"testing"
	"time"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// demoDurations son los valores del modo demostración:
// fronteras acumuladas T1=30, T2=90, T3=180, T4=300 segundos.
var demoDurations = Durations{
	Created:   30 * time.Second,
	Paid:      60 * time.Second,
	Preparing: 90 * time.Second,
	InTransit: 120 * time.Second,
}

const purchasedAt int64 = 1790035200 // 2026-09-22 00:00:00 UTC

// La tabla es la de docs/modelo-del-pedido.md §4.3, caso por caso.
func TestStatusAt(t *testing.T) {
	tests := []struct {
		name    string
		elapsed int64
		want    pb.OrderStatus
	}{
		{"fecha futura, reloj desfasado", -10, pb.OrderStatus_ORDER_STATUS_CREATED},
		{"borde t=0", 0, pb.OrderStatus_ORDER_STATUS_CREATED},
		{"ultimo segundo de CREATED", 29, pb.OrderStatus_ORDER_STATUS_CREATED},
		{"borde frontera CREATED->PAID", 30, pb.OrderStatus_ORDER_STATUS_PAID},
		{"ultimo segundo de PAID", 89, pb.OrderStatus_ORDER_STATUS_PAID},
		{"frontera PAID->PREPARING", 90, pb.OrderStatus_ORDER_STATUS_PREPARING},
		{"ultimo segundo de PREPARING", 179, pb.OrderStatus_ORDER_STATUS_PREPARING},
		{"frontera PREPARING->IN_TRANSIT", 180, pb.OrderStatus_ORDER_STATUS_IN_TRANSIT},
		{"ultimo segundo de IN_TRANSIT", 299, pb.OrderStatus_ORDER_STATUS_IN_TRANSIT},
		{"frontera IN_TRANSIT->DELIVERED", 300, pb.OrderStatus_ORDER_STATUS_DELIVERED},
		{"mucho despues de la suma", 1000000, pb.OrderStatus_ORDER_STATUS_DELIVERED},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := StatusAt(purchasedAt, purchasedAt+tc.elapsed, demoDurations)
			if got != tc.want {
				t.Errorf("StatusAt(elapsed=%ds) = %v; want %v", tc.elapsed, got, tc.want)
			}
		})
	}
}

// El caso 12 de la tabla: el instante más grande representable no debe
// desbordar la resta y dar la vuelta.
func TestStatusAtDoesNotOverflow(t *testing.T) {
	if got := StatusAt(purchasedAt, math.MaxInt64-purchasedAt, demoDurations); got != pb.OrderStatus_ORDER_STATUS_DELIVERED {
		t.Errorf("StatusAt(now=MaxInt64-purchasedAt) = %v; want DELIVERED", got)
	}
	if got := StatusAt(math.MinInt64+1, math.MaxInt64, demoDurations); got != pb.OrderStatus_ORDER_STATUS_DELIVERED {
		t.Errorf("StatusAt(fecha minima, now maximo) = %v; want DELIVERED", got)
	}
	if got := StatusAt(math.MaxInt64, math.MinInt64+1, demoDurations); got != pb.OrderStatus_ORDER_STATUS_CREATED {
		t.Errorf("StatusAt(fecha maxima, now minimo) = %v; want CREATED", got)
	}
}

// Con los defaults (modo revisión) el ciclo dura casi un día.
func TestStatusAtWithDefaultDurations(t *testing.T) {
	tests := []struct {
		elapsed time.Duration
		want    pb.OrderStatus
	}{
		{1 * time.Minute, pb.OrderStatus_ORDER_STATUS_CREATED},
		{10 * time.Minute, pb.OrderStatus_ORDER_STATUS_PAID},
		{1 * time.Hour, pb.OrderStatus_ORDER_STATUS_PREPARING},
		{10 * time.Hour, pb.OrderStatus_ORDER_STATUS_IN_TRANSIT},
		{25 * time.Hour, pb.OrderStatus_ORDER_STATUS_DELIVERED},
	}
	for _, tc := range tests {
		now := purchasedAt + int64(tc.elapsed/time.Second)
		if got := StatusAt(purchasedAt, now, defaultDurations); got != tc.want {
			t.Errorf("StatusAt(elapsed=%v) = %v; want %v", tc.elapsed, got, tc.want)
		}
	}
}

// La línea de tiempo trae las cinco etapas en orden, con las fronteras
// acumuladas a partir de la fecha de compra.
func TestTimelineFor(t *testing.T) {
	timeline := TimelineFor(purchasedAt, demoDurations)
	if len(timeline) != 5 {
		t.Fatalf("len(timeline) = %d; want 5", len(timeline))
	}

	want := []struct {
		status pb.OrderStatus
		at     int64
	}{
		{pb.OrderStatus_ORDER_STATUS_CREATED, purchasedAt},
		{pb.OrderStatus_ORDER_STATUS_PAID, purchasedAt + 30},
		{pb.OrderStatus_ORDER_STATUS_PREPARING, purchasedAt + 90},
		{pb.OrderStatus_ORDER_STATUS_IN_TRANSIT, purchasedAt + 180},
		{pb.OrderStatus_ORDER_STATUS_DELIVERED, purchasedAt + 300},
	}
	for i, w := range want {
		if timeline[i].GetStatus() != w.status || timeline[i].GetStartsAtUnix() != w.at {
			t.Errorf("timeline[%d] = %v en %d; want %v en %d",
				i, timeline[i].GetStatus(), timeline[i].GetStartsAtUnix(), w.status, w.at)
		}
	}
}

// Cada etapa de la línea de tiempo empieza justo cuando StatusAt cambia:
// las dos funciones no pueden contradecirse.
func TestTimelineAgreesWithStatusAt(t *testing.T) {
	for _, step := range TimelineFor(purchasedAt, demoDurations) {
		if got := StatusAt(purchasedAt, step.GetStartsAtUnix(), demoDurations); got != step.GetStatus() {
			t.Errorf("en %d la linea de tiempo dice %v pero StatusAt dice %v",
				step.GetStartsAtUnix(), step.GetStatus(), got)
		}
	}
}

func TestLoadDurations(t *testing.T) {
	env := func(values map[string]string) func(string) (string, bool) {
		return func(key string) (string, bool) { v, ok := values[key]; return v, ok }
	}

	t.Run("sin variables usa los defaults", func(t *testing.T) {
		got, err := loadDurations(env(nil))
		if err != nil || got != defaultDurations {
			t.Fatalf("loadDurations() = %v, %v; want %v, nil", got, err, defaultDurations)
		}
	})

	t.Run("sobrescribe solo lo que viene", func(t *testing.T) {
		got, err := loadDurations(env(map[string]string{"ORDER_CREATED_DURATION": "30s"}))
		if err != nil {
			t.Fatalf("loadDurations() error = %v", err)
		}
		if got.Created != 30*time.Second {
			t.Errorf("Created = %v; want 30s", got.Created)
		}
		if got.Paid != defaultDurations.Paid {
			t.Errorf("Paid = %v; want el default %v", got.Paid, defaultDurations.Paid)
		}
	})

	invalid := []struct {
		name, value string
	}{
		{"texto que no es duracion", "cinco minutos"},
		{"sin unidad", "30"},
		{"cero", "0s"},
		{"negativa", "-5m"},
		{"mas corta que un segundo", "500ms"},
		{"no es segundos enteros", "1500ms"},
		{"mas de un anio", "9000h"},
	}
	for _, tc := range invalid {
		t.Run("invalida: "+tc.name, func(t *testing.T) {
			if _, err := loadDurations(env(map[string]string{"ORDER_PAID_DURATION": tc.value})); err == nil {
				t.Errorf("loadDurations(%q) = nil; want error", tc.value)
			}
		})
	}
}
