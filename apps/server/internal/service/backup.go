package service

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"golang.org/x/crypto/argon2"
)

// OpenBerth backup wire format, v1:
//
//   Magic       [6] = "OBBK01"
//   SaltLen     [1] = 16
//   Salt        [16]
//   NonceLen    [1] = 12
//   Nonce       [12]
//   AADLen      [2]  big-endian  (<= 1024)
//   AAD         [AADLen]   JSON: {"t":"<rfc3339>","admin":"<name>","ver":"<v>"}
//   Ciphertext  [...]  AES-256-GCM(gzipped tar || tag)
//
// The tag is part of the GCM stream (not stored separately). AAD is
// authenticated, so a backup with a tampered header fails to decrypt.
const (
	backupMagic     = "OBBK01"
	backupSaltLen   = 16
	backupNonceLen  = 12
	backupMaxAADLen = 1024
	backupMinPass   = 12

	argon2Time    uint32 = 3
	argon2Memory  uint32 = 64 * 1024 // 64 MiB
	argon2Threads uint8  = 4
	argon2KeyLen  uint32 = 32
)

// BackupAAD is what gets bound into the GCM seal as associated data.
// Any tampering with these fields after emit (e.g. attacker rewriting
// the admin name) fails decryption.
type BackupAAD struct {
	Timestamp string `json:"t"`
	AdminUser string `json:"admin"`
	Version   string `json:"ver"`
}

// ValidateBackupPassphrase returns a user-friendly error if the
// passphrase is too short. Use it both at backup and restore time so
// the client gets a consistent message.
func ValidateBackupPassphrase(pass string) error {
	if len(pass) < backupMinPass {
		return fmt.Errorf("backup passphrase must be at least %d characters", backupMinPass)
	}
	return nil
}

// WrapBackup emits the v1 header to out and returns a writer that
// encrypts further writes. Caller writes the gzipped tar stream, then
// Close() to flush the final GCM block. Uses Argon2id(64 MiB, t=3).
func WrapBackup(out io.Writer, pass string, aad BackupAAD) (io.WriteCloser, error) {
	if err := ValidateBackupPassphrase(pass); err != nil {
		return nil, err
	}

	salt := make([]byte, backupSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, fmt.Errorf("generate salt: %w", err)
	}
	nonce := make([]byte, backupNonceLen)
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}

	aadBytes, err := json.Marshal(aad)
	if err != nil {
		return nil, fmt.Errorf("marshal aad: %w", err)
	}
	if len(aadBytes) > backupMaxAADLen {
		return nil, fmt.Errorf("aad too large: %d > %d", len(aadBytes), backupMaxAADLen)
	}

	if _, err := out.Write([]byte(backupMagic)); err != nil {
		return nil, err
	}
	if _, err := out.Write([]byte{byte(backupSaltLen)}); err != nil {
		return nil, err
	}
	if _, err := out.Write(salt); err != nil {
		return nil, err
	}
	if _, err := out.Write([]byte{byte(backupNonceLen)}); err != nil {
		return nil, err
	}
	if _, err := out.Write(nonce); err != nil {
		return nil, err
	}
	var aadLenBuf [2]byte
	binary.BigEndian.PutUint16(aadLenBuf[:], uint16(len(aadBytes)))
	if _, err := out.Write(aadLenBuf[:]); err != nil {
		return nil, err
	}
	if _, err := out.Write(aadBytes); err != nil {
		return nil, err
	}

	key := argon2.IDKey([]byte(pass), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &gcmWriter{out: out, gcm: gcm, nonce: nonce, aad: aadBytes}, nil
}

