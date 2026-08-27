package service

import (
	"bytes"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestAuditPartCodecRequiresExactly256BitKey(t *testing.T) {
	for _, size := range []int{0, 1, 16, 31, 33, 64} {
		_, err := NewAuditPartCodec(bytes.Repeat([]byte{1}, size), "v1")
		require.ErrorIs(t, err, ErrAuditCodecKeyLength, "size=%d", size)
	}
	codec, err := NewAuditPartCodec(bytes.Repeat([]byte{1}, 32), "v1")
	require.NoError(t, err)
	require.NotNil(t, codec)
}

func TestAuditPartCodecRoundTripAndRandomNonce(t *testing.T) {
	codec := mustAuditCodec(t, 1)
	aad := auditCodecAAD()
	plaintext := []byte("synthetic-auditFoundation-plaintext-never-persist")
	first, err := codec.Encrypt(aad, plaintext)
	require.NoError(t, err)
	second, err := codec.Encrypt(aad, plaintext)
	require.NoError(t, err)
	require.NotEqual(t, first.Nonce, second.Nonce)
	require.NotEqual(t, first.Ciphertext, second.Ciphertext)
	require.False(t, bytes.Contains(first.Ciphertext, plaintext))

	decoded, err := codec.Decrypt(aad, first)
	require.NoError(t, err)
	require.Equal(t, plaintext, decoded)
}

func TestAuditPartCodecRejectsWrongKeyAndEveryTamperClass(t *testing.T) {
	codec := mustAuditCodec(t, 2)
	aad := auditCodecAAD()
	part, err := codec.Encrypt(aad, []byte("synthetic-codec-tamper-value"))
	require.NoError(t, err)

	wrongKey := mustAuditCodec(t, 3)
	_, err = wrongKey.Decrypt(aad, part)
	require.ErrorIs(t, err, ErrAuditCodecAuth)

	mutations := map[string]func(EncryptedAuditPart) EncryptedAuditPart{
		"nonce":      func(p EncryptedAuditPart) EncryptedAuditPart { p.Nonce = cloneAndFlip(p.Nonce); return p },
		"ciphertext": func(p EncryptedAuditPart) EncryptedAuditPart { p.Ciphertext = cloneAndFlip(p.Ciphertext); return p },
		"tag":        func(p EncryptedAuditPart) EncryptedAuditPart { p.AuthTag = cloneAndFlip(p.AuthTag); return p },
		"ciphertext_hash": func(p EncryptedAuditPart) EncryptedAuditPart {
			p.CiphertextSHA256 = cloneAndFlip(p.CiphertextSHA256)
			return p
		},
		"plaintext_hash": func(p EncryptedAuditPart) EncryptedAuditPart {
			p.PlaintextSHA256 = cloneAndFlip(p.PlaintextSHA256)
			return p
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			_, err := codec.Decrypt(aad, mutate(cloneEncryptedAuditPart(part)))
			require.Error(t, err)
		})
	}

	aadMutations := map[string]func(AuditPartAAD) AuditPartAAD{
		"interaction_id":     func(v AuditPartAAD) AuditPartAAD { v.InteractionID = uuid.New(); return v },
		"gateway_request_id": func(v AuditPartAAD) AuditPartAAD { v.GatewayRequestID = uuid.New(); return v },
		"direction":          func(v AuditPartAAD) AuditPartAAD { v.Direction = "response"; return v },
		"sequence":           func(v AuditPartAAD) AuditPartAAD { v.Sequence++; return v },
		"admitted_at":        func(v AuditPartAAD) AuditPartAAD { v.AdmittedAt = v.AdmittedAt.Add(time.Nanosecond); return v },
		"key_version":        func(v AuditPartAAD) AuditPartAAD { v.KeyVersion = "v2"; return v },
	}
	for name, mutate := range aadMutations {
		t.Run("aad_"+name, func(t *testing.T) {
			_, err := codec.Decrypt(mutate(aad), cloneEncryptedAuditPart(part))
			require.Error(t, err)
		})
	}

	badVersion := cloneEncryptedAuditPart(part)
	badVersion.AADFormatVersion = "core-gateway-aad-v0"
	_, err = codec.Decrypt(aad, badVersion)
	require.ErrorIs(t, err, ErrAuditCodecMetadata)
}

func mustAuditCodec(t *testing.T, fill byte) *AuditPartCodec {
	t.Helper()
	codec, err := NewAuditPartCodec(bytes.Repeat([]byte{fill}, 32), "v1")
	require.NoError(t, err)
	return codec
}

func auditCodecAAD() AuditPartAAD {
	return AuditPartAAD{
		InteractionID:    uuid.MustParse("11111111-2222-4333-8444-555555555555"),
		GatewayRequestID: uuid.MustParse("aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee"),
		Direction:        "request",
		Sequence:         0,
		AdmittedAt:       time.Date(2026, 8, 4, 1, 2, 3, 456, time.UTC),
		KeyVersion:       "v1",
	}
}

func cloneAndFlip(value []byte) []byte {
	cloned := append([]byte(nil), value...)
	if len(cloned) > 0 {
		cloned[0] ^= 0x80
	}
	return cloned
}

func cloneEncryptedAuditPart(part EncryptedAuditPart) EncryptedAuditPart {
	part.Nonce = append([]byte(nil), part.Nonce...)
	part.Ciphertext = append([]byte(nil), part.Ciphertext...)
	part.AuthTag = append([]byte(nil), part.AuthTag...)
	part.PlaintextSHA256 = append([]byte(nil), part.PlaintextSHA256...)
	part.CiphertextSHA256 = append([]byte(nil), part.CiphertextSHA256...)
	return part
}

func TestAuditPartCodecErrorsDoNotExposeKeyMaterial(t *testing.T) {
	key := bytes.Repeat([]byte("K"), 32)
	_, err := NewAuditPartCodec(key[:31], "v1")
	require.True(t, errors.Is(err, ErrAuditCodecKeyLength))
	require.NotContains(t, err.Error(), string(key))
}
