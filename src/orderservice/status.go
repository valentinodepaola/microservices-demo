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
	"fmt"
	"math"
	"os"
	"time"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
)

// Durations es cuánto dura cada etapa del pedido. ENTREGADO no aparece
// porque es absorbente: una vez alcanzado, ahí se queda (ADR 0005).
// Ver docs/modelo-del-pedido.md §5.
type Durations struct {
	Created   time.Duration
	Paid      time.Duration
	Preparing time.Duration
	InTransit time.Duration
}

// defaultDurations es el modo revisión: el ciclo completo dura casi un día.
// El modo demostración (5 minutos) se aplica desde Kustomize en #28.
var defaultDurations = Durations{
	Created:   5 * time.Minute,
	Paid:      30 * time.Minute,
	Preparing: 3 * time.Hour,
	InTransit: 20 * time.Hour,
}

const (
	minDuration = time.Second
	// Tope de cordura: una etapa más larga que un año es casi seguro un dedazo,
	// y además mantiene las fronteras lejos de cualquier desbordamiento.
	maxDuration = 365 * 24 * time.Hour
)

// durationEnv es el nombre de la variable de entorno de cada etapa.
var durationEnv = []struct {
	name  string
	field func(*Durations) *time.Duration
}{
	{"ORDER_CREATED_DURATION", func(d *Durations) *time.Duration { return &d.Created }},
	{"ORDER_PAID_DURATION", func(d *Durations) *time.Duration { return &d.Paid }},
	{"ORDER_PREPARING_DURATION", func(d *Durations) *time.Duration { return &d.Preparing }},
	{"ORDER_IN_TRANSIT_DURATION", func(d *Durations) *time.Duration { return &d.InTransit }},
}

// loadDurations lee las cuatro variables de entorno. Una variable ausente toma
// su default; una presente pero inválida es un error, para que el servicio no
// arranque con un valor silencioso que nadie note.
func loadDurations(lookup func(string) (string, bool)) (Durations, error) {
	d := defaultDurations
	for _, e := range durationEnv {
		raw, ok := lookup(e.name)
		if !ok || raw == "" {
			continue
		}
		value, err := time.ParseDuration(raw)
		if err != nil {
			return Durations{}, fmt.Errorf("%s=%q is not a valid duration: %w", e.name, raw, err)
		}
		if value < minDuration || value > maxDuration {
			return Durations{}, fmt.Errorf("%s=%q must be between %v and %v", e.name, raw, minDuration, maxDuration)
		}
		if value%time.Second != 0 {
			return Durations{}, fmt.Errorf("%s=%q must be a whole number of seconds", e.name, raw)
		}
		*e.field(&d) = value
	}
	return d, nil
}

func durationsFromEnv() (Durations, error) { return loadDurations(os.LookupEnv) }

// boundaries son los segundos acumulados en que termina cada etapa:
// T1 = d1, T2 = d1+d2, T3 = d1+d2+d3, T4 = d1+d2+d3+d4.
func (d Durations) boundaries() [4]int64 {
	t1 := int64(d.Created / time.Second)
	t2 := t1 + int64(d.Paid/time.Second)
	t3 := t2 + int64(d.Preparing/time.Second)
	t4 := t3 + int64(d.InTransit/time.Second)
	return [4]int64{t1, t2, t3, t4}
}

// StatusAt calcula el estado del pedido en un instante dado. Es pura: no toca
// Redis ni lee el reloj, para que las pruebas fijen el momento que quieran.
// Las fronteras son semiabiertas [inicio, fin): en el segundo exacto del cambio
// el pedido ya pertenece a la etapa siguiente (docs/modelo-del-pedido.md §4).
func StatusAt(purchasedAtUnix, nowUnix int64, d Durations) pb.OrderStatus {
	elapsed := elapsedSeconds(purchasedAtUnix, nowUnix)
	t := d.boundaries()

	switch {
	case elapsed < t[0]: // incluye fechas en el futuro (reloj desfasado)
		return pb.OrderStatus_ORDER_STATUS_CREATED
	case elapsed < t[1]:
		return pb.OrderStatus_ORDER_STATUS_PAID
	case elapsed < t[2]:
		return pb.OrderStatus_ORDER_STATUS_PREPARING
	case elapsed < t[3]:
		return pb.OrderStatus_ORDER_STATUS_IN_TRANSIT
	default:
		return pb.OrderStatus_ORDER_STATUS_DELIVERED
	}
}

// TimelineFor devuelve las cinco etapas en orden con el segundo en que empieza
// cada una. También es derivada: no se guarda nada de esto (ADR 0005).
func TimelineFor(purchasedAtUnix int64, d Durations) []*pb.StatusStep {
	t := d.boundaries()
	steps := []struct {
		status pb.OrderStatus
		offset int64
	}{
		{pb.OrderStatus_ORDER_STATUS_CREATED, 0},
		{pb.OrderStatus_ORDER_STATUS_PAID, t[0]},
		{pb.OrderStatus_ORDER_STATUS_PREPARING, t[1]},
		{pb.OrderStatus_ORDER_STATUS_IN_TRANSIT, t[2]},
		{pb.OrderStatus_ORDER_STATUS_DELIVERED, t[3]},
	}

	timeline := make([]*pb.StatusStep, 0, len(steps))
	for _, s := range steps {
		timeline = append(timeline, &pb.StatusStep{
			Status:       s.status,
			StartsAtUnix: purchasedAtUnix + s.offset,
		})
	}
	return timeline
}

// elapsedSeconds resta sin desbordar: con fechas absurdas satura en los
// extremos en vez de dar la vuelta y devolver un estado equivocado.
func elapsedSeconds(purchasedAtUnix, nowUnix int64) int64 {
	elapsed := nowUnix - purchasedAtUnix
	switch {
	case nowUnix > 0 && purchasedAtUnix < 0 && elapsed < 0:
		return math.MaxInt64
	case nowUnix < 0 && purchasedAtUnix > 0 && elapsed > 0:
		return math.MinInt64
	default:
		return elapsed
	}
}
