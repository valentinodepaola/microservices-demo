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
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

const (
	testTrackingID  = "AB-123-4567890"
	testKnownProd   = "OLJCESPC7Z"
	testMissingProd = "YA-NO-EXISTE"
	testPurchasedAt = int64(1760000000)
)

func quietLogger() logrus.FieldLogger {
	l := logrus.New()
	l.Out = io.Discard
	return l
}

func testOrder() *pb.TrackedOrder {
	return &pb.TrackedOrder{
		ShippingTrackingId: testTrackingID,
		Status:             pb.OrderStatus_ORDER_STATUS_IN_TRANSIT,
		PurchasedAtUnix:    testPurchasedAt,
		Total:              &pb.Money{CurrencyCode: "USD", Units: 50},
		Items: []*pb.OrderItem{
			{Item: &pb.CartItem{ProductId: testKnownProd, Quantity: 2}, Cost: &pb.Money{CurrencyCode: "USD", Units: 19, Nanos: 990000000}},
			{Item: &pb.CartItem{ProductId: testMissingProd, Quantity: 1}, Cost: &pb.Money{CurrencyCode: "USD", Units: 5}},
		},
		Timeline: []*pb.StatusStep{
			{Status: pb.OrderStatus_ORDER_STATUS_CREATED, StartsAtUnix: testPurchasedAt},
			{Status: pb.OrderStatus_ORDER_STATUS_PAID, StartsAtUnix: testPurchasedAt + 30},
			{Status: pb.OrderStatus_ORDER_STATUS_PREPARING, StartsAtUnix: testPurchasedAt + 90},
			{Status: pb.OrderStatus_ORDER_STATUS_IN_TRANSIT, StartsAtUnix: testPurchasedAt + 180},
			{Status: pb.OrderStatus_ORDER_STATUS_DELIVERED, StartsAtUnix: testPurchasedAt + 300},
		},
	}
}

func fakeLookup(_ context.Context, id string) (*pb.Product, error) {
	if id == testKnownProd {
		return &pb.Product{Id: id, Name: "Sunglasses", Picture: "/static/img/products/sunglasses.jpg"}, nil
	}
	return nil, status.Error(codes.NotFound, "no product")
}

func TestTrackingStatusLabel(t *testing.T) {
	want := map[pb.OrderStatus]string{
		pb.OrderStatus_ORDER_STATUS_CREATED:     "Creado",
		pb.OrderStatus_ORDER_STATUS_PAID:        "Pagado",
		pb.OrderStatus_ORDER_STATUS_PREPARING:   "En preparación",
		pb.OrderStatus_ORDER_STATUS_IN_TRANSIT:  "En tránsito",
		pb.OrderStatus_ORDER_STATUS_DELIVERED:   "Entregado",
		pb.OrderStatus_ORDER_STATUS_UNSPECIFIED: "Desconocido",
	}
	for s, label := range want {
		if got := trackingStatusLabel(s); got != label {
			t.Errorf("trackingStatusLabel(%v) = %q, want %q", s, got, label)
		}
	}
}

func TestBuildTrackingView(t *testing.T) {
	now := time.Unix(testPurchasedAt+200, 0) // ya en tránsito, aún no entregado
	v := buildTrackingView(context.Background(), quietLogger(), testOrder(), now, fakeLookup)

	if v.TrackingID != testTrackingID || v.StatusLabel != "En tránsito" || v.Delivered {
		t.Errorf("header = %q / %q / delivered=%v", v.TrackingID, v.StatusLabel, v.Delivered)
	}
	if v.PurchasedAt != time.Unix(testPurchasedAt, 0).UTC().Format(trackingTimeLayout) {
		t.Errorf("PurchasedAt = %q", v.PurchasedAt)
	}
	if v.Total.GetUnits() != 50 {
		t.Errorf("Total = %v", v.Total)
	}

	if len(v.Items) != 2 {
		t.Fatalf("got %d items, want 2", len(v.Items))
	}
	known := v.Items[0]
	if known.Product == nil || known.Product.GetName() != "Sunglasses" || known.Quantity != 2 {
		t.Errorf("known item = %+v", known)
	}
	// 2 × 19.99 = 39.98
	if known.Price.GetUnits() != 39 || known.Price.GetNanos() != 980000000 {
		t.Errorf("line price = %v, want 39.98", known.Price)
	}
	missing := v.Items[1]
	if missing.Product != nil || missing.ProductID != testMissingProd {
		t.Errorf("missing item should degrade to its product_id, got %+v", missing)
	}

	if len(v.Timeline) != 5 {
		t.Fatalf("got %d steps, want 5", len(v.Timeline))
	}
	for i, s := range v.Timeline {
		wantReached := i <= 3
		if s.Reached != wantReached {
			t.Errorf("step %d (%s) reached = %v, want %v", i, s.Label, s.Reached, wantReached)
		}
		if s.Current != (i == 3) {
			t.Errorf("step %d (%s) current = %v", i, s.Label, s.Current)
		}
	}
}

