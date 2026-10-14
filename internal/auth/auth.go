// Package auth รหัสผ่านผู้ใช้ระบบ (Argon2id) + session in-memory + rate limit
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/argon2"
)

const (
	argonMemory  uint32 = 64 * 1024 // 64MB
	argonTime    uint32 = 2
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
)

// HashPassword — Argon2id (memory 64MB, time 2, threads 2) เข้ารหัสในรูปแบบ PHC string "$argon2id$..."
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	// PHC: base64 แบบไม่มี padding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword — ตรวจรหัสผ่านกับ hash แบบ constant-time, hash format ผิดคืน false
func VerifyPassword(hash, password string) bool {
	parts := strings.Split(hash, "$") // ["", "argon2id", "v=19", "m=...,t=...,p=...", salt, hash]
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// SessionInfo ข้อมูล session ของผู้ใช้ที่ล็อกอินอยู่
// (json tags ให้ key ออกมาเป็นตัวเล็ก เพื่อให้หน้าเว็บอ่าน me.username/me.role ได้)
type SessionInfo struct {
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expires_at"`
}

type sessionEntry struct {
	info      *SessionInfo
	expiresAt time.Time
}

// Sessions เก็บ session แบบ in-memory (token → ข้อมูลผู้ใช้)
type Sessions struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]*sessionEntry
}

// NewSessions สร้างที่เก็บ session พร้อม goroutine เก็บกวาด session หมดอายุทุก 1 ชั่วโมง
func NewSessions(ttl time.Duration) *Sessions {
	s := &Sessions{
		ttl:     ttl,
		entries: make(map[string]*sessionEntry),
	}
	go func() {
		ticker := time.NewTicker(time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			now := time.Now()
			s.mu.Lock()
			for token, e := range s.entries {
				if now.After(e.expiresAt) {
					delete(s.entries, token)
				}
			}
			s.mu.Unlock()
		}
	}()
	return s
}

// New สร้าง session ใหม่ คืน token (random 32 bytes base64url)
func (s *Sessions) New(username, role string) string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(err) // crypto/rand พัง = ระบบใช้งานไม่ได้
	}
	token := base64.RawURLEncoding.EncodeToString(buf)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[token] = &sessionEntry{
		info:      &SessionInfo{Username: username, Role: role, ExpiresAt: time.Now().Add(s.ttl)},
		expiresAt: time.Now().Add(s.ttl),
	}
	return token
}

// Get คืนข้อมูล session ถ้า token ใช้ได้ (ยังไม่หมดอายุ); อัปเดต sliding expiry
func (s *Sessions) Get(token string) (*SessionInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[token]
	if !ok || time.Now().After(e.expiresAt) {
		if ok {
			delete(s.entries, token) // หมดอายุ ลบทิ้งเลย
		}
		return nil, false
	}
	e.expiresAt = time.Now().Add(s.ttl) // sliding expiry
	e.info.ExpiresAt = e.expiresAt
	return e.info, true
}

// Delete ลบ session (logout)
func (s *Sessions) Delete(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, token)
}

// RateLimiter จำกัดจำนวนครั้งต่อ key ภายใน window (sliding window in-memory)
type RateLimiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

// NewRateLimiter จำกัด max ครั้ง ต่อ window ต่อ key
func NewRateLimiter(max int, window time.Duration) *RateLimiter {
	return &RateLimiter{
		max:    max,
		window: window,
		hits:   make(map[string][]time.Time),
	}
}

// Allow คืน true ถ้า key ยังเรียกได้ใน window นี้
func (r *RateLimiter) Allow(key string) bool {
	now := time.Now()
	r.mu.Lock()
	defer r.mu.Unlock()

	// ตัดรายการเก่าที่หลุด window ออก
	recent := r.hits[key][:0]
	for _, t := range r.hits[key] {
		if now.Sub(t) <= r.window {
			recent = append(recent, t)
		}
	}
	r.hits[key] = recent

	if len(recent) >= r.max {
		return false
	}
	r.hits[key] = append(r.hits[key], now)
	return true
}
