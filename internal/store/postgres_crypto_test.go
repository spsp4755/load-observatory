package store

import (
	"bytes"
	"testing"
)

func TestSnapshotProtectionEncryptsAndRestoresTargetAPIKeys(t *testing.T) {
	key := bytes.Repeat([]byte{7}, 32)
	state := []byte(`{"targets":{"target-1":{"api_key":"secret-key","name":"model"}}}`)
	protected, err := protectSnapshot(state, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(protected, []byte("secret-key")) {
		t.Fatalf("plaintext API key in protected state: %s", protected)
	}
	restored, err := unprotectSnapshot(protected, key)
	if err != nil || !bytes.Contains(restored, []byte("secret-key")) {
		t.Fatalf("restored=%s err=%v", restored, err)
	}
}

func TestDecodeEncryptionKeyRequires32Bytes(t *testing.T) {
	encoded := "BwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwcHBwc="
	key, err := DecodeEncryptionKey(encoded)
	if err != nil || len(key) != 32 {
		t.Fatalf("key=%d err=%v", len(key), err)
	}
	if _, err := DecodeEncryptionKey("short"); err == nil {
		t.Fatal("expected invalid key to fail")
	}
}