func TestTrackingResult(t *testing.T) {
	cases := []struct {
		err  error
		code int
	}{
		{nil, http.StatusOK},
		{status.Error(codes.NotFound, "x"), http.StatusNotFound},
		{status.Error(codes.InvalidArgument, "x"), http.StatusNotFound},
		{status.Error(codes.Unavailable, "x"), http.StatusServiceUnavailable},
		{status.Error(codes.DeadlineExceeded, "x"), http.StatusServiceUnavailable},
		{errors.New("boom"), http.StatusServiceUnavailable},
	}
	for _, c := range cases {
		if got, _ := trackingResult(testTrackingID, c.err); got != c.code {
			t.Errorf("trackingResult(%v) = %d, want %d", c.err, got, c.code)
		}
	}
}

// --- Handler de punta a punta contra orderservice y catálogo falsos ---

type fakeOrderService struct {
	pb.UnimplementedOrderServiceServer
}

func (fakeOrderService) GetOrderByTrackingId(_ context.Context, req *pb.GetOrderByTrackingIdRequest) (*pb.TrackedOrder, error) {
	switch req.GetTrackingId() {
	case testTrackingID:
		return testOrder(), nil
	case "CAIDO":
		return nil, status.Error(codes.Unavailable, "redis down")
	default:
		return nil, status.Error(codes.NotFound, "no order")
	}
}

type fakeCatalog struct {
	pb.UnimplementedProductCatalogServiceServer
}

func (fakeCatalog) GetProduct(ctx context.Context, req *pb.GetProductRequest) (*pb.Product, error) {
	return fakeLookup(ctx, req.GetId())
}

func bufConn(t *testing.T, register func(*grpc.Server)) *grpc.ClientConn {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	srv := grpc.NewServer()
	register(srv)
	go srv.Serve(lis)
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func TestTrackingHandler(t *testing.T) {
	fe := &frontendServer{
		orderSvcConn: bufConn(t, func(s *grpc.Server) { pb.RegisterOrderServiceServer(s, fakeOrderService{}) }),
		productCatalogSvcConn: bufConn(t, func(s *grpc.Server) {
			pb.RegisterProductCatalogServiceServer(s, fakeCatalog{})
		}),
	}

	cases := []struct {
		name     string
		query    string
		code     int
		contains []string
		absent   []string
	}{
		{"primera visita", "", http.StatusOK, []string{`name="id"`}, []string{`role="alert"`}},
		{"campo vacío", "?id=++", http.StatusBadRequest, []string{msgTrackingEmpty}, nil},
		{"no existe", "?id=NO-EXISTE", http.StatusNotFound, []string{"No encontramos ningún pedido con el número NO-EXISTE"}, nil},
		{"servicio caído", "?id=CAIDO", http.StatusServiceUnavailable, []string{msgTrackingUnavailable}, nil},
		{"pedido real", "?id=" + testTrackingID, http.StatusOK,
			[]string{testTrackingID, "En tránsito", "Sunglasses", testMissingProd, "Producto no disponible", "Entregado"},
			[]string{`role="alert"`}},
		{"escapa el número", "?id=%3Cscript%3E", http.StatusNotFound, []string{"&lt;script&gt;"}, []string{"<script>"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/rastreo"+c.query, nil)
			req = req.WithContext(context.WithValue(req.Context(), ctxKeyLog{}, quietLogger()))
			rec := httptest.NewRecorder()
			fe.trackingHandler(rec, req)

			if rec.Code != c.code {
				t.Errorf("status = %d, want %d", rec.Code, c.code)
			}
			body := rec.Body.String()
			for _, s := range c.contains {
				if !strings.Contains(body, s) {
					t.Errorf("body does not contain %q", s)
				}
			}
			for _, s := range c.absent {
				if strings.Contains(body, s) {
					t.Errorf("body should not contain %q", s)
				}
			}
		})
	}
}

// La vista nunca debe llevar dirección ni correo (ADR 0009): TrackedOrder ya
// no los trae, y trackingOrderView no tiene dónde ponerlos.
func TestTrackingViewHasNoPersonalData(t *testing.T) {
	v := buildTrackingView(context.Background(), quietLogger(), testOrder(), time.Now(), fakeLookup)
	s := strings.ToLower(strings.Join([]string{v.TrackingID, v.StatusLabel, v.PurchasedAt}, " "))
	for _, bad := range []string{"@", "street", "calle"} {
		if strings.Contains(s, bad) {
			t.Errorf("view leaks %q", bad)
		}
	}
}
