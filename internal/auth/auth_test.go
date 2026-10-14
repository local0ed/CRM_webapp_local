package auth

import (
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	hash, err := HashPassword("password-1234")
	if err != nil {
		t.Fatal(err)
	}
	if len(hash) < 10 || hash[:10] != "$argon2id$" {
		t.Fatalf("format ไม่ใช่ PHC argon2id: %q", hash)
	}
	if !VerifyPassword(hash, "password-1234") {
		t.Fatal("รหัสผ่านถูกต้องต้อง verify ผ่าน")
	}
	if VerifyPassword(hash, "wrong-password") {
		t.Fatal("รหัสผ่านผิดต้อง verify ไม่ผ่าน")
	}
}

func TestBadHashFormat(t *testing.T) {
	if VerifyPassword("not-a-hash", "x") {
		t.Fatal("hash format ผิดต้องคืน false")
	}
	if VerifyPassword("$argon2id$garbage", "x") {
		t.Fatal("hash format ผิดต้องคืน false")
	}
}

func TestUniqueSalts(t *testing.T) {
	a, _ := HashPassword("same")
	b, _ := HashPassword("same")
	if a == b {
		t.Fatal("salt ต้องสุ่ม hash ของรหัสเดียวกันไม่ควรเหมือนกัน")
	}
}

func TestSessions(t *testing.T) {
	s := NewSessions(50 * time.Millisecond)
	token := s.New("somchai", "admin")

	info, ok := s.Get(token)
	if !ok || info.Username != "somchai" || info.Role != "admin" {
		t.Fatalf("Get ต้องได้ session ที่สร้างไว้ ได้ %v ok=%v", info, ok)
	}

	// sliding expiry — Get ก่อนหมดอายุ ต้องต่ออายุให้
	time.Sleep(30 * time.Millisecond)
	if _, ok := s.Get(token); !ok {
		t.Fatal("Get ระหว่างอายุต้องยังใช้ได้")
	}
	// ถ้าไม่มี sliding จะหมดอายุที่ 50ms แต่ต่ออายุแล้ว จึงยังอยู่อีกช่วง
	time.Sleep(30 * time.Millisecond)
	if _, ok := s.Get(token); !ok {
		t.Fatal("sliding expiry ควรต่ออายุ session")
	}

	// token แปลกปลอม
	if _, ok := s.Get("fake-token"); ok {
		t.Fatal("token ปลอมต้องไม่ผ่าน")
	}

	// รอให้หมดอายุจริง (ล่าสุด Get เมื่อครู่)
	time.Sleep(60 * time.Millisecond)
	if _, ok := s.Get(token); ok {
		t.Fatal("หมดอายุแล้วต้องไม่ผ่าน")
	}

	// logout
	token2 := s.New("somying", "viewer")
	s.Delete(token2)
	if _, ok := s.Get(token2); ok {
		t.Fatal("ลบแล้วต้องหาไม่เจอ")
	}
}

func TestRateLimiter(t *testing.T) {
	r := NewRateLimiter(3, time.Second)
	for i := 0; i < 3; i++ {
		if !r.Allow("1.2.3.4") {
			t.Fatalf("ครั้งที่ %d ยังไม่เกินโควตา ต้อง Allow", i+1)
		}
	}
	if r.Allow("1.2.3.4") {
		t.Fatal("เกินโควตา 3 ครั้งแล้ว ต้องปฏิเสธ")
	}
	if !r.Allow("5.6.7.8") {
		t.Fatal("คนละ key ต้องไม่โดนจำกัด")
	}

	// sliding window — รอ window หมดแล้ว Allow อีกได้
	r2 := NewRateLimiter(1, 50*time.Millisecond)
	if !r2.Allow("ip") {
		t.Fatal("ครั้งแรกต้องผ่าน")
	}
	if r2.Allow("ip") {
		t.Fatal("ครั้งที่สองติดกันต้องถูกปฏิเสธ")
	}
	time.Sleep(60 * time.Millisecond)
	if !r2.Allow("ip") {
		t.Fatal("หลัง window หมดต้อง Allow อีกได้")
	}
}
