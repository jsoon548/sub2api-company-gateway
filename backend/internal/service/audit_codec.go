package service

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
)

const AuditAADFormatVersion = "core-gateway-aad-v1"

var (
	ErrAuditCodecKeyLength = errors.New("audit content key must be exactly 32 bytes")
	ErrAuditCodecMetadata  = errors.New("invalid audit part metadata")
	ErrAuditCodecAuth      = errors.New("audit content authentication failed")
)

// AuditPartAAD is the complete authenticated metadata set frozen by the Core
// Gateway v1 contract. No caller-controlled extra fields are accepted.
type AuditPartAAD struct {
	InteractionID    uuid.UUID
	GatewayRequestID uuid.UUID
	Direction        string
	Sequence         int
	AdmittedAt       time.Time
	KeyVersion       string
}

// EncryptedAuditPart is safe for PostgreSQL persistence. The GCM tag is kept
// separate from ciphertext so both storage constraints and tamper tests are
// explicit.
type EncryptedAuditPart struct {
	Nonce            []byte
	Ciphertext       []byte
	AuthTag          []byte
	KeyVersion       string
	AADFormatVersion string
	PlaintextLength  int64
	CiphertextLength int64
	PlaintextSHA256  []byte
	CiphertextSHA256 []byte
}

type AuditPartCodec struct {
	aead       cipher.AEAD
	keyVersion string
}

func NewAuditPartCodec(key []byte, keyVersion string) (*AuditPartCodec, error) {
	if len(key) != 32 {
		return nil, ErrAuditCodecKeyLength
	}
	if keyVersion == "" {
		return nil, ErrAuditCodecMetadata
	}
	keyCopy := append([]byte(nil), key...)
	block, err := aes.NewCipher(keyCopy)
	for i := range keyCopy {
		keyCopy[i] = 0
	}
	if err != nil {
		return nil, fmt.Errorf("create audit cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create audit gcm: %w", err)
	}
	return &AuditPartCodec{aead: aead, keyVersion: keyVersion}, nil
}

func (c *AuditPartCodec) Encrypt(aad AuditPartAAD, plaintext []byte) (EncryptedAuditPart, error) {
	encodedAAD, err := c.encodeAAD(aad)
	if err != nil {
		return EncryptedAuditPart{}, err
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return EncryptedAuditPart{}, fmt.Errorf("generate audit nonce: %w", err)
	}
	sealed := c.aead.Seal(nil, nonce, plaintext, encodedAAD)
	tagStart := len(sealed) - c.aead.Overhead()
	ciphertext := append([]byte(nil), sealed[:tagStart]...)
	tag := append([]byte(nil), sealed[tagStart:]...)
	plainHash := sha256.Sum256(plaintext)
	cipherHashInput := append(append([]byte(nil), ciphertext...), tag...)
	cipherHash := sha256.Sum256(cipherHashInput)
	return EncryptedAuditPart{
		Nonce:            nonce,
		Ciphertext:       ciphertext,
		AuthTag:          tag,
		KeyVersion:       c.keyVersion,
		AADFormatVersion: AuditAADFormatVersion,
		PlaintextLength:  int64(len(plaintext)),
		CiphertextLength: int64(len(ciphertext)),
		PlaintextSHA256:  append([]byte(nil), plainHash[:]...),
		CiphertextSHA256: append([]byte(nil), cipherHash[:]...),
	}, nil
}

func (c *AuditPartCodec) Decrypt(aad AuditPartAAD, part EncryptedAuditPart) ([]byte, error) {
	if part.KeyVersion != c.keyVersion || part.AADFormatVersion != AuditAADFormatVersion ||
		len(part.Nonce) != c.aead.NonceSize() || len(part.AuthTag) != c.aead.Overhead() ||
		part.CiphertextLength != int64(len(part.Ciphertext)) || part.PlaintextLength < 0 ||
		len(part.PlaintextSHA256) != sha256.Size || len(part.CiphertextSHA256) != sha256.Size {
		return nil, ErrAuditCodecMetadata
	}
	encodedAAD, err := c.encodeAAD(aad)
	if err != nil {
		return nil, err
	}
	cipherHashInput := append(append([]byte(nil), part.Ciphertext...), part.AuthTag...)
	cipherHash := sha256.Sum256(cipherHashInput)
	if !equalBytes(cipherHash[:], part.CiphertextSHA256) {
		return nil, ErrAuditCodecAuth
	}
	sealed := append(append([]byte(nil), part.Ciphertext...), part.AuthTag...)
	plaintext, err := c.aead.Open(nil, part.Nonce, sealed, encodedAAD)
	if err != nil {
		return nil, ErrAuditCodecAuth
	}
	plainHash := sha256.Sum256(plaintext)
	if int64(len(plaintext)) != part.PlaintextLength || !equalBytes(plainHash[:], part.PlaintextSHA256) {
		return nil, ErrAuditCodecAuth
	}
	return plaintext, nil
}

func (c *AuditPartCodec) encodeAAD(aad AuditPartAAD) ([]byte, error) {
	if aad.InteractionID == uuid.Nil || aad.GatewayRequestID == uuid.Nil ||
		(aad.Direction != "request" && aad.Direction != "response") ||
		aad.Sequence < 0 || aad.AdmittedAt.IsZero() || aad.KeyVersion != c.keyVersion {
		return nil, ErrAuditCodecMetadata
	}
	canonical := struct {
		FormatVersion    string `json:"format_version"`
		InteractionID    string `json:"interaction_id"`
		GatewayRequestID string `json:"gateway_request_id"`
		Direction        string `json:"direction"`
		Sequence         int    `json:"sequence"`
		AdmittedAt       string `json:"admitted_at"`
		KeyVersion       string `json:"key_version"`
	}{
		FormatVersion:    AuditAADFormatVersion,
		InteractionID:    aad.InteractionID.String(),
		GatewayRequestID: aad.GatewayRequestID.String(),
		Direction:        aad.Direction,
		Sequence:         aad.Sequence,
		AdmittedAt:       aad.AdmittedAt.UTC().Format(time.RFC3339Nano),
		KeyVersion:       aad.KeyVersion,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, fmt.Errorf("encode audit aad: %w", err)
	}
	return encoded, nil
}

func equalBytes(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var diff byte
	for i := range a {
		diff |= a[i] ^ b[i]
	}
	return diff == 0
}
