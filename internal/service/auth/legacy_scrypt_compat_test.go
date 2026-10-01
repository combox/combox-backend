package auth

import "testing"

func TestVerifyWerkzeugCompatVector(t *testing.T) {
	// python hashlib.scrypt(b'testpass123', salt=b'HKyVS63GwMcRAbg', n=1024, r=8, p=1, dklen=32)
	plain := `scrypt:1024:8:1$HKyVS63GwMcRAbg$2ee79f5f23a8e2ac2139b4e4aee7e73f581992071c1ff8c3406c0fa7729596d9`
	if err := VerifyWerkzeugScrypt(plain, "testpass123"); err != nil {
		t.Fatalf("plain: %v", err)
	}
	if err := VerifyWerkzeugScrypt("!legacy!"+plain, "testpass123"); err != nil {
		t.Fatalf("prefixed: %v", err)
	}
	if err := VerifyWerkzeugScrypt("!legacy!"+plain, "wrong"); err == nil {
		t.Fatalf("wrong password accepted")
	}
}
