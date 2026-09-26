package sandbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"os"
	"strings"
)

func getMasterKey() []byte {
	k := os.Getenv("ENCRYPTION_MASTER_KEY")
	if k == "" {
		k = "triagehubs_dev_default_key_change_me"
		log.Println("[WARNING] ENCRYPTION_MASTER_KEY not set, using insecure default.")
	}
	hash := sha256.Sum256([]byte(k))
	return hash[:]
}

// EncryptAPIKey encrypts sensitive keys using AES-256-GCM
func EncryptAPIKey(plaintext string) (string, error) {
	if strings.TrimSpace(plaintext) == "" {
		return "", nil
	}
	block, err := aes.NewCipher(getMasterKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err = io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)
	return "enc:" + hex.EncodeToString(ciphertext), nil
}

// DecryptAPIKey decrypts an AES-256-GCM ciphertext
func DecryptAPIKey(cipherHex string) (string, error) {
	if !strings.HasPrefix(cipherHex, "enc:") {
		// Plaintext legacy fallback
		return cipherHex, nil
	}
	rawHex := strings.TrimPrefix(cipherHex, "enc:")
	data, err := hex.DecodeString(rawHex)
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(getMasterKey())
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// MaskAPIKey produces a safe UI identifier, e.g. "sk-ant-••••••••4a2e"
func MaskAPIKey(key string) string {
	if key == "" {
		return "Managed Pool"
	}
	plain, err := DecryptAPIKey(key)
	if err != nil || plain == "" {
		return "••••••••"
	}
	if len(plain) <= 8 {
		return "••••••••"
	}
	return plain[:7] + "••••••••" + plain[len(plain)-4:]
}
