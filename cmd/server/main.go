package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	_ "github.com/lib/pq"
	"github.com/nurtw/escrow/internal/domain"
	"github.com/nurtw/escrow/internal/gateway"
	grpchandler "github.com/nurtw/escrow/internal/grpc"
	"github.com/nurtw/escrow/internal/repository"
	"github.com/nurtw/escrow/internal/resolver"
	"github.com/nurtw/escrow/internal/security"
	"github.com/nurtw/escrow/internal/service"
	waybillv1 "github.com/nurtw/escrow/proto/v1"
	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

const (
	unclaimedTransitTimeout = 24 * time.Hour
	unclaimedSweepInterval  = time.Minute
)

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func enableCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// In-Memory fallback rate limiter matching security.PinRateLimiter interface exactly
type memoryPinRateLimiter struct {
	mu       sync.Mutex
	attempts map[string]int64
}

func newMemoryPinRateLimiter() *memoryPinRateLimiter {
	return &memoryPinRateLimiter{
		attempts: make(map[string]int64),
	}
}

func (m *memoryPinRateLimiter) RecordFailedAttempt(ctx context.Context, waybillID string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.attempts[waybillID]++
	return m.attempts[waybillID], nil
}

func (m *memoryPinRateLimiter) CheckLockout(ctx context.Context, waybillID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.attempts[waybillID] >= 3, nil
}

func (m *memoryPinRateLimiter) ResetAttempts(ctx context.Context, waybillID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.attempts, waybillID)
	return nil
}

