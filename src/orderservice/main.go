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
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/reflection"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/orderservice/genproto"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

const (
	defaultPort = "50051"

	// serviceName es el nombre que reporta el health check de readiness.
	// El nombre vacío ("") reporta solo que el proceso está vivo.
	serviceName = "hipstershop.OrderService"

	redisCheckInterval = 5 * time.Second
	redisPingTimeout   = 2 * time.Second
)

var log *logrus.Logger

func init() {
	log = logrus.New()
	log.Level = logrus.DebugLevel
	log.Formatter = &logrus.JSONFormatter{
		FieldMap: logrus.FieldMap{
			logrus.FieldKeyTime:  "timestamp",
			logrus.FieldKeyLevel: "severity",
			logrus.FieldKeyMsg:   "message",
		},
		TimestampFormat: time.RFC3339Nano,
	}
	log.Out = os.Stdout

	// go-redis trae su propio logger en texto plano; lo mandamos por logrus
	// para que todo lo que sale del servicio siga siendo JSON.
	redis.SetLogger(redisLogger{})
}

// redisLogger adapta los mensajes internos de go-redis al logger del servicio.
type redisLogger struct{}

func (redisLogger) Printf(_ context.Context, format string, v ...interface{}) {
	log.WithField("component", "go-redis").Debugf(format, v...)
}

func main() {
	redisAddr, err := requiredEnv("REDIS_ADDR")
	if err != nil {
		log.Fatal(err)
	}

	port := defaultPort
	if value, ok := os.LookupEnv("PORT"); ok {
		port = value
	}
	port = fmt.Sprintf(":%s", port)

	lis, err := net.Listen("tcp", port)
	if err != nil {
		log.Fatalf("failed to listen: %v", err)
	}

	rdb := redis.NewClient(&redis.Options{Addr: redisAddr})
	defer rdb.Close()

	srv := grpc.NewServer()
	svc := &server{rdb: rdb}
	pb.RegisterOrderServiceServer(srv, svc)

	// Dos estados en el mismo health check:
	//   ""                        -> liveness: el proceso responde. Siempre SERVING.
	//   "hipstershop.OrderService" -> readiness: puede atender pedidos. Depende de Redis.
	// Así, si Redis se cae, Kubernetes deja de mandarle tráfico pero no reinicia el pod.
	healthcheck := health.NewServer()
	healthpb.RegisterHealthServer(srv, healthcheck)
	healthcheck.SetServingStatus("", healthpb.HealthCheckResponse_SERVING)
	healthcheck.SetServingStatus(serviceName, healthpb.HealthCheckResponse_NOT_SERVING)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, os.Interrupt)
	defer stop()

	ping := func(ctx context.Context) error { return rdb.Ping(ctx).Err() }
	go watchRedis(ctx, ping, healthcheck, redisCheckInterval)

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		healthcheck.Shutdown()
		srv.GracefulStop()
	}()

	log.Infof("Order Service listening on port %s", port)

	// Register reflection service on gRPC server.
	reflection.Register(srv)
	if err := srv.Serve(lis); err != nil {
		log.Fatalf("failed to serve: %v", err)
	}
}

// server controls RPC service responses.
// Los RPC todavía no están implementados (eso es #23); el struct embebido
// responde codes.Unimplemented mientras tanto.
type server struct {
	pb.UnimplementedOrderServiceServer
	rdb *redis.Client
}

// requiredEnv regresa el valor de una variable de entorno obligatoria,
// o un error claro si no está definida.
func requiredEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("environment variable %q not set", key)
	}
	return v, nil
}

// watchRedis revisa Redis cada `every` y actualiza el estado de readiness.
// Solo escribe en el log cuando el estado cambia, para no llenarlo de ruido.
func watchRedis(ctx context.Context, ping func(context.Context) error, hs *health.Server, every time.Duration) {
	last := healthpb.HealthCheckResponse_UNKNOWN

	check := func() {
		pctx, cancel := context.WithTimeout(ctx, redisPingTimeout)
		err := ping(pctx)
		cancel()

		status := healthpb.HealthCheckResponse_SERVING
		if err != nil {
			status = healthpb.HealthCheckResponse_NOT_SERVING
		}
		if status == last {
			return
		}
		if err != nil {
			log.Warnf("redis unavailable, reporting NOT_SERVING: %v", err)
		} else {
			log.Info("redis reachable, reporting SERVING")
		}
		hs.SetServingStatus(serviceName, status)
		last = status
	}

	check()
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			check()
		}
	}
}
