package bucket

import (
	"crypto/md5" //nolint:gosec // MD5 is required by the S3 SSE-C protocol as a key checksum.
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strings"
)

// customerKeyAlgorithm is the only algorithm that S3, GCS and Azure support
// for customer-provided encryption keys.
const customerKeyAlgorithm = "AES256"

// CustomerKey is a 256-bit key for server-side encryption with a customer-provided key:
// SSE-C on S3, customer-supplied encryption keys (CSEK) on GCS, and
// customer-provided keys (CPK) on Azure Blob Storage.
type CustomerKey struct {
	raw []byte
}

// ParseCustomerKey parses a base64-encoded 256-bit customer-provided key.
func ParseCustomerKey(b64 string) (*CustomerKey, error) {
	raw, err := base64.StdEncoding.DecodeString(strings.TrimSpace(b64))
	if err != nil {
		return nil, fmt.Errorf("customer key is not valid base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("customer key must be 32 bytes (256 bits), got %d bytes", len(raw))
	}
	return &CustomerKey{raw: raw}, nil
}

// base64 returns the base64-encoded key.
func (k *CustomerKey) base64() string {
	return base64.StdEncoding.EncodeToString(k.raw)
}

// md5Base64 returns the base64-encoded MD5 digest of the key, used by S3.
func (k *CustomerKey) md5Base64() string {
	sum := md5.Sum(k.raw) //nolint:gosec
	return base64.StdEncoding.EncodeToString(sum[:])
}

// sha256Base64 returns the base64-encoded SHA-256 digest of the key, used by GCS and Azure.
func (k *CustomerKey) sha256Base64() string {
	sum := sha256.Sum256(k.raw)
	return base64.StdEncoding.EncodeToString(sum[:])
}