func runUnclaimedWaybillSweep(ctx context.Context, repo *repository.EscrowRepository, done chan<- struct{}) {
	defer close(done)
	ticker := time.NewTicker(unclaimedSweepInterval)
	defer ticker.Stop()

	for {
		held, err := repo.HoldExpiredTransitWaybills(ctx, unclaimedTransitTimeout)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("[WAYBILL] Failed to process expired transit waybills: %v", err)
		} else if held > 0 {
			log.Printf("[WAYBILL] Moved %d expired transit waybill(s) into UNCLAIMED_HOLD", held)
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func main() {
	log.Println("Starting NURTW Escrow & Waybill Dual Engine (gRPC + HTTP REST)...")
	security.RequireJWTSecret()

	dbHost := getEnv("DB_HOST", "127.0.0.1")
	dbPort := getEnv("DB_PORT", "5432")
	dbUser := getEnv("DB_USER", "postgres")
	dbPass := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "nurtw_escrow")
	sslMode := getEnv("DB_SSLMODE", "disable")

	var dsn string
	if dbPass == "" {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s dbname=%s sslmode=%s",
			dbHost, dbPort, dbUser, dbName, sslMode,
		)
	} else {
		dsn = fmt.Sprintf(
			"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
			dbHost, dbPort, dbUser, dbPass, dbName, sslMode,
		)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Failed to open database connection: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(10)
	db.SetConnMaxLifetime(5 * time.Minute)

	if err := db.Ping(); err != nil {
		log.Fatalf("Database ping failed: %v", err)
	}
	log.Printf("Connected to PostgreSQL (nurtw_escrow) on %s:%s.", dbHost, dbPort)

	redisAddr := getEnv("REDIS_ADDR", "127.0.0.1:6379")
	rdb := redis.NewClient(&redis.Options{
		Addr: redisAddr,
	})

	var pinLimiter security.PinRateLimiter
	pingCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	if err := rdb.Ping(pingCtx).Err(); err != nil {
		log.Printf("[SECURITY] Redis not reachable on %s (%v). Using secure in-memory PIN limiter for local development.", redisAddr, err)
		pinLimiter = newMemoryPinRateLimiter()
	} else {
		log.Printf("[SECURITY] Connected to Redis on %s for distributed PIN brute-force defense.", redisAddr)
		pinLimiter = security.NewRedisPinRateLimiter(rdb)
	}

	repo := repository.NewEscrowRepository(db)
	serviceCtx, stopService := context.WithCancel(context.Background())
	unclaimedSweepDone := make(chan struct{})
	go runUnclaimedWaybillSweep(serviceCtx, repo, unclaimedSweepDone)
	nibssResolver := resolver.NewPaystackNibssResolver(7 * time.Second)
	payoutDisburser := resolver.NewPaystackPayoutDisburser(10 * time.Second)
	smsDispatcher := resolver.NewTermiiSMSDispatcher(8 * time.Second)
	calculator := domain.NewFeeCalculator()

	escrowSvc := service.NewEscrowService(repo, nibssResolver, calculator, payoutDisburser)

	grpcPort := getEnv("GRPC_PORT", "50051")
	grpcListener, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%s", grpcPort))
	if err != nil {
		log.Fatalf("Failed to bind gRPC port %s: %v", grpcPort, err)
	}
	grpcServer := grpc.NewServer()
	grpcHandler := grpchandler.NewWaybillGRPCHandler(repo, escrowSvc)
	waybillv1.RegisterWaybillServiceServer(grpcServer, grpcHandler)
	reflection.Register(grpcServer)

	go func() {
		log.Printf("[gRPC] Service listening on 0.0.0.0:%s", grpcPort)
		if err := grpcServer.Serve(grpcListener); err != nil {
			log.Fatalf("gRPC server error: %v", err)
		}
	}()

	httpPort := getEnv("HTTP_PORT", "8080")
	restHandler := gateway.NewRESTHandler(db, repo, escrowSvc, nibssResolver, smsDispatcher, pinLimiter)
	ussdHandler := gateway.NewUSSDHandler(escrowSvc, pinLimiter)

	mux := http.NewServeMux()

	// Public routes (no JWT)
	mux.HandleFunc("/api/v1/agent/register", restHandler.HandleRegister)
	mux.HandleFunc("/api/v1/agent/login", restHandler.HandleLogin)
	mux.HandleFunc("/api/v1/parks", restHandler.HandleListParks)
	mux.Handle("/api/v1/ussd", ussdHandler)
	mux.HandleFunc("/api/v1/waybills/cancel", security.AuthMiddleware(restHandler.HandleCancelWaybill))

	// Protected routes (JWT required)
	mux.HandleFunc("/api/v1/agent/profile", security.AuthMiddleware(restHandler.HandleAgentProfile))
	mux.HandleFunc("/api/v1/bank/resolve", security.AuthMiddleware(restHandler.HandleResolveDriver))
	mux.HandleFunc("/api/v1/waybills", security.AuthMiddleware(restHandler.HandleCreateWaybill))
	mux.HandleFunc("/api/v1/waybills/verify-pin", security.AuthMiddleware(restHandler.HandleVerifyPin))
	mux.HandleFunc("/api/v1/waybills/start-transit", security.AuthMiddleware(restHandler.HandleStartTransit))
	mux.HandleFunc("/api/v1/waybills/report-refusal", security.AuthMiddleware(restHandler.HandleReportReceiverRefusal))
	mux.HandleFunc("/api/v1/waybills/disburse", security.AuthMiddleware(restHandler.HandleDisburse))
	mux.HandleFunc("/api/v1/admin/waybills/resolve", security.AuthMiddleware(restHandler.HandleResolveHeldWaybill))
	mux.HandleFunc("/api/v1/admin/settlement/summary", security.AuthMiddleware(restHandler.HandleSettlementSummary))
	mux.HandleFunc("/api/v1/waybills/status", security.AuthMiddleware(restHandler.HandleWaybillStatus))
	mux.HandleFunc("/api/v1/agent/waybills", security.AuthMiddleware(restHandler.HandleListAgentWaybills))
	mux.HandleFunc("/api/v1/agent/fund-wallet", security.AuthMiddleware(restHandler.HandleFundWallet))
	mux.HandleFunc("/api/v1/parks/create", security.AuthMiddleware(restHandler.HandleCreatePark))

	// Agent PWA (static files from ./web)
	webDir := getEnv("WEB_DIR", "web")
	fileServer := http.FileServer(http.Dir(webDir))
	mux.Handle("/app/", http.StripPrefix("/app/", fileServer))

	httpServer := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%s", httpPort),
		Handler:      enableCORS(mux),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	go func() {
		log.Printf("[HTTP REST & USSD] Gateway listening on 0.0.0.0:%s", httpPort)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	shutdownChan := make(chan os.Signal, 1)
	signal.Notify(shutdownChan, os.Interrupt, syscall.SIGTERM)

	<-shutdownChan
	log.Println("Shutting down servers gracefully...")
	stopService()
	<-unclaimedSweepDone
	grpcServer.GracefulStop()
	_ = httpServer.Close()
	log.Println("All services stopped.")
}
