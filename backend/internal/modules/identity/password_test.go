package identity

import "testing"

func TestArgon2Verifier(t *testing.T) {
	encoded := HashPassword("long test password")
	if !CheckPassword(encoded, "long test password") || CheckPassword(encoded, "wrong") {
		t.Fatal("password verification failed")
	}
	for _, v := range []string{"", "broken", hashPrefix + "bad$bad", "$argon2id$v=19$m=999999999,t=3,p=1$a$b"} {
		if CheckPassword(v, "test") {
			t.Fatal("invalid verifier accepted")
		}
	}
	if encoded == HashPassword("long test password") {
		t.Fatal("salt must be random")
	}
}
