package secrets

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestMasterKeyAndBox(t *testing.T) {
	path := filepath.Join(t.TempDir(), "master.key")
	if err := GenerateMasterKey(path); err != nil {
		t.Fatal(err)
	}
	if fi, err := os.Stat(path); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("key file mode: %v %v", fi.Mode().Perm(), err)
	}
	key, err := LoadMasterKey(path)
	if err != nil {
		t.Fatal(err)
	}
	box, err := NewBox(key)
	if err != nil {
		t.Fatal(err)
	}

	blob, err := box.Encrypt([]byte("hunter2"), aad(1))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("hunter2")) {
		t.Fatal("plaintext visible in ciphertext")
	}
	pt, err := box.Decrypt(blob, aad(1))
	if err != nil || string(pt) != "hunter2" {
		t.Fatalf("decrypt = %q, %v", pt, err)
	}
	// AAD binds the blob to its device: decrypting as another device must fail.
	if _, err := box.Decrypt(blob, aad(2)); err == nil {
		t.Fatal("decrypt with wrong AAD should fail")
	}
}