// UnwrapBackup reads the header, validates the passphrase by trial
// decryption, and returns an io.Reader that yields the decrypted
// gzipped-tar stream. Fails fast on wrong passphrase or tampered bytes.
//
// If the input does not start with the v1 magic, returns
// ErrLegacyUnencrypted so the caller can decide whether to accept the
// older unencrypted format (gated behind an explicit opt-in flag).
func UnwrapBackup(in io.Reader, pass string) (io.Reader, *BackupAAD, error) {
	var magic [6]byte
	if _, err := io.ReadFull(in, magic[:]); err != nil {
		return nil, nil, fmt.Errorf("read magic: %w", err)
	}
	if string(magic[:]) != backupMagic {
		// Give the caller enough to distinguish old-format backups
		// from genuinely-corrupted input. Push the magic bytes back
		// via a prefix reader so the legacy path can re-consume them.
		return nil, nil, &LegacyUnencryptedBackupError{prefix: magic[:]}
	}
	if err := ValidateBackupPassphrase(pass); err != nil {
		return nil, nil, err
	}

	var saltLen [1]byte
	if _, err := io.ReadFull(in, saltLen[:]); err != nil {
		return nil, nil, fmt.Errorf("read salt len: %w", err)
	}
	if saltLen[0] == 0 || saltLen[0] > 64 {
		return nil, nil, errors.New("invalid salt length")
	}
	salt := make([]byte, saltLen[0])
	if _, err := io.ReadFull(in, salt); err != nil {
		return nil, nil, fmt.Errorf("read salt: %w", err)
	}

	var nonceLen [1]byte
	if _, err := io.ReadFull(in, nonceLen[:]); err != nil {
		return nil, nil, fmt.Errorf("read nonce len: %w", err)
	}
	if nonceLen[0] == 0 || nonceLen[0] > 32 {
		return nil, nil, errors.New("invalid nonce length")
	}
	nonce := make([]byte, nonceLen[0])
	if _, err := io.ReadFull(in, nonce); err != nil {
		return nil, nil, fmt.Errorf("read nonce: %w", err)
	}

	var aadLenBuf [2]byte
	if _, err := io.ReadFull(in, aadLenBuf[:]); err != nil {
		return nil, nil, fmt.Errorf("read aad len: %w", err)
	}
	aadLen := binary.BigEndian.Uint16(aadLenBuf[:])
	if aadLen > backupMaxAADLen {
		return nil, nil, errors.New("aad too large")
	}
	aadBytes := make([]byte, aadLen)
	if _, err := io.ReadFull(in, aadBytes); err != nil {
		return nil, nil, fmt.Errorf("read aad: %w", err)
	}
	var aad BackupAAD
	_ = json.Unmarshal(aadBytes, &aad)

	// Consume the rest of the stream into memory so we can AEAD-open it.
	// GCM requires the whole ciphertext+tag for authentication. Admins
	// with multi-GB backups are already limited by http.MaxBytesReader;
	// reasonable instances fit in RAM.
	ciphertext, err := io.ReadAll(in)
	if err != nil {
		return nil, nil, fmt.Errorf("read ciphertext: %w", err)
	}

	key := argon2.IDKey([]byte(pass), salt, argon2Time, argon2Memory, argon2Threads, argon2KeyLen)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, err
	}
	plaintext, err := gcm.Open(nil, nonce, ciphertext, aadBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("decrypt (wrong passphrase or corrupted backup): %w", err)
	}

	return bytes.NewReader(plaintext), &aad, nil
}

// LegacyUnencryptedBackupError signals to the caller that the input is a
// pre-v1 tarball (raw gzip). The prefix field carries the bytes already
// consumed so the caller can prepend them when reconstructing the
// original stream for legacy handling.
type LegacyUnencryptedBackupError struct {
	prefix []byte
}

func (e *LegacyUnencryptedBackupError) Error() string {
	return "backup is in pre-passphrase format; resubmit with legacyUnencrypted=true"
}

// Prefix returns the bytes already consumed from the stream (typically
// the would-be magic — actually the first 6 bytes of the gzip stream).
func (e *LegacyUnencryptedBackupError) Prefix() []byte { return e.prefix }

// ── Internal helpers ────────────────────────────────────────────────

type gcmWriter struct {
	out   io.Writer
	gcm   cipher.AEAD
	nonce []byte
	aad   []byte
	buf   []byte
	done  bool
}

func (w *gcmWriter) Write(p []byte) (int, error) {
	if w.done {
		return 0, errors.New("gcmWriter: write after close")
	}
	w.buf = append(w.buf, p...)
	return len(p), nil
}

func (w *gcmWriter) Close() error {
	if w.done {
		return nil
	}
	w.done = true
	ct := w.gcm.Seal(nil, w.nonce, w.buf, w.aad)
	_, err := w.out.Write(ct)
	return err
}

