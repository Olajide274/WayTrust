package security

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var (
	ErrMaxPinAttemptsExceeded = errors.New("MAX_ATTEMPTS_EXCEEDED: Waybill locked due to 3 failed PIN attempts")
)

type PinRateLimiter interface {
	RecordFailedAttempt(ctx context.Context, waybillID string) (int64, error)
	ResetAttempts(ctx context.Context, waybillID string) error
	CheckLockout(ctx context.Context, waybillID string) (bool, error)
}

type RedisPinRateLimiter struct {
	client     *redis.Client
	maxRetries int64
	lockoutTTL time.Duration
}

func NewRedisPinRateLimiter(client *redis.Client) *RedisPinRateLimiter {
	return &RedisPinRateLimiter{
		client:     client,
		maxRetries: 3,
		lockoutTTL: 30 * time.Minute,
	}
}

// RecordFailedAttempt increments the failed PIN counter and sets a security TTL
func (l *RedisPinRateLimiter) RecordFailedAttempt(ctx context.Context, waybillID string) (int64, error) {
	if l.client == nil {
		return 0, nil
	}

	key := fmt.Sprintf("pin_attempts:%s", waybillID)

	pipe := l.client.Pipeline()
	incrCmd := pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, l.lockoutTTL)
	_, err := pipe.Exec(ctx)
	if err != nil {
		return 0, fmt.Errorf("redis rate-limiter failed: %w", err)
	}

	attempts := incrCmd.Val()
	if attempts >= l.maxRetries {
		return attempts, ErrMaxPinAttemptsExceeded
	}

	return attempts, nil
}

// ResetAttempts clears the failed attempt counter upon successful PIN authentication
func (l *RedisPinRateLimiter) ResetAttempts(ctx context.Context, waybillID string) error {
	if l.client == nil {
		return nil
	}
	key := fmt.Sprintf("pin_attempts:%s", waybillID)
	return l.client.Del(ctx, key).Err()
}

// CheckLockout verifies whether the waybill is currently locked out by brute-force protection
func (l *RedisPinRateLimiter) CheckLockout(ctx context.Context, waybillID string) (bool, error) {
	if l.client == nil {
		return false, nil
	}
	key := fmt.Sprintf("pin_attempts:%s", waybillID)
	val, err := l.client.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return val >= l.maxRetries, nil
}
